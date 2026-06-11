package bot

import (
	"context"
	"fmt"
	"log"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"lists-tg/internal/config"
	"lists-tg/internal/lists"
	"lists-tg/internal/service"
)

type App struct {
	cfg     *config.Config
	svc     *service.Manager
	sess    *SessionStore
	ready   map[int64]bool
}

func Run(ctx context.Context, cfg *config.Config) error {
	if !cfg.Enabled {
		log.Println("lists-tg disabled in config")
		select {}
	}
	if cfg.Token == "" {
		return fmt.Errorf("telegram token is empty")
	}

	app := &App{
		cfg:   cfg,
		svc:   service.NewManager(cfg.StatePath),
		sess:  NewSessionStore(),
		ready: make(map[int64]bool),
	}

	opts := []tgbot.Option{
		tgbot.WithDefaultHandler(app.defaultHandler),
	}

	b, err := tgbot.New(cfg.Token, opts...)
	if err != nil {
		return err
	}

	b.RegisterHandler(tgbot.HandlerTypeMessageText, "/start", tgbot.MatchTypeExact, app.handleStart)
	b.RegisterHandler(tgbot.HandlerTypeCallbackQueryData, "s:", tgbot.MatchTypePrefix, app.handleCallback)

	log.Println("lists-tg started")
	b.Start(ctx)
	return nil
}

func (a *App) defaultHandler(ctx context.Context, b *tgbot.Bot, update *models.Update) {
	if update.Message == nil || update.Message.Text == "" {
		return
	}
	if strings.HasPrefix(update.Message.Text, "/") {
		return
	}

	chatID := update.Message.Chat.ID
	if !a.ready[chatID] {
		a.sendStartCheck(ctx, b, chatID)
		return
	}

	a.handleListInput(ctx, b, update)
}

func listPath(cfg *config.Config, t lists.EntryType) string {
	if t == lists.TypeDomain {
		return cfg.DomainList
	}
	return cfg.IPList
}

func typeLabel(t lists.EntryType) string {
	if t == lists.TypeDomain {
		return "домены"
	}
	return "IP/CIDR"
}
