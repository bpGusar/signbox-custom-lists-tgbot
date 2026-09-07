package bot

import (
	"testing"

	"github.com/go-telegram/bot/models"

	"lst-signbox-lists-tgbot/internal/frp"
)

func TestActorID(t *testing.T) {
	cb := &models.Update{CallbackQuery: &models.CallbackQuery{From: models.User{ID: 42}}}
	if got := actorID(cb); got != 42 {
		t.Fatalf("callback actor = %d, want 42", got)
	}
	msg := &models.Update{Message: &models.Message{From: &models.User{ID: 7}}}
	if got := actorID(msg); got != 7 {
		t.Fatalf("message actor = %d, want 7", got)
	}
	if got := actorID(&models.Update{}); got != 0 {
		t.Fatalf("empty actor = %d, want 0", got)
	}
}

func TestFrpAuthorized_onlyOwner(t *testing.T) {
	ok := &models.Update{CallbackQuery: &models.CallbackQuery{From: models.User{ID: frpAllowedUserID}}}
	if !frpAuthorized(ok) {
		t.Fatal("the designated user must be authorized")
	}
	for _, id := range []int64{0, 1, frpAllowedUserID + 1, 340814763} {
		u := &models.Update{CallbackQuery: &models.CallbackQuery{From: models.User{ID: id}}}
		if frpAuthorized(u) {
			t.Fatalf("user %d must not be authorized for frp", id)
		}
	}
}

func TestFrpStateLine(t *testing.T) {
	cases := map[string]string{
		frp.StateOK:            "✅ настроено и работает",
		frp.StateDisabled:      "⏸ выключено (frp не включён)",
		frp.StateNotConfigured: "⚙️ не хватает адреса VPS или токена",
		frp.StateRemoved:       "🗑 удалено",
		"":                     "— установка ещё не запускалась",
		"running":              "⏳ выполняется…",
	}
	for state, want := range cases {
		if got := frpStateLine(frp.Info{State: state}); got != want {
			t.Errorf("frpStateLine(%q) = %q, want %q", state, got, want)
		}
	}
	if got := frpStateLine(frp.Info{State: frp.StateError, Detail: "config verify failed"}); got != "❌ ошибка: config verify failed" {
		t.Errorf("error state line = %q", got)
	}
}

func TestFrpPanelKeyboard_hasAllFields(t *testing.T) {
	a := &App{}
	kb := a.frpPanelKeyboard(frp.Info{Enabled: true})
	var cbs []string
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			cbs = append(cbs, btn.CallbackData)
		}
	}
	for _, f := range frp.EditableFields {
		want := frpCbPrefix + "edit:" + string(f)
		found := false
		for _, cb := range cbs {
			if cb == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing edit button for %s (%q)", f, want)
		}
	}
	if !containsAll(cbs, frpCbPrefix+"toggle", frpCbPrefix+"install", frpCbPrefix+"log", frpCbPrefix+"remove") {
		t.Errorf("panel keyboard missing an action button: %v", cbs)
	}
}
