package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type State struct {
	FilesChangedAt *time.Time `json:"files_changed_at,omitempty"`
	LastRestartAt  *time.Time `json:"last_restart_at,omitempty"`
	ServiceStale   bool       `json:"service_stale"`
}

type Manager struct {
	path string
}

func NewManager(path string) *Manager {
	return &Manager{path: path}
}

func (m *Manager) Load() (*State, error) {
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
	return os.Rename(tmpPath, m.path)
}

func (m *Manager) MarkFilesChanged() error {
	st, err := m.Load()
	if err != nil {
		return err
	}
	now := time.Now()
	st.FilesChangedAt = &now
	st.ServiceStale = true
	return m.Save(st)
}

func (m *Manager) MarkRestarted() error {
	st, err := m.Load()
	if err != nil {
		return err
	}
	now := time.Now()
	st.LastRestartAt = &now
	st.ServiceStale = false
	return m.Save(st)
}

func (m *Manager) StaleBanner() string {
	st, err := m.Load()
	if err != nil || !st.ServiceStale || st.FilesChangedAt == nil {
		return ""
	}
	return "⚠️ Файлы изменены (" + st.FilesChangedAt.Format("02.01.2006 15:04") + "). Сервис не перезапускался с тех пор."
}
