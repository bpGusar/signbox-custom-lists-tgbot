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
)

type PendingOp struct {
	ID             string
	ChatID         int64
	Kind           ActionKind
	ListType       lists.EntryType
	ListPath       string
	Values         []string
	DisableValues  []string
	Created        time.Time
}

type SessionStore struct {
	mu   sync.Mutex
	ops  map[string]*PendingOp
	ttl  time.Duration
}

func NewSessionStore() *SessionStore {
	s := &SessionStore{
		ops: make(map[string]*PendingOp),
		ttl: 30 * time.Minute,
	}
	go s.cleanupLoop()
	return s
}

func (s *SessionStore) Create(chatID int64, kind ActionKind, listType lists.EntryType, listPath string, values, disableValues []string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := randomID()
	s.ops[id] = &PendingOp{
		ID:            id,
		ChatID:        chatID,
		Kind:          kind,
		ListType:      listType,
		ListPath:      listPath,
		Values:        append([]string(nil), values...),
		DisableValues: append([]string(nil), disableValues...),
		Created:       time.Now(),
	}
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
		s.mu.Unlock()
	}
}

func randomID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
