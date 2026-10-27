package bot

import (
	"context"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"lst-signbox-lists-tgbot/internal/podkop"
)

var emptyListFiles = podkop.EmptyListFiles

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

// backendMiddleware refuses to work unless exactly one of podkop and forkop
// is installed: every list the bot edits belongs to one of their sections.
func (a *App) backendMiddleware(next tgbot.HandlerFunc) tgbot.HandlerFunc {
	return func(ctx context.Context, b *tgbot.Bot, update *models.Update) {
		_, err := a.backend()
		if err == nil {
			next(ctx, b, update)
			return
		}
		chatID, _ := updateChatID(update)
		a.logf(chatID, "backend_unavailable err=%v", err)
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
