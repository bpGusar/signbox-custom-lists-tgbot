package bot

import (
	"context"
	"fmt"
	"log"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"lst-signbox-lists-tgbot/internal/config"
	"lst-signbox-lists-tgbot/internal/lists"
	"lst-signbox-lists-tgbot/internal/service"
)

type App struct {
	cfg     *config.Config
	svc     *service.Manager
	sess    *SessionStore
	ready   map[int64]bool
}

func Run(ctx context.Context, cfg *config.Config) error {
	if !cfg.Enabled {
		log.Println("lst-signbox-lists-tgbot disabled in config")
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
	b.RegisterHandler(tgbot.HandlerTypeMessageText, "/start", tgbot.MatchTypePrefix, app.handleStart)
	b.RegisterHandler(tgbot.HandlerTypeCallbackQueryData, "s:", tgbot.MatchTypePrefix, app.handleCallback)

	log.Println("lst-signbox-lists-tgbot started")
	b.Start(ctx)
	return nil
}

func (a *App) defaultHandler(ctx context.Context, b *tgbot.Bot, update *models.Update) {
	if update.Message == nil || update.Message.Text == "" {
		return
	}
	if isStartCommand(update.Message.Text) {
		a.handleStart(ctx, b, update)
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

func isStartCommand(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return false
	}
	cmd := parts[0]
	if cmd == "/start" {
		return true
	}
	return strings.HasPrefix(cmd, "/start@")
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
