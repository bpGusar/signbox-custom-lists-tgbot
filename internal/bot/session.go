package bot

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"lst-signbox-lists-tgbot/internal/lists"
	"lst-signbox-lists-tgbot/internal/probe"
	"lst-signbox-lists-tgbot/internal/proxylink"
)

type ActionKind int

const (
	ActionAdd ActionKind = iota
	ActionDelete
	ActionDisable
	ActionAddAll
	ActionAddNew
	ActionDisableConfirm
	ActionDisableAddMissing
	ActionStartCreate
	ActionStartRetry
	// ActionMove retags values that already exist in another category.
	ActionMove
	// ActionCategory backs the action buttons on a category card.
	ActionCategory
	// ActionBind adds a list file to a podkop section.
	ActionBind
)

type PendingOp struct {
	ID            string
	ChatID        int64
	Kind          ActionKind
	ListType      lists.EntryType
	ListPath      string
	Values        []string
	DisableValues []string
	// Section is the podkop section the list belongs to, empty for the file
	// pair from the bot's own config.
	Section string
	// Category is the target category for adds and moves, or the subject of a
	// category card.
	Category string
	// Origin is the category values are being moved out of, for reporting.
	Origin  map[string]string
	Created time.Time
}

// awaitKind says how the next plain text message from a chat should be read.
type awaitKind int

const (
	awaitNone awaitKind = iota
	// awaitNewCategory: the text names a new category for a pending add.
	awaitNewCategory
	// awaitRename: the text is the new name for the category in awaitOpID.
	awaitRename
	// awaitBindPath: the text is a file path to bind to a podkop section.
	awaitBindPath
	// awaitMaxPing: the text is a latency threshold for a proxy import.
	awaitMaxPing
)

// chatState holds the one thing no message can carry on its own: what the bot
// expects the chat's next plain text message to mean. Category picks do not
// need it — they travel inside the tapped command.
type chatState struct {
	await     awaitKind
	awaitOpID string
	// proxyFile says the chat has been shown what a subscription file must
	// look like, so a document may now be read as one. It lives beside await
	// rather than inside it: a document and a text message are different
	// channels, and typing something must not disarm the file upload.
	proxyFile bool
	updated   time.Time
}

// linkResult is one link's measurement, whichever stage produced it.
type linkResult struct {
	Latency time.Duration
	OK      bool
	// Reason explains a failure in a form fit for a chat message.
	Reason string
}

// ProxyImport is a subscription file being turned into podkop's link list. It
// does not fit PendingOp: it outlives several screens, carries a running
// measurement, and is mutated from a background goroutine.
type ProxyImport struct {
	ID       string
	ChatID   int64
	FileName string
	Links    []proxylink.Link
	Stats    proxylink.Stats
	MaxPing  time.Duration
	// Results is keyed by Link.DedupKey.
	Results map[string]linkResult
	// Method is what the surviving numbers were measured with.
	Method probe.Method
	// Tunnel says stage B ran, so Method is not the whole story.
	Tunnel bool
	// TunnelNote explains why stage B did not run, when it did not.
	TunnelNote string
	Section    string
	Running    bool
	// MessageID is the message the progress and the report are written into,
	// because the run outlives the update that started it.
	MessageID int
	cancel    context.CancelFunc
	Created   time.Time
}

// Passed is the links that made it under the threshold, fastest first.
func (p *ProxyImport) Passed() []proxylink.Link {
	var out []proxylink.Link
	for _, l := range p.Links {
		if r, ok := p.Results[l.DedupKey()]; ok && r.OK && r.Latency <= p.MaxPing {
			out = append(out, l)
		}
	}
	sortByLatency(out, p.Results)
	return out
}

// Failed is everything else: too slow, or never answered.
func (p *ProxyImport) Failed() []proxylink.Link {
	var out []proxylink.Link
	for _, l := range p.Links {
		if r, ok := p.Results[l.DedupKey()]; !ok || !r.OK || r.Latency > p.MaxPing {
			out = append(out, l)
		}
	}
	return out
}

func sortByLatency(links []proxylink.Link, results map[string]linkResult) {
	for i := 1; i < len(links); i++ {
		for j := i; j > 0 && results[links[j].DedupKey()].Latency < results[links[j-1].DedupKey()].Latency; j-- {
			links[j], links[j-1] = links[j-1], links[j]
		}
	}
}

type SessionStore struct {
	mu      sync.Mutex
	ops     map[string]*PendingOp
	imports map[string]*ProxyImport
	chats   map[int64]*chatState
	ttl     time.Duration
}

func NewSessionStore() *SessionStore {
	s := &SessionStore{
		ops:     make(map[string]*PendingOp),
		imports: make(map[string]*ProxyImport),
		chats:   make(map[int64]*chatState),
		ttl:     30 * time.Minute,
	}
	go s.cleanupLoop()
	return s
}

// clone is what leaves the store: an import is mutated by the measuring
// goroutine while the chat handlers read it, so nothing outside the store's
// lock ever holds the stored struct itself. The cancel function stays behind —
// stopping a run goes through the store.
func (p *ProxyImport) clone() *ProxyImport {
	out := *p
	out.cancel = nil
	out.Links = append([]proxylink.Link(nil), p.Links...)
	out.Results = make(map[string]linkResult, len(p.Results))
	for k, v := range p.Results {
		out.Results[k] = v
	}
	return &out
}

// CreateImport stores imp under a fresh id and returns a copy of it.
func (s *SessionStore) CreateImport(imp ProxyImport) *ProxyImport {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored := imp
	stored.ID = randomID()
	stored.Created = time.Now()
	if stored.Results == nil {
		stored.Results = make(map[string]linkResult)
	}
	s.imports[stored.ID] = &stored
	return stored.clone()
}

func (s *SessionStore) GetImport(id string) (*ProxyImport, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	imp, ok := s.imports[id]
	if !ok {
		return nil, false
	}
	if time.Since(imp.Created) > s.ttl {
		delete(s.imports, id)
		return nil, false
	}
	return imp.clone(), true
}

// UpdateImport applies apply under the store's lock, which is the only thing
// standing between the measuring goroutine and the chat handlers.
func (s *SessionStore) UpdateImport(id string, apply func(*ProxyImport)) (*ProxyImport, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	imp, ok := s.imports[id]
	if !ok {
		return nil, false
	}
	apply(imp)
	return imp.clone(), true
}

func (s *SessionStore) DeleteImport(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if imp, ok := s.imports[id]; ok && imp.cancel != nil {
		imp.cancel()
	}
	delete(s.imports, id)
}

// Create stores op and returns its id; Values and Origin are copied so the
// caller can keep mutating its own slices.
func (s *SessionStore) Create(op PendingOp) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := randomID()
	stored := op
	stored.ID = id
	stored.Values = append([]string(nil), op.Values...)
	stored.DisableValues = append([]string(nil), op.DisableValues...)
	if op.Origin != nil {
		stored.Origin = make(map[string]string, len(op.Origin))
		for k, v := range op.Origin {
			stored.Origin[k] = v
		}
	}
	stored.Created = time.Now()
	s.ops[id] = &stored
	return id
}

func (s *SessionStore) Get(id string) (*PendingOp, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	op, ok := s.ops[id]
	if !ok {
		return nil, false
	}
	if time.Since(op.Created) > s.ttl {
		delete(s.ops, id)
		return nil, false
	}
	return op, true
}

func (s *SessionStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.ops, id)
}

// chatLocked returns the chat's state, dropping it first if it went stale.
// Callers must hold s.mu.
func (s *SessionStore) chatLocked(chatID int64) *chatState {
	st, ok := s.chats[chatID]
	if ok && time.Since(st.updated) > s.ttl {
		delete(s.chats, chatID)
		ok = false
	}
	if !ok {
		st = &chatState{}
		s.chats[chatID] = st
	}
	st.updated = time.Now()
	return st
}

// Await arms the chat so the next plain text message is read as kind.
func (s *SessionStore) Await(chatID int64, kind awaitKind, opID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.chatLocked(chatID)
	st.await = kind
	st.awaitOpID = opID
}

// TakeAwait consumes the armed expectation, if any.
func (s *SessionStore) TakeAwait(chatID int64) (awaitKind, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.chatLocked(chatID)
	kind, opID := st.await, st.awaitOpID
	st.await, st.awaitOpID = awaitNone, ""
	return kind, opID
}

// ArmProxyFile lets the chat's next document be read as a subscription file.
func (s *SessionStore) ArmProxyFile(chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chatLocked(chatID).proxyFile = true
}

func (s *SessionStore) ProxyFileArmed(chatID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chatLocked(chatID).proxyFile
}

func (s *SessionStore) DisarmProxyFile(chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chatLocked(chatID).proxyFile = false
}

func (s *SessionStore) ClearAwait(chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.chatLocked(chatID)
	st.await, st.awaitOpID = awaitNone, ""
}

func (s *SessionStore) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		s.mu.Lock()
		now := time.Now()
		for id, op := range s.ops {
			if now.Sub(op.Created) > s.ttl {
				delete(s.ops, id)
			}
		}
		for id, imp := range s.imports {
			if now.Sub(imp.Created) > s.ttl {
				if imp.cancel != nil {
					imp.cancel()
				}
				delete(s.imports, id)
			}
		}
		for id, st := range s.chats {
			if now.Sub(st.updated) > s.ttl {
				delete(s.chats, id)
			}
		}
		s.mu.Unlock()
	}
}

func randomID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
