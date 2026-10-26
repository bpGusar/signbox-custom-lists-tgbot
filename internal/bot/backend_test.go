package bot

import (
	"testing"

	"lst-signbox-lists-tgbot/internal/config"
	"lst-signbox-lists-tgbot/internal/podkop"
)

func appWithBackend(cmds []string, b podkop.Backend, err error) *App {
	return &App{
		cfg:           &config.Config{RestartCmds: cmds},
		detectBackend: func() (podkop.Backend, error) { return b, err },
	}
}

func TestSettingsShowForkopCheck(t *testing.T) {
	app := appWithBackend([]string{config.DefaultRestartCmd}, podkop.Forkop, nil)
	var texts []string
	for _, row := range app.settingsMenuInlineKeyboard().InlineKeyboard {
		for _, btn := range row {
			texts = append(texts, btn.Text)
		}
	}
	if !containsAll(texts, "🔄 Перезапустить forkop", menuBtnCheckBackend(podkop.Forkop)) {
		t.Fatalf("settings must follow forkop, got %v", texts)
	}
	if !containsNone(texts, menuBtnCheckBackend(podkop.Podkop)) {
		t.Fatalf("podkop must not be offered on a forkop router, got %v", texts)
	}
}

func TestBackendName(t *testing.T) {
	if got := appWithBackend(nil, podkop.Forkop, nil).backendName(); got != "forkop" {
		t.Fatalf("backendName = %q", got)
	}
	if got := appWithBackend(nil, podkop.Backend{}, podkop.ErrNotInstalled).backendName(); got != "podkop/forkop" {
		t.Fatalf("backendName without a backend = %q", got)
	}
}
