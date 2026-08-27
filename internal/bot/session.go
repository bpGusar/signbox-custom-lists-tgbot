package bot

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"lst-signbox-lists-tgbot/internal/lists"
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
)

// chatState holds the one thing no message can carry on its own: what the bot
// expects the chat's next plain text message to mean. Category picks do not
// need it — they travel inside the tapped command.
type chatState struct {
	await     awaitKind
	awaitOpID string
	updated   time.Time
}

type SessionStore struct {
	mu    sync.Mutex
	ops   map[string]*PendingOp
	chats map[int64]*chatState
	ttl   time.Duration
}

func NewSessionStore() *SessionStore {
	s := &SessionStore{
		ops:   make(map[string]*PendingOp),
		chats: make(map[int64]*chatState),
		ttl:   30 * time.Minute,
	}
	go s.cleanupLoop()
	return s
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
