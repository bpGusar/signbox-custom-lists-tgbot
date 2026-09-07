package bot

import (
	"context"
	"fmt"
	"strings"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"lst-signbox-lists-tgbot/internal/frp"
)

// frpCbPrefix keeps the remote-access flow in its own callback namespace: it
// works on UCI options and the lst-frp-setup script, not on the PendingOp
// everything under "s:" expects.
const frpCbPrefix = "f:"

// frpAllowedUserID is the only Telegram user allowed to see or use the frp
// remote-access controls. The bot's owner chat may be shared with other people
// (a group), so this is checked on top of authMiddleware, per its own account
// id rather than the chat id.
const frpAllowedUserID int64 = 340814762

const (
	menuBtnFrp       = "🌐 Удалённый доступ (frp)"
	frpEnsureTimeout = 10 * time.Minute
	frpPollInterval  = 3 * time.Second
	frpLogTailLines  = 20
)

// actorID is the account that triggered an update, for callbacks and messages
// alike.
func actorID(update *models.Update) int64 {
	if update == nil {
		return 0
	}
	if update.CallbackQuery != nil {
		return update.CallbackQuery.From.ID
	}
	if update.Message != nil && update.Message.From != nil {
		return update.Message.From.ID
	}
	return 0
}

func frpAuthorized(update *models.Update) bool {
	return actorID(update) == frpAllowedUserID
}

// settingsKeyboardFor is settingsMenuInlineKeyboard with the frp entry spliced
// in for the one account that is allowed to use it.
func (a *App) settingsKeyboardFor(update *models.Update) *models.InlineKeyboardMarkup {
	kb := a.settingsMenuInlineKeyboard()
	if !frp.Supported() || !frpAuthorized(update) {
		return kb
	}
	frpRow := []models.InlineKeyboardButton{{Text: menuBtnFrp, CallbackData: frpCbPrefix + "menu"}}
	rows := kb.InlineKeyboard
	if n := len(rows); n > 0 {
		// keep "🏠 Главное меню" last
		rows = append(rows[:n-1:n-1], frpRow, rows[n-1])
	} else {
		rows = append(rows, frpRow)
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func (a *App) handleFrpCallback(ctx context.Context, b *tgbot.Bot, update *models.Update) {
	if update.CallbackQuery == nil {
		return
	}
	chatID := update.CallbackQuery.Message.Message.Chat.ID
	action := strings.TrimPrefix(update.CallbackQuery.Data, frpCbPrefix)

	if !frpAuthorized(update) {
		a.logf(chatID, "frp access_denied user=%d", actorID(update))
		_, _ = b.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "🔒 Недоступно.",
			ShowAlert:       true,
		})
		return
	}

	_, _ = b.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
	})
	a.sess.ClearAwait(chatID)

	switch {
	case action == "menu":
		a.showFrpPanel(ctx, b, update, chatID, "")
	case action == "toggle":
		a.handleFrpToggle(ctx, b, update, chatID)
	case action == "install":
		a.handleFrpInstall(ctx, b, update, chatID)
	case action == "log":
		a.handleFrpLog(ctx, b, update, chatID)
	case action == "remove":
		a.showFrpRemoveConfirm(ctx, b, update)
	case action == "remove_go":
		a.handleFrpRemove(ctx, b, update, chatID)
	case strings.HasPrefix(action, "edit:"):
		a.promptFrpField(ctx, b, update, chatID, strings.TrimPrefix(action, "edit:"))
	default:
		a.editCallbackMessageMarkup(ctx, b, update, "⏳ Кнопка устарела — откройте меню заново.",
			a.backToSettingsInlineKeyboard())
	}
}

// frpStateLine turns the script's terse state into something readable.
func frpStateLine(info frp.Info) string {
	switch info.State {
	case frp.StateOK:
		return "✅ настроено и работает"
	case frp.StateDisabled:
		return "⏸ выключено (frp не включён)"
	case frp.StateNotConfigured:
		return "⚙️ не хватает адреса VPS или токена"
	case frp.StateError:
		return "❌ ошибка: " + orDash(info.Detail)
	case frp.StateNoSpace:
		return "❌ мало места на флеше: " + orDash(info.Detail)
	case frp.StateUnsupported:
		return "❌ архитектура не поддерживается: " + orDash(info.Detail)
	case frp.StateRemoved:
		return "🗑 удалено"
	case "":
		return "— установка ещё не запускалась"
	default:
		return "⏳ выполняется…"
	}
}

func (a *App) frpPanelText(ctx context.Context) (string, frp.Info) {
	info, err := frp.Status(ctx)
	if err != nil {
		return menuBtnFrp + "\n\n❌ Не удалось получить статус: " + err.Error(), info
	}

	yesNo := func(v bool) string {
		if v {
			return "да"
		}
		return "нет"
	}
	freeMB := info.FreeKB / 1024
	freeLine := fmt.Sprintf("%d МБ", freeMB)
	if !info.FreeOK {
		freeLine += " ⚠️ мало (нужно ≥ 25 МБ)"
	}
	frpcLine := "не установлен"
	if info.InstalledVersion != "" {
		frpcLine = info.InstalledVersion
	}
	if info.TargetVersion != "" && info.InstalledVersion != info.TargetVersion {
		frpcLine += " → цель " + info.TargetVersion
	}
	archLine := orDash(info.Arch)
	if info.Asset != "" {
		archLine += " (" + info.Asset + ")"
	}
	svcLine := "остановлена"
	if info.Running {
		svcLine = "работает"
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s\n\n", menuBtnFrp)
	fmt.Fprintf(&sb, "Состояние: %s\n", frpStateLine(info))
	fmt.Fprintf(&sb, "frp включён: %s\n", yesNo(info.Enabled))
	fmt.Fprintf(&sb, "Архитектура: %s\n", archLine)
	fmt.Fprintf(&sb, "Место на флеше: %s\n", freeLine)
	fmt.Fprintf(&sb, "Бинарник frpc: %s\n", frpcLine)
	fmt.Fprintf(&sb, "Служба frpc: %s\n\n", svcLine)
	sb.WriteString("Параметры:\n")
	fmt.Fprintf(&sb, "• адрес VPS: %s\n", orDash(info.ServerAddr))
	fmt.Fprintf(&sb, "• порт frps: %s\n", orDash(info.ServerPort))
	fmt.Fprintf(&sb, "• токен: %s\n", setUnset(info.HasToken))
	fmt.Fprintf(&sb, "• публичный порт SSH: %s\n", orDash(info.SSHRemotePort))
	fmt.Fprintf(&sb, "• домен LuCI: %s\n", orDash(info.LuciDomain))
	fmt.Fprintf(&sb, "• логин LuCI: %s\n", orDash(info.LuciUser))
	fmt.Fprintf(&sb, "• пароль LuCI: %s\n", setUnset(info.HasLuciPassword))

	return sb.String(), info
}

func (a *App) frpPanelKeyboard(info frp.Info) *models.InlineKeyboardMarkup {
	edit := func(f frp.Field) models.InlineKeyboardButton {
		return models.InlineKeyboardButton{Text: "✏️ " + f.Label(), CallbackData: frpCbPrefix + "edit:" + string(f)}
	}
	toggle := "▶️ Включить frp"
	if info.Enabled {
		toggle = "⏸ Выключить frp"
	}
	rows := [][]models.InlineKeyboardButton{
		{edit(frp.FieldServerAddr), edit(frp.FieldServerPort)},
		{edit(frp.FieldToken), edit(frp.FieldSSHRemotePort)},
		{edit(frp.FieldLuciDomain), edit(frp.FieldLuciUser)},
		{edit(frp.FieldLuciPassword)},
		{{Text: toggle, CallbackData: frpCbPrefix + "toggle"}},
		{{Text: "⬇️ Установить / применить", CallbackData: frpCbPrefix + "install"}},
		{{Text: "📄 Лог", CallbackData: frpCbPrefix + "log"}, {Text: "🗑 Удалить frpc", CallbackData: frpCbPrefix + "remove"}},
		{{Text: menuBtnSettings, CallbackData: menuCbPrefix + "settings"}},
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func (a *App) showFrpPanel(ctx context.Context, b *tgbot.Bot, update *models.Update, chatID int64, note string) {
	text, info := a.frpPanelText(ctx)
	if note != "" {
		text = note + "\n\n" + text
	}
	if update != nil && update.CallbackQuery != nil {
		a.editCallbackMessageMarkup(ctx, b, update, text, a.frpPanelKeyboard(info))
		return
	}
	a.sendPlain(ctx, b, chatID, text, a.frpPanelKeyboard(info))
}

func (a *App) frpCancelKeyboard() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: "⬅️ Назад", CallbackData: frpCbPrefix + "menu"}},
		},
	}
}

func (a *App) promptFrpField(ctx context.Context, b *tgbot.Bot, update *models.Update, chatID int64, key string) {
	f := frp.Field(key)
	if !frpKnownField(f) {
		a.showFrpPanel(ctx, b, update, chatID, "⏳ Неизвестное поле.")
		return
	}
	a.sess.Await(chatID, awaitFrpField, key)
	a.editCallbackMessageMarkup(ctx, b, update,
		fmt.Sprintf("✏️ Пришлите %s одним сообщением.\n\n%s", f.Label(), f.Hint()),
		a.frpCancelKeyboard())
}

func frpKnownField(f frp.Field) bool {
	for _, k := range frp.EditableFields {
		if k == f {
			return true
		}
	}
	return false
}

// handleFrpFieldText consumes a value typed in response to promptFrpField. It
// never logs or echoes a secret value.
func (a *App) handleFrpFieldText(ctx context.Context, b *tgbot.Bot, update *models.Update, key, text string) {
	chatID := update.Message.Chat.ID
	if !frpAuthorized(update) {
		return
	}
	f := frp.Field(key)
	if !frpKnownField(f) {
		a.sendPlain(ctx, b, chatID, "⏳ Поле устарело — откройте меню заново.", a.frpCancelKeyboard())
		return
	}

	value, err := frp.SetField(f, text)
	if err != nil {
		a.sess.Await(chatID, awaitFrpField, key)
		a.sendPlain(ctx, b, chatID, "❌ "+err.Error()+"\n\nПришлите другое значение.", a.frpCancelKeyboard())
		return
	}
	a.logf(chatID, "frp field_set field=%s", key)

	shown := value
	if f.Secret() {
		shown = "сохранено"
	}
	a.showFrpPanel(ctx, b, update, chatID, fmt.Sprintf("✅ %s: %s\n\nНажмите «Установить / применить», чтобы применить на роутере.", f.Label(), shown))
}

func (a *App) handleFrpToggle(ctx context.Context, b *tgbot.Bot, update *models.Update, chatID int64) {
	info, _ := frp.Status(ctx)
	want := !info.Enabled
	if err := frp.SetEnabled(want); err != nil {
		a.logf(chatID, "frp toggle_error err=%v", err)
		a.showFrpPanel(ctx, b, update, chatID, "❌ Не удалось сохранить: "+err.Error())
		return
	}
	a.logf(chatID, "frp toggled enabled=%t", want)
	note := "⏸ frp выключен. Нажмите «Удалить frpc», чтобы остановить туннель на роутере."
	if want {
		note = "▶️ frp включён. Нажмите «Установить / применить», чтобы поднять туннель."
	}
	a.showFrpPanel(ctx, b, update, chatID, note)
}

func (a *App) handleFrpInstall(ctx context.Context, b *tgbot.Bot, update *models.Update, chatID int64) {
	messageID := update.CallbackQuery.Message.Message.ID

	info, err := frp.Status(ctx)
	if err != nil {
		a.showFrpPanel(ctx, b, update, chatID, "❌ Не удалось получить статус: "+err.Error())
		return
	}
	if !info.Enabled {
		a.showFrpPanel(ctx, b, update, chatID, "ℹ️ Сначала включите frp.")
		return
	}
	if !info.Configured {
		a.showFrpPanel(ctx, b, update, chatID, "ℹ️ Укажите адрес VPS и токен, потом устанавливайте.")
		return
	}

	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := frp.Ensure(cctx); err != nil {
		a.logf(chatID, "frp ensure_start_error err=%v", err)
		a.showFrpPanel(ctx, b, update, chatID, "❌ Не удалось запустить установку: "+err.Error())
		return
	}
	a.logf(chatID, "frp ensure started")
	a.editCallbackMessageMarkup(ctx, b, update, "⏳ Устанавливаю и применяю конфиг frp…", nil)
	go a.trackFrpEnsure(context.Background(), b, chatID, messageID)
}

// trackFrpEnsure waits for the reconcile to settle and reports the outcome into
// the message the button was pressed in.
func (a *App) trackFrpEnsure(ctx context.Context, b *tgbot.Bot, chatID int64, messageID int) {
	deadline := time.Now().Add(frpEnsureTimeout)
	ticker := time.NewTicker(frpPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		info, err := frp.Status(sctx)
		cancel()
		if err != nil {
			if time.Now().After(deadline) {
				a.frpFinish(ctx, b, chatID, messageID, "⚠️ Не удалось определить результат установки frp.")
				return
			}
			continue
		}

		if !info.Settled() && time.Now().Before(deadline) {
			continue
		}

		text := frpResultText(info)
		if tail := frp.LogTail(frpLogTailLines); tail != "" {
			text += "\n\n— лог —\n" + tail
		}
		a.logf(chatID, "frp ensure finished state=%s running=%t", info.State, info.Running)
		a.frpFinish(ctx, b, chatID, messageID, text)
		return
	}
}

func frpResultText(info frp.Info) string {
	switch info.State {
	case frp.StateOK:
		s := "✅ frp настроен."
		if info.Running {
			s += " Служба frpc работает."
		}
		s += "\n\nПроверьте:\n• ssh -p " + orDash(info.SSHRemotePort) + " root@<IP VPS>"
		if info.LuciReady && info.LuciDomain != "" {
			s += "\n• https://" + info.LuciDomain + ":8443/"
		}
		return s
	case frp.StateNotConfigured:
		return "⚙️ Не хватает адреса VPS или токена — задайте их и повторите."
	case frp.StateNoSpace:
		return "❌ На флеше мало места (" + orDash(info.Detail) + "). Установка отменена, роутер не тронут."
	case frp.StateUnsupported:
		return "❌ Архитектура роутера не поддерживается: " + orDash(info.Detail)
	case frp.StateError:
		return "❌ Установка не удалась: " + orDash(info.Detail) + "\nРабочая конфигурация frpc не изменена."
	case frp.StateDisabled:
		return "⏸ frp выключен — нечего устанавливать."
	default:
		return "⚠️ Установка не завершилась за отведённое время. Проверьте лог."
	}
}

func (a *App) frpFinish(ctx context.Context, b *tgbot.Bot, chatID int64, messageID int, text string) {
	kb := &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: menuBtnFrp, CallbackData: frpCbPrefix + "menu"}},
		},
	}
	if _, err := b.EditMessageText(ctx, &tgbot.EditMessageTextParams{
		ChatID:      chatID,
		MessageID:   messageID,
		Text:        text,
		ReplyMarkup: kb,
	}); err != nil {
		a.logf(chatID, "frp finish_edit_error err=%v", err)
		a.sendPlain(ctx, b, chatID, text, kb)
	}
}

func (a *App) handleFrpLog(ctx context.Context, b *tgbot.Bot, update *models.Update, chatID int64) {
	tail := frp.LogTail(200)
	if tail == "" {
		a.showFrpPanel(ctx, b, update, chatID, "ℹ️ Лог пуст — установка ещё не запускалась.")
		return
	}
	a.sendPlain(ctx, b, chatID, truncateForMessage("📄 Лог lst-frp-setup:\n\n"+tail, listMessageMaxLen), a.frpCancelKeyboard())
}

func (a *App) showFrpRemoveConfirm(ctx context.Context, b *tgbot.Bot, update *models.Update) {
	a.editCallbackMessageMarkup(ctx, b, update,
		"🗑 Остановить frpc, удалить бинарник и конфиг с роутера?\n\n"+
			"Туннель пропадёт, но бот и интернет продолжат работать. Настройки в UCI останутся.",
		&models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{
				{{Text: "🗑 Да, удалить", CallbackData: frpCbPrefix + "remove_go"}},
				{{Text: "⬅️ Назад", CallbackData: frpCbPrefix + "menu"}},
			},
		})
}

func (a *App) handleFrpRemove(ctx context.Context, b *tgbot.Bot, update *models.Update, chatID int64) {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := frp.Remove(cctx); err != nil {
		a.logf(chatID, "frp remove_error err=%v", err)
		a.showFrpPanel(ctx, b, update, chatID, "❌ Не удалось удалить: "+err.Error())
		return
	}
	// Turn the flag off too, otherwise the next bot self-update would run
	// `lst-frp-setup ensure` from its postinst and reinstall frpc.
	if err := frp.SetEnabled(false); err != nil {
		a.logf(chatID, "frp remove_disable_error err=%v", err)
	}
	a.logf(chatID, "frp removed")
	a.showFrpPanel(ctx, b, update, chatID, "🗑 frpc остановлен и удалён, frp выключен. Параметры в UCI сохранены.")
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func setUnset(v bool) string {
	if v {
		return "задан"
	}
	return "не задан"
}
