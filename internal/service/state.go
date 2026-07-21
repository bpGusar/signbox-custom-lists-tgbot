package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type State struct {
	FilesChangedAt *time.Time `json:"files_changed_at,omitempty"`
	LastRestartAt  *time.Time `json:"last_restart_at,omitempty"`
	ServiceStale   bool       `json:"service_stale"`
	OwnerChatID    *int64     `json:"owner_chat_id,omitempty"`
}

type Manager struct {
	mu   sync.Mutex
	path string
}

func NewManager(path string) *Manager {
	return &Manager{path: path}
}

func (m *Manager) Load() (*State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadLocked()
}

func (m *Manager) loadLocked() (*State, error) {
	data, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			return &State{}, nil
		}
		return nil, err
	}

	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return &State{}, nil
	}
	return &st, nil
}

func (m *Manager) Save(st *State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveLocked(st)
}

func (m *Manager) saveLocked(st *State) error {
	dir := filepath.Dir(m.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".state-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, m.path)
}

func (m *Manager) MarkFilesChanged() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.loadLocked()
	if err != nil {
		return err
	}
	now := time.Now()
	st.FilesChangedAt = &now
	st.ServiceStale = true
	return m.saveLocked(st)
}

func (m *Manager) MarkRestarted() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.loadLocked()
	if err != nil {
		return err
	}
	now := time.Now()
	st.LastRestartAt = &now
	st.ServiceStale = false
	return m.saveLocked(st)
}

func (m *Manager) StaleBanner() string {
	st, err := m.Load()
	if err != nil || !st.ServiceStale || st.FilesChangedAt == nil {
		return ""
	}
	return "⚠️ Файлы изменены (" + st.FilesChangedAt.Format("02.01.2006 15:04") + "). Сервис не перезапускался с тех пор."
}

// ClaimOrCheckOwner returns the bot's owner chat ID, atomically claiming
// chatID as the owner if none is set yet. isOwner reports whether chatID
// is (now, or already) the owner. Once an owner is set, every other chat ID
// is rejected until the owner is cleared (e.g. by removing owner_chat_id
// from the state file).
func (m *Manager) ClaimOrCheckOwner(chatID int64) (ownerChatID int64, isOwner bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.loadLocked()
	if err != nil {
		return 0, false, err
	}

	if st.OwnerChatID == nil {
		id := chatID
		st.OwnerChatID = &id
		if err := m.saveLocked(st); err != nil {
			return 0, false, err
		}
		return chatID, true, nil
	}

	return *st.OwnerChatID, *st.OwnerChatID == chatID, nil
}
