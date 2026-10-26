package bot

import (
	"testing"

	"lst-signbox-lists-tgbot/internal/config"
	"lst-signbox-lists-tgbot/internal/podkop"
)

func appWithBackend(cmd, label string, b podkop.Backend, err error) *App {
	return &App{
		cfg:           &config.Config{RestartCmd: cmd, ServiceLabel: label},
		detectBackend: func() (podkop.Backend, error) { return b, err },
	}
}

func TestRestartFollowsInstalledBackend(t *testing.T) {
	cases := []struct {
		name      string
		cmd       string
		label     string
		backend   podkop.Backend
		err       error
		wantCmd   string
		wantLabel string
	}{
		{"podkop как был", "/etc/init.d/podkop restart", "podkop", podkop.Podkop, nil, "/etc/init.d/podkop restart", "podkop"},
		{"стоковая команда после перехода на forkop", "/etc/init.d/podkop restart", "podkop", podkop.Forkop, nil, "/etc/init.d/forkop restart", "forkop"},
		{"команда forkop остаётся своей", "/etc/init.d/forkop reload", "Forkop", podkop.Forkop, nil, "/etc/init.d/forkop reload", "Forkop"},
		{"своя подпись не трогается", "/etc/init.d/podkop restart", "VPN", podkop.Forkop, nil, "/etc/init.d/forkop restart", "VPN"},
		{"не podkop и не forkop", "/etc/init.d/sing-box restart", "sing-box", podkop.Forkop, nil, "/etc/init.d/sing-box restart", "sing-box"},
		{"ничего не установлено", "/etc/init.d/podkop restart", "podkop", podkop.Backend{}, podkop.ErrNotInstalled, "/etc/init.d/podkop restart", "podkop"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			app := appWithBackend(c.cmd, c.label, c.backend, c.err)
			if got := app.restartCmd(); got != c.wantCmd {
				t.Errorf("restartCmd = %q, want %q", got, c.wantCmd)
			}
			if got := app.serviceLabel(); got != c.wantLabel {
				t.Errorf("serviceLabel = %q, want %q", got, c.wantLabel)
			}
		})
	}
}

func TestSettingsShowForkopCheck(t *testing.T) {
	app := appWithBackend("/etc/init.d/podkop restart", "podkop", podkop.Forkop, nil)
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
	if got := appWithBackend("", "", podkop.Forkop, nil).backendName(); got != "forkop" {
		t.Fatalf("backendName = %q", got)
	}
	if got := appWithBackend("", "", podkop.Backend{}, podkop.ErrNotInstalled).backendName(); got != "podkop/forkop" {
		t.Fatalf("backendName without a backend = %q", got)
	}
}
