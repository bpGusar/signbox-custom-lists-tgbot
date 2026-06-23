package bot

import (
	"strings"
	"testing"

	"lst-signbox-lists-tgbot/internal/config"
)

func keyboardTexts(t *testing.T, opID string, newVals, active, disabled []string) []string {
	t.Helper()
	rows, _ := buildListInputKeyboard(opID, newVals, active, disabled)
	var texts []string
	for _, row := range rows {
		for _, btn := range row {
			texts = append(texts, btn.Text)
		}
	}
	return texts
}

func containsAll(texts []string, want ...string) bool {
	set := make(map[string]struct{}, len(texts))
	for _, t := range texts {
		set[t] = struct{}{}
	}
	for _, w := range want {
		if _, ok := set[w]; !ok {
			return false
		}
	}
	return true
}

func containsNone(texts []string, forbidden ...string) bool {
	set := make(map[string]struct{}, len(texts))
	for _, t := range texts {
		set[t] = struct{}{}
	}
	for _, f := range forbidden {
		if _, ok := set[f]; ok {
			return false
		}
	}
	return true
}

func TestBuildListInputKeyboard_onlyNew(t *testing.T) {
	texts := keyboardTexts(t, "op1", []string{"test.com", "example.org"}, nil, nil)

	if !containsAll(texts, "➕ Добавить", "❌ Отмена") {
		t.Fatalf("expected add and cancel, got %v", texts)
	}
	if !containsNone(texts, "⏸ Отключить", "🗑 Удалить", "✅ Включить", "➕ Добавить всё") {
		t.Fatalf("unexpected extra buttons: %v", texts)
	}
}

func TestBuildListInputKeyboard_mixedBuckets(t *testing.T) {
	texts := keyboardTexts(t, "op2",
		[]string{"test.com", "example.org"},
		[]string{"revanced.app"},
		[]string{"kick.com"},
	)

	if !containsAll(texts,
		"➕ Добавить", "✅ Включить", "➕ Добавить всё",
		"🗑 Удалить", "⏸ Отключить", "❌ Отмена",
	) {
		t.Fatalf("expected full action set, got %v", texts)
	}
}

func TestBuildListInputKeyboard_onlyDisabled(t *testing.T) {
	texts := keyboardTexts(t, "op3", nil, nil, []string{"kick.com"})

	if !containsAll(texts, "✅ Включить", "🗑 Удалить", "❌ Отмена") {
		t.Fatalf("expected enable/delete/cancel, got %v", texts)
	}
	if !containsNone(texts, "➕ Добавить", "⏸ Отключить", "➕ Добавить всё") {
		t.Fatalf("unexpected buttons: %v", texts)
	}
}

func TestBuildListInputKeyboard_onlyActive(t *testing.T) {
	texts := keyboardTexts(t, "op4", nil, []string{"revanced.app"}, nil)

	if !containsAll(texts, "🗑 Удалить", "⏸ Отключить", "❌ Отмена") {
		t.Fatalf("expected delete/disable/cancel, got %v", texts)
	}
	if !containsNone(texts, "➕ Добавить", "✅ Включить", "➕ Добавить всё") {
		t.Fatalf("unexpected buttons: %v", texts)
	}
}

func TestReplyKeyboard_onlyMainMenu(t *testing.T) {
	app := &App{}
	kb := app.replyKeyboard()
	if len(kb.Keyboard) != 1 || len(kb.Keyboard[0]) != 1 {
		t.Fatalf("expected single main menu button, got %#v", kb.Keyboard)
	}
	if kb.Keyboard[0][0].Text != menuBtnMainMenu {
		t.Fatalf("expected %q, got %q", menuBtnMainMenu, kb.Keyboard[0][0].Text)
	}
}

func TestMainMenuInlineKeyboard_baseActions(t *testing.T) {
	app := &App{cfg: &config.Config{}}
	kb := app.mainMenuInlineKeyboard()
	var texts []string
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			texts = append(texts, btn.Text)
		}
	}
	want := []string{
		menuBtnDownloadIP, menuBtnDownloadDomains,
		menuBtnViewIP, menuBtnViewDomains,
	}
	if !containsAll(texts, want...) {
		t.Fatalf("expected base menu buttons, got %v", texts)
	}
	if !containsNone(texts, menuBtnMainMenu, menuBtnCheckPodkop, menuBtnSettings) {
		t.Fatalf("unexpected buttons in inline menu: %v", texts)
	}
}

func TestMainMenuInlineKeyboard_withSettings(t *testing.T) {
	app := &App{cfg: &config.Config{
		RestartCmd:   "/etc/init.d/podkop restart",
		ServiceLabel: "Podkop",
	}}
	kb := app.mainMenuInlineKeyboard()
	var texts []string
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			texts = append(texts, btn.Text)
		}
	}
	if !containsAll(texts, menuBtnSettings) {
		t.Fatalf("expected settings button, got %v", texts)
	}
	if !containsNone(texts, app.menuBtnRestart(), menuBtnCheckPodkop) {
		t.Fatalf("restart and podkop should be in settings, not main menu: %v", texts)
	}
}

func TestSettingsMenuInlineKeyboard_withRestartAndPodkop(t *testing.T) {
	app := &App{cfg: &config.Config{
		RestartCmd:   "/etc/init.d/podkop restart",
		ServiceLabel: "Podkop",
	}}
	kb := app.settingsMenuInlineKeyboard()
	var texts []string
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			texts = append(texts, btn.Text)
		}
	}
	if !containsAll(texts, app.menuBtnRestart(), app.menuBtnAutoRestart(), menuBtnCheckPodkop, menuBtnMainMenu) {
		t.Fatalf("expected restart, auto restart, podkop and main menu buttons, got %v", texts)
	}
}

func TestSettingsMenuInlineKeyboard_singBoxOnlyRestart(t *testing.T) {
	app := &App{cfg: &config.Config{
		RestartCmd:   "/etc/init.d/sing-box restart",
		ServiceLabel: "sing-box",
	}}
	kb := app.settingsMenuInlineKeyboard()
	var texts []string
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			texts = append(texts, btn.Text)
		}
	}
	if !containsAll(texts, app.menuBtnRestart(), app.menuBtnAutoRestart(), menuBtnMainMenu) {
		t.Fatalf("expected restart, auto restart and main menu buttons, got %v", texts)
	}
	if !containsNone(texts, menuBtnCheckPodkop) {
		t.Fatalf("podkop check should not appear for sing-box, got %v", texts)
	}
}

func TestMenuBtnAutoRestart_states(t *testing.T) {
	on := &App{cfg: &config.Config{AutoRestart: true}}
	if on.menuBtnAutoRestart() != "✅ Автоперезапуск: вкл" {
		t.Fatalf("unexpected on label: %q", on.menuBtnAutoRestart())
	}
	off := &App{cfg: &config.Config{AutoRestart: false}}
	if off.menuBtnAutoRestart() != "⏸ Автоперезапуск: выкл" {
		t.Fatalf("unexpected off label: %q", off.menuBtnAutoRestart())
	}
}

func TestSettingsMenuText_autoRestartStatus(t *testing.T) {
	app := &App{cfg: &config.Config{
		RestartCmd:   "/etc/init.d/podkop restart",
		ServiceLabel: "podkop",
		AutoRestart:  true,
	}}
	text := app.settingsMenuText()
	if !strings.Contains(text, "Автоперезапуск podkop после изменения списков: вкл") {
		t.Fatalf("unexpected settings text: %q", text)
	}
}

func TestBackToMainMenuInlineKeyboard(t *testing.T) {
	app := &App{}
	kb := app.backToMainMenuInlineKeyboard()
	if len(kb.InlineKeyboard) != 1 || len(kb.InlineKeyboard[0]) != 1 {
		t.Fatalf("expected single back button, got %#v", kb.InlineKeyboard)
	}
	btn := kb.InlineKeyboard[0][0]
	if btn.Text != menuBtnMainMenu {
		t.Fatalf("expected %q, got %q", menuBtnMainMenu, btn.Text)
	}
	if btn.CallbackData != menuCbPrefix+"main_menu" {
		t.Fatalf("expected main_menu callback, got %q", btn.CallbackData)
	}
}
