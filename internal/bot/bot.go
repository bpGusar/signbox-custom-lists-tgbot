package bot

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"lst-signbox-lists-tgbot/internal/config"
	"lst-signbox-lists-tgbot/internal/lists"
	"lst-signbox-lists-tgbot/internal/podkop"
	"lst-signbox-lists-tgbot/internal/service"
	"lst-signbox-lists-tgbot/internal/version"
)

type App struct {
	cfg        *config.Config
	svc        *service.Manager
	sess       *SessionStore
	readyMu    sync.Mutex
	ready      map[int64]bool
	verChecker *version.Checker
	// readSections reads podkop's config. It is a field so the screens can be
	// exercised without a router to run uci on; nil means the real thing.
	readSections func(context.Context) ([]podkop.Section, error)
}

func (a *App) isReady(chatID int64) bool {
	a.readyMu.Lock()
	defer a.readyMu.Unlock()
	return a.ready[chatID]
}

func (a *App) setReady(chatID int64, ready bool) {
	a.readyMu.Lock()
	defer a.readyMu.Unlock()
	a.ready[chatID] = ready
}

const (
	menuCbPrefix       = "m:"
	menuBtnMainMenu    = "🏠 Главное меню"
	menuBtnCheckPodkop = "🔗 Проверить Podkop"
	menuBtnSettings    = "⚙️ Настройки"
	tgMaxMessageLen    = 4096
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
		version.Display(), cfg.DomainList, cfg.IPList, cfg.RestartCmd != "", cfg.GetAutoRestart(), cfg.StatePath,
	)

	opts := []tgbot.Option{
		tgbot.WithDefaultHandler(app.defaultHandler),
		tgbot.WithMiddlewares(app.authMiddleware),
	}

	b, err := tgbot.New(cfg.Token, opts...)
	if err != nil {
		return err
	}

	b.RegisterHandler(tgbot.HandlerTypeMessageText, "/start", tgbot.MatchTypeExact, app.handleStart)
	b.RegisterHandler(tgbot.HandlerTypeMessageText, "/start", tgbot.MatchTypePrefix, app.handleStart)
	b.RegisterHandler(tgbot.HandlerTypeMessageText, "/menu", tgbot.MatchTypeExact, app.handleShowMenu)
	b.RegisterHandler(tgbot.HandlerTypeMessageText, "/hide", tgbot.MatchTypeExact, app.handleHideKeyboard)
	// A list too long for a keyboard is picked by tapping a command inside the
	// message, so each command family needs its own prefix handler.
	b.RegisterHandler(tgbot.HandlerTypeMessageText, "/cd_", tgbot.MatchTypePrefix, app.handleViewCategoryCommand)
	b.RegisterHandler(tgbot.HandlerTypeMessageText, "/ci_", tgbot.MatchTypePrefix, app.handleViewCategoryCommand)
	b.RegisterHandler(tgbot.HandlerTypeMessageText, "/gd_", tgbot.MatchTypePrefix, app.handleAddCategoryCommand)
	b.RegisterHandler(tgbot.HandlerTypeMessageText, "/gi_", tgbot.MatchTypePrefix, app.handleAddCategoryCommand)
	b.RegisterHandler(tgbot.HandlerTypeCallbackQueryData, "s:", tgbot.MatchTypePrefix, app.handleCallback)
	b.RegisterHandler(tgbot.HandlerTypeCallbackQueryData, menuCbPrefix, tgbot.MatchTypePrefix, app.handleMenuCallback)

	log.Println("lst-signbox-lists-tgbot started")
	go app.watchVersion(ctx, b)
	go app.resumePendingUpgrade(ctx, b)
	b.Start(ctx)
	return nil
}

const (
	versionCheckInterval = 6 * time.Hour
	// The router's uplink (and podkop's tunnel) may still be coming up right
	// after boot, so the first check waits instead of burning its attempt.
	versionCheckStartDelay = 2 * time.Minute
)

// watchVersion polls GitHub for new releases and pushes a one-off message to
// the owner chat for each version it has not announced yet.
func (a *App) watchVersion(ctx context.Context, b *tgbot.Bot) {
	if version.IsDev() {
		return
	}

	timer := time.NewTimer(versionCheckStartDelay)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		a.notifyNewVersion(ctx, b)
		timer.Reset(versionCheckInterval)
	}
}

func (a *App) notifyNewVersion(ctx context.Context, b *tgbot.Bot) {
	chatID, ok := a.svc.Owner()
	if !ok {
		return
	}

	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if a.verChecker.CheckFresh(cctx) != version.StatusOutdated {
		return
	}
	latest := a.verChecker.Latest()
	if a.svc.NotifiedVersion() == latest {
		return
	}

	params := &tgbot.SendMessageParams{
		ChatID: chatID,
		Text:   fmt.Sprintf("🆕 Доступна новая версия: %s\nТекущая: %s", latest, version.Display()),
	}
	if service.UpgradeSupported() {
		// Same callback as the settings entry, so pressing it runs the very
		// same prompt-and-install flow, right in the notification message.
		params.ReplyMarkup = &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{
				{{Text: menuBtnUpgrade, CallbackData: menuCbPrefix + "upgrade"}},
			},
		}
	} else {
		params.Text += "\n\nОбновление на роутере:\nlst-signbox-lists-tgbot-upgrade start"
	}

	if _, err := b.SendMessage(ctx, params); err != nil {
		a.logf(chatID, "version_notify send_error version=%s err=%v", latest, err)
		return
	}
	if err := a.svc.MarkVersionNotified(latest); err != nil {
		a.logf(chatID, "version_notify mark_error version=%s err=%v", latest, err)
	}
	a.logf(chatID, "version_notify sent version=%s", latest)
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
	chatID := update.Message.Chat.ID
	if isStartCommand(text) {
		a.sess.ClearAwait(chatID)
		a.handleStart(ctx, b, update)
		return
	}
	if a.handleMenuAction(ctx, b, chatID, text) {
		a.sess.ClearAwait(chatID)
		return
	}
	// A message the bot explicitly asked for — a category name, a file path —
	// is not list input, so it is consumed before the parser sees it. It also
	// comes before the command guard below, because a path starts with the
	// same slash a command does.
	if kind, opID := a.sess.TakeAwait(chatID); kind != awaitNone {
		a.handleAwaitedText(ctx, b, update, kind, opID, text)
		return
	}
	if strings.HasPrefix(text, "/") {
		return
	}

	if !a.isReady(chatID) {
		a.sendStartCheck(ctx, b, chatID)
		return
	}

	a.handleListInput(ctx, b, update)
}

// updateChatID extracts the chat ID an update belongs to, if any.
func updateChatID(update *models.Update) (int64, bool) {
	if update.Message != nil {
		return update.Message.Chat.ID, true
	}
	if update.CallbackQuery != nil && update.CallbackQuery.Message.Message != nil {
		return update.CallbackQuery.Message.Message.Chat.ID, true
	}
	return 0, false
}

// authMiddleware restricts the bot to a single owner chat, since the bot has
// no per-user permission model: whoever can message it can rewrite the
// domain/IP lists and trigger service restarts. The first chat to interact
// with the bot (fresh install, or after an upgrade from a version that
// predates this check) is claimed as the owner and persisted in state.json;
// every other chat is rejected until owner_chat_id is cleared there.
func (a *App) authMiddleware(next tgbot.HandlerFunc) tgbot.HandlerFunc {
	return func(ctx context.Context, b *tgbot.Bot, update *models.Update) {
		chatID, ok := updateChatID(update)
		if !ok {
			next(ctx, b, update)
			return
		}

		owner, isOwner, err := a.svc.ClaimOrCheckOwner(chatID)
		if err != nil {
			a.logf(chatID, "owner_check_error err=%v", err)
			// Fail closed: the owner check is a security boundary, so an
			// unreadable/unwritable state file must not open the bot up.
			return
		}
		if !isOwner {
			a.logf(chatID, "access_denied owner_chat_id=%d", owner)
			if update.CallbackQuery != nil {
				_, _ = b.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
					CallbackQueryID: update.CallbackQuery.ID,
					Text:            "🔒 Бот уже привязан к другому пользователю.",
					ShowAlert:       true,
				})
				return
			}
			_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
				ChatID: chatID,
				Text:   "🔒 Этот бот уже привязан к другому пользователю.",
			})
			return
		}

		next(ctx, b, update)
	}
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

func typeLabel(t lists.EntryType) string {
	if t == lists.TypeDomain {
		return "домены"
	}
	return "IP/CIDR"
}
