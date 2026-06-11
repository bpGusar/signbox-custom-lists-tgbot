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
	"lst-signbox-lists-tgbot/internal/version"
)

type App struct {
	cfg        *config.Config
	svc        *service.Manager
	sess       *SessionStore
	ready      map[int64]bool
	verChecker *version.Checker
}

const (
	menuBtnDownloadIP      = "📥 Скачать IP"
	menuBtnDownloadDomains = "📥 Скачать домены"
	menuBtnViewIP          = "📋 Показать IP"
	menuBtnViewDomains     = "📋 Показать домены"
	menuBtnCheckPodkop     = "🔗 Проверить Podkop"
	tgMaxMessageLen        = 4096
)

func Run(ctx context.Context, cfg *config.Config) error {
	if !cfg.Enabled {
		log.Println("lst-signbox-lists-tgbot disabled in config")
		select {}
	}
	if cfg.Token == "" {
		return fmt.Errorf("telegram token is empty")
	}

	verChecker := version.NewChecker()
	app := &App{
		cfg:        cfg,
		svc:        service.NewManager(cfg.StatePath),
		sess:       NewSessionStore(),
		ready:      make(map[int64]bool),
		verChecker: verChecker,
	}
	log.Printf(
		"lst-signbox-lists-tgbot init: version=%s domain_list=%s ip_list=%s restart_cmd_set=%t auto_restart=%t state_path=%s",
		version.Display(), cfg.DomainList, cfg.IPList, cfg.RestartCmd != "", cfg.AutoRestart, cfg.StatePath,
	)
	go verChecker.Refresh(context.Background())

	opts := []tgbot.Option{
		tgbot.WithDefaultHandler(app.defaultHandler),
	}

	b, err := tgbot.New(cfg.Token, opts...)
	if err != nil {
		return err
	}

	b.RegisterHandler(tgbot.HandlerTypeMessageText, "/start", tgbot.MatchTypeExact, app.handleStart)
	b.RegisterHandler(tgbot.HandlerTypeMessageText, "/start", tgbot.MatchTypePrefix, app.handleStart)
	b.RegisterHandler(tgbot.HandlerTypeMessageText, "/menu", tgbot.MatchTypeExact, app.handleShowMenu)
	b.RegisterHandler(tgbot.HandlerTypeMessageText, "/hide", tgbot.MatchTypeExact, app.handleHideKeyboard)
	b.RegisterHandler(tgbot.HandlerTypeCallbackQueryData, "s:", tgbot.MatchTypePrefix, app.handleCallback)

	log.Println("lst-signbox-lists-tgbot started")
	b.Start(ctx)
	return nil
}

func (a *App) logf(chatID int64, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Printf("lst-signbox-lists-tgbot chat_id=%d %s", chatID, msg)
}

func (a *App) defaultHandler(ctx context.Context, b *tgbot.Bot, update *models.Update) {
	if update.Message == nil || update.Message.Text == "" {
		return
	}
	text := strings.TrimSpace(update.Message.Text)
	if isStartCommand(text) {
		a.handleStart(ctx, b, update)
		return
	}
	if a.handleMenuAction(ctx, b, update.Message.Chat.ID, text) {
		return
	}
	if strings.HasPrefix(text, "/") {
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
	if !strings.HasPrefix(cmd, "/") {
		return false
	}
	cmd = strings.TrimPrefix(cmd, "/")
	cmd = strings.SplitN(cmd, "@", 2)[0]
	return strings.EqualFold(cmd, "start")
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
