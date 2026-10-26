package bot

import (
	"context"
	"errors"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"lst-signbox-lists-tgbot/internal/podkop"
)

func (a *App) backend() (podkop.Backend, error) {
	if a.detectBackend != nil {
		return a.detectBackend()
	}
	return podkop.Detect()
}

// backendName names the routing package in messages, both of them when the
// router has neither.
func (a *App) backendName() string {
	if b, err := a.backend(); err == nil {
		return b.Name
	}
	return "podkop/forkop"
}

func (a *App) backendTitle() string {
	if b, err := a.backend(); err == nil {
		return b.Title
	}
	return "Podkop/Forkop"
}

func otherBackend(b podkop.Backend) podkop.Backend {
	if b.IsForkop() {
		return podkop.Podkop
	}
	return podkop.Forkop
}

func mentionsBackend(s string, b podkop.Backend) bool {
	return strings.Contains(strings.ToLower(s), b.Name)
}

// managedBackend is the backend the restart command is about. A command
// written for the other package — the stock podkop one on a router that has
// since moved to forkop — is taken as meant for the one installed.
func (a *App) managedBackend() (podkop.Backend, bool) {
	cmd := a.cfg.RestartCmd
	if !mentionsBackend(cmd, podkop.Podkop) && !mentionsBackend(cmd, podkop.Forkop) {
		return podkop.Backend{}, false
	}
	b, err := a.backend()
	if err != nil {
		return podkop.Backend{}, false
	}
	return b, true
}

func (a *App) restartCmd() string {
	b, ok := a.managedBackend()
	if !ok || mentionsBackend(a.cfg.RestartCmd, b) {
		return a.cfg.RestartCmd
	}
	return b.InitScript() + " restart"
}

func (a *App) serviceLabel() string {
	b, ok := a.managedBackend()
	if ok && strings.EqualFold(strings.TrimSpace(a.cfg.ServiceLabel), otherBackend(b).Name) {
		return b.Name
	}
	return a.cfg.ServiceLabel
}

// backendMiddleware refuses to work while podkop and forkop are both
// installed: their configs would disagree about which lists are routed where.
func (a *App) backendMiddleware(next tgbot.HandlerFunc) tgbot.HandlerFunc {
	return func(ctx context.Context, b *tgbot.Bot, update *models.Update) {
		_, err := a.backend()
		if !errors.Is(err, podkop.ErrBothInstalled) {
			next(ctx, b, update)
			return
		}
		chatID, _ := updateChatID(update)
		a.logf(chatID, "backend_conflict err=%v", err)
		text := "⛔️ " + err.Error()
		if update.CallbackQuery != nil {
			_, _ = b.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
				CallbackQueryID: update.CallbackQuery.ID,
				Text:            text,
				ShowAlert:       true,
			})
			return
		}
		if chatID != 0 {
			_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{ChatID: chatID, Text: text})
		}
	}
}
