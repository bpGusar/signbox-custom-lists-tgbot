package bot

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"lst-signbox-lists-tgbot/internal/lists"
	"lst-signbox-lists-tgbot/internal/podkop"
	"lst-signbox-lists-tgbot/internal/service"
	"lst-signbox-lists-tgbot/internal/version"
)

const cbPrefix = "s:"

func (a *App) handleStart(ctx context.Context, b *tgbot.Bot, update *models.Update) {
	chatID := update.Message.Chat.ID
	a.logf(chatID, "command=/start")
	a.sendStartCheck(ctx, b, chatID)
}

func (a *App) sendStartCheck(ctx context.Context, b *tgbot.Bot, chatID int64) {
	a.sess.DisarmProxyFile(chatID)
	missing := a.missingFiles(ctx)
	if len(missing) > 0 {
		a.logf(chatID, "start_check missing_files=%s", strings.Join(missing, ","))
		text := "📂 Отсутствуют файлы:\n" + strings.Join(missing, "\n")
		kb := &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{
				{{Text: "📝 Создать файлы", CallbackData: cbPrefix + a.sess.Create(PendingOp{ChatID: chatID, Kind: ActionStartCreate})}},
				{{Text: "🔄 Проверить снова", CallbackData: cbPrefix + a.sess.Create(PendingOp{ChatID: chatID, Kind: ActionStartRetry})}},
			},
		}
		_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
			ChatID:      chatID,
			Text:        text,
			ReplyMarkup: kb,
		})
		return
	}

	wasReady := a.isReady(chatID)
	a.setReady(chatID, true)
	a.logf(chatID, "start_check ready=true")
	text := a.welcomeText(ctx)
	_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID:      chatID,
		Text:        text,
		ReplyMarkup: a.mainMenuInlineKeyboard(),
	})
	if !wasReady {
		a.ensureReplyKeyboard(ctx, b, chatID)
	}
}

func (a *App) welcomeText(ctx context.Context) string {
	text := a.versionHeader(ctx) + "\n\n" +
		"✅ Бот готов к работе.\n\n" +
		"Отправьте список доменов или IP/CIDR через запятую или с новой строки.\n\n" +
		"Примеры:\n" +
		"• example.com, github.com\n" +
		"• kick.com\n" +
		"• 192.168.1.0/24\n\n" +
		"Можно вставить строки из файла — префикс // будет проигнорирован.\n" +
		"Бот определит тип, проверит файл и предложит действия.\n" +
		"Записи можно разложить по категориям: «Добавить в категорию» при добавлении,\n" +
		"«Управление записями» → секция → «Показать» для просмотра и управления.\n\n" +
		a.sectionsSummary(ctx) +
		"\n\n⌨️ /menu — показать кнопки, /hide — скрыть"

	if banner := a.svc.StaleBanner(); banner != "" {
		text = banner + "\n\n" + text
		if reason := a.restartHiddenReason(); reason != "" {
			text += "\n\n" + reason
		}
	}
	return text
}

func (a *App) versionHeader(ctx context.Context) string {
	lines := []string{fmt.Sprintf("📦 Версия: %s", version.Display())}

	if version.IsDev() {
		lines = append(lines, "ℹ️ Сборка для разработки")
		return strings.Join(lines, "\n")
	}

	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	switch a.verChecker.CheckFresh(rctx) {
	case version.StatusCurrent:
		lines = append(lines, "✅ Версия актуальна")
	case version.StatusOutdated:
		lines = append(lines, fmt.Sprintf("⚠️ Доступна новая версия: %s", a.verChecker.Latest()))
	default:
		if a.verChecker.LastError() != nil {
			lines = append(lines, "ℹ️ Не удалось проверить обновления")
		}
	}
	return strings.Join(lines, "\n")
}

func (a *App) menuBtnRestart() string {
	return "🔄 Перезапустить " + a.cfg.ServiceLabel
}

func (a *App) menuBtnAutoRestart() string {
	if a.cfg.GetAutoRestart() {
		return "✅ Автоперезапуск: вкл"
	}
	return "⏸ Автоперезапуск: выкл"
}

func (a *App) settingsMenuText() string {
	text := menuBtnSettings
	if a.cfg.RestartCmd != "" {
		status := "выкл"
		if a.cfg.GetAutoRestart() {
			status = "вкл"
		}
		text += fmt.Sprintf("\n\nАвтоперезапуск %s после изменения списков: %s", a.cfg.ServiceLabel, status)
	}
	return text
}

func (a *App) handleShowMenu(ctx context.Context, b *tgbot.Bot, update *models.Update) {
	chatID := update.Message.Chat.ID
	a.logf(chatID, "command=/menu")
	_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID:      chatID,
		Text:        "⌨️ Меню",
		ReplyMarkup: a.mainMenuInlineKeyboard(),
	})
	a.ensureReplyKeyboard(ctx, b, chatID)
}

func (a *App) handleHideKeyboard(ctx context.Context, b *tgbot.Bot, update *models.Update) {
	chatID := update.Message.Chat.ID
	a.logf(chatID, "command=/hide")
	_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID: chatID,
		Text:   "⌨️ Клавиатура скрыта. /menu — показать снова.",
		ReplyMarkup: &models.ReplyKeyboardRemove{
			RemoveKeyboard: true,
		},
	})
}

func (a *App) restartHiddenReason() string {
	st, err := a.svc.Load()
	if err != nil || st == nil || !st.ServiceStale {
		return ""
	}
	if a.cfg.RestartCmd == "" {
		return "Кнопка перезапуска недоступна: не настроен restart_cmd.\n" +
			"Настройка через UCI:\n" +
			"uci set lst-signbox-lists-tgbot.main.restart_cmd='/etc/init.d/sing-box restart'\n" +
			"uci commit lst-signbox-lists-tgbot\n" +
			"/etc/init.d/lst-signbox-lists-tgbot restart\n" +
			"Или через ENV: LST_SIGNBOX_LISTS_TGBOT_RESTART_CMD='...'."
	}
	return ""
}

func (a *App) replyKeyboard() *models.ReplyKeyboardMarkup {
	return &models.ReplyKeyboardMarkup{
		Keyboard: [][]models.KeyboardButton{
			{{Text: menuBtnMainMenu}},
		},
		ResizeKeyboard:        true,
		InputFieldPlaceholder: "домены или IP/CIDR",
	}
}

func (a *App) ensureReplyKeyboard(ctx context.Context, b *tgbot.Bot, chatID int64) {
	_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID:              chatID,
		Text:                "\u200b",
		ReplyMarkup:         a.replyKeyboard(),
		DisableNotification: true,
	})
}

func (a *App) mainMenuInlineKeyboard() *models.InlineKeyboardMarkup {
	rows := [][]models.InlineKeyboardButton{
		{{Text: btnManage, CallbackData: menuCbPrefix + "manage"}},
		{{Text: btnProxyLinks, CallbackData: menuCbPrefix + menuActionProxyLinks}},
	}
	if a.hasSettingsMenu() {
		rows = append(rows, []models.InlineKeyboardButton{{
			Text: menuBtnSettings, CallbackData: menuCbPrefix + "settings",
		}})
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func (a *App) hasSettingsMenu() bool {
	return a.cfg.RestartCmd != "" || service.UpgradeSupported()
}

func (a *App) settingsMenuInlineKeyboard() *models.InlineKeyboardMarkup {
	var rows [][]models.InlineKeyboardButton
	if a.cfg.RestartCmd != "" {
		rows = append(rows, []models.InlineKeyboardButton{{
			Text: a.menuBtnRestart(), CallbackData: menuCbPrefix + "restart",
		}})
		rows = append(rows, []models.InlineKeyboardButton{{
			Text: a.menuBtnAutoRestart(), CallbackData: menuCbPrefix + "toggle_auto_restart",
		}})
	}
	if isPodkopCommand(a.cfg.RestartCmd) {
		rows = append(rows, []models.InlineKeyboardButton{{
			Text: menuBtnCheckPodkop, CallbackData: menuCbPrefix + "check_podkop",
		}})
	}
	if service.UpgradeSupported() {
		rows = append(rows, []models.InlineKeyboardButton{{
			Text: menuBtnUpgrade, CallbackData: menuCbPrefix + "upgrade",
		}})
	}
	rows = append(rows, []models.InlineKeyboardButton{{
		Text: menuBtnMainMenu, CallbackData: menuCbPrefix + "main_menu",
	}})
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func (a *App) backToSettingsInlineKeyboard() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: menuBtnSettings, CallbackData: menuCbPrefix + "settings"}},
		},
	}
}

func (a *App) backToMainMenuInlineKeyboard() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: menuBtnMainMenu, CallbackData: menuCbPrefix + "main_menu"}},
		},
	}
}

func (a *App) handleMenuAction(ctx context.Context, b *tgbot.Bot, chatID int64, text string) bool {
	if strings.EqualFold(strings.TrimSpace(text), menuBtnMainMenu) {
		a.logf(chatID, "menu main")
		a.sendStartCheck(ctx, b, chatID)
		return true
	}
	return false
}

func (a *App) handleMenuCallback(ctx context.Context, b *tgbot.Bot, update *models.Update) {
	if update.CallbackQuery == nil {
		return
	}

	chatID := update.CallbackQuery.Message.Message.Chat.ID
	action := strings.TrimPrefix(update.CallbackQuery.Data, menuCbPrefix)

	// Leaving the upload screen for anything else takes the file expectation
	// with it: a document must always come right after the instructions.
	if action != menuActionProxyLinks {
		a.sess.DisarmProxyFile(chatID)
	}

	_, _ = b.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
	})

	// A button that points at a list carries the whole target — list type,
	// section and file — so it keeps working no matter what was opened since.
	if act, ok := parseTargetAction(action); ok {
		a.handleTargetAction(ctx, b, update, act)
		return
	}
	if token, ok := strings.CutPrefix(action, "sec_"); ok {
		a.logf(chatID, "menu section token=%s", token)
		a.showSectionCard(ctx, b, update, token)
		return
	}

	switch action {
	case "manage":
		a.logf(chatID, "menu manage")
		a.showSections(ctx, b, update)
	case menuActionProxyLinks:
		a.logf(chatID, "menu proxy_links")
		a.showProxyUploadHint(ctx, b, update, chatID)
	case "check_podkop":
		a.logf(chatID, "menu check_podkop")
		text, ok := a.podkopIntegrationText(ctx)
		if !ok {
			text = "ℹ️ Проверка интеграции доступна только для podkop."
		}
		a.editCallbackMessageMarkup(ctx, b, update, text, a.backToSettingsInlineKeyboard())
	case "settings":
		a.logf(chatID, "menu settings")
		a.editCallbackMessageMarkup(ctx, b, update, a.settingsMenuText(), a.settingsMenuInlineKeyboard())
	case "toggle_auto_restart":
		if a.cfg.RestartCmd == "" {
			break
		}
		newVal := !a.cfg.GetAutoRestart()
		if err := a.cfg.SetAutoRestart(newVal); err != nil {
			a.logf(chatID, "toggle_auto_restart error err=%v", err)
			a.editCallbackMessageMarkup(ctx, b, update,
				a.settingsMenuText()+"\n\n❌ Не удалось сохранить: "+err.Error(),
				a.settingsMenuInlineKeyboard())
			break
		}
		a.logf(chatID, "toggle_auto_restart enabled=%t", newVal)
		a.editCallbackMessageMarkup(ctx, b, update, a.settingsMenuText(), a.settingsMenuInlineKeyboard())
	case "main_menu":
		a.logf(chatID, "menu main_inline")
		a.editCallbackMessageMarkup(ctx, b, update, a.welcomeText(ctx), a.mainMenuInlineKeyboard())
	case "restart":
		if a.cfg.RestartCmd != "" {
			a.logf(chatID, "menu restart")
			a.runRestartNotify(ctx, b, chatID)
		}
	case "upgrade":
		if service.UpgradeSupported() {
			a.logf(chatID, "menu upgrade")
			a.handleUpgradePrompt(ctx, b, update, chatID)
		}
	case "upgrade_go":
		if service.UpgradeSupported() {
			a.logf(chatID, "menu upgrade_go")
			a.handleUpgradeStart(ctx, b, update, chatID)
		}
	default:
		// Buttons drawn before an update point at actions that are gone.
		a.logf(chatID, "menu unknown action=%q", action)
		a.editCallbackMessageMarkup(ctx, b, update,
			"⏳ Эта кнопка устарела — откройте меню заново.", a.backToMainMenuInlineKeyboard())
	}
}

// missingFiles is every list file that is bound somewhere but not on disk.
func (a *App) missingFiles(ctx context.Context) []string {
	var missing []string
	for _, p := range a.allPaths(ctx) {
		if !lists.FileExists(p) {
			missing = append(missing, p)
		}
	}
	return missing
}

// sectionsSummary tells the welcome screen what the bot can reach, without
// making the user open a menu to find out.
func (a *App) sectionsSummary(ctx context.Context) string {
	secs := a.sections(ctx)
	if len(secs) == 1 && secs[0].Name == "" {
		return fmt.Sprintf("📄 Домены: %s\n📄 IP: %s", a.cfg.DomainList, a.cfg.IPList)
	}
	names := make([]string, 0, len(secs))
	for _, s := range secs {
		names = append(names, s.Name)
	}
	return "🗂 Секции podkop: " + strings.Join(names, ", ")
}

func (a *App) sendListFile(ctx context.Context, b *tgbot.Bot, chatID int64, tgt listTarget) {
	path, label := tgt.Path, typeLabel(tgt.Type)
	f, err := os.Open(path)
	if err != nil {
		a.logf(chatID, "download_file_error label=%q path=%q err=%v", label, path, err)
		_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
			ChatID: chatID,
			Text:   fmt.Sprintf("Не удалось открыть %s (%s): %v", label, path, err),
		})
		return
	}
	defer func() { _ = f.Close() }()

	filename := fileLabel(path)

	_, err = b.SendDocument(ctx, &tgbot.SendDocumentParams{
		ChatID: chatID,
		Document: &models.InputFileUpload{
			Filename: filename,
			Data:     f,
		},
		Caption: fmt.Sprintf("%s — %s\n%s", sectionDisplayName(tgt.Section), label, path),
	})
	if err != nil {
		a.logf(chatID, "send_document_error label=%q path=%q err=%v", label, path, err)
		_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
			ChatID: chatID,
			Text:   fmt.Sprintf("Не удалось отправить %s: %v", label, err),
		})
		return
	}
	a.logf(chatID, "download_file_sent label=%q path=%q", label, path)
}

func truncateForMessage(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	const suffix = "\n\n… (список обрезан — используйте «Скачать»)"
	if len(suffix) >= maxLen {
		return truncateValidUTF8(text, maxLen)
	}
	limit := maxLen - len(suffix)
	chunk := truncateValidUTF8(text, limit)
	if i := strings.LastIndex(chunk, "\n"); i > limit/2 {
		chunk = chunk[:i]
	}
	return chunk + suffix
}

// truncateValidUTF8 truncates s to at most maxLen bytes, backing off further
// if needed so the result never ends mid-rune (Telegram rejects invalid UTF-8).
func truncateValidUTF8(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	cut := s[:maxLen]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}

func (a *App) editCallbackMessageMarkup(ctx context.Context, b *tgbot.Bot, update *models.Update, text string, kb *models.InlineKeyboardMarkup) {
	a.editCallbackMessage(ctx, b, update, text, kb, "")
}

// editCallbackMessage rewrites the message a button was tapped in. The stale
// banner it prepends is plain text with no HTML specials, so it is safe to add
// under any parse mode.
func (a *App) editCallbackMessage(ctx context.Context, b *tgbot.Bot, update *models.Update, text string, kb *models.InlineKeyboardMarkup, parseMode models.ParseMode) {
	if banner := a.svc.StaleBanner(); banner != "" && !strings.Contains(text, "⚠️") {
		text = banner + "\n\n" + text
		if reason := a.restartHiddenReason(); reason != "" {
			text += "\n\n" + reason
		}
	}

	params := &tgbot.EditMessageTextParams{
		ChatID:    update.CallbackQuery.Message.Message.Chat.ID,
		MessageID: update.CallbackQuery.Message.Message.ID,
		Text:      text,
		ParseMode: parseMode,
	}
	if kb != nil {
		params.ReplyMarkup = kb
	}
	_, _ = b.EditMessageText(ctx, params)
}

func (a *App) handleListInput(ctx context.Context, b *tgbot.Bot, update *models.Update) {
	chatID := update.Message.Chat.ID
	text := update.Message.Text

	parsed := lists.ParseInput(text)
	if parsed.Mixed {
		a.logf(chatID, "list_input mixed_types")
		_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
			ChatID: chatID,
			Text:   buildMixedInputMessage(text),
		})
		return
	}
	if parsed.Empty {
		a.logf(chatID, "list_input empty")
		return
	}
	if len(parsed.Invalid) > 0 {
		a.logf(chatID, "list_input invalid_count=%d", len(parsed.Invalid))
		msg := "❌ Невалидные записи:\n" + lists.FormatList(parsed.Invalid)
		_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
			ChatID:      chatID,
			Text:        msg,
			ReplyMarkup: a.backToMainMenuInlineKeyboard(),
		})
		return
	}

	a.logf(chatID, "list_input accepted type=%s count=%d", typeLabel(parsed.Type), len(parsed.Valid))

	// Which file the entries belong in is a question of its own now: the same
	// domains can go to any podkop section.
	opID := a.sess.Create(PendingOp{ChatID: chatID, Kind: ActionAdd, ListType: parsed.Type, Values: parsed.Valid})
	op, ok := a.sess.Get(opID)
	if !ok {
		return
	}
	a.askAddTarget(ctx, b, a.replyTo(nil, chatID), op)
}

func (a *App) handleCallback(ctx context.Context, b *tgbot.Bot, update *models.Update) {
	if update.CallbackQuery == nil {
		return
	}

	chatID := update.CallbackQuery.Message.Message.Chat.ID
	data := strings.TrimPrefix(update.CallbackQuery.Data, cbPrefix)

	parts := strings.SplitN(data, ":", 2)
	opID := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	op, ok := a.sess.Get(opID)
	if !ok {
		a.logf(chatID, "callback stale op_id=%s action=%s", opID, action)
		_, _ = b.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "⏳ Операция устарела. Отправьте список снова.",
		})
		return
	}
	a.logf(chatID, "callback op_kind=%d op_id=%s action=%s", op.Kind, opID, action)

	if action == "cancel" {
		a.sess.Delete(opID)
		a.sess.ClearAwait(chatID)
		a.answerAndEditMarkup(ctx, b, update, "❌ Операция отменена.", a.backToMainMenuInlineKeyboard())
		return
	}

	// Picking a category works the same for every operation that asks for one,
	// so it is routed before the per-kind switch.
	if token, ok := strings.CutPrefix(action, cbPickPrefix); ok {
		a.handleCategoryPick(ctx, b, update, op, token)
		return
	}
	if action == cbNewCategory {
		a.promptNewCategory(ctx, b, update, op)
		return
	}

	// Choosing where the pending entries land: the section first, then the
	// file when the section is fed from more than one.
	if token, ok := strings.CutPrefix(action, cbSectionPrefix); ok {
		a.handleAddSectionPick(ctx, b, update, op, token)
		return
	}
	if token, ok := strings.CutPrefix(action, cbFilePrefix); ok {
		a.handleAddFilePick(ctx, b, update, op, token)
		return
	}

	switch op.Kind {
	case ActionAdd:
		switch action {
		case "apply":
			a.handleApplyMixed(ctx, b, update, op)
		case "add":
			a.execAddNew(ctx, b, update, op)
		case "addall":
			a.execAddAll(ctx, b, update, op)
		case "en":
			a.execEnable(ctx, b, update, op)
		case "del":
			vals := op.Values
			if len(vals) == 0 {
				vals = op.DisableValues
			}
			a.handleDelete(ctx, b, update, opForValues(op, vals))
		case "dis":
			a.handleDisablePrompt(ctx, b, update, opForDisable(op))
		case "cat":
			a.handleAddCategoryPrompt(ctx, b, update, op)
		}
	case ActionMove:
		if action == "confirm" {
			a.execMove(ctx, b, update, op)
			return
		}
		a.answerCallback(ctx, b, update)
	case ActionCategory:
		a.handleCategoryAction(ctx, b, update, op, action)
	case ActionBind:
		switch action {
		case "confirm":
			a.execBind(ctx, b, update, op)
		case "custom":
			a.promptBindPath(ctx, b, update, op)
		}
	case ActionAddAll:
		if action == "confirm" {
			a.execAddAll(ctx, b, update, op)
		}
	case ActionAddNew:
		if action == "confirm" {
			a.execAddNew(ctx, b, update, op)
		}
	case ActionDisableConfirm:
		if action == "confirm" {
			a.execDisable(ctx, b, update, op)
		}
	case ActionDisableAddMissing:
		if action == "confirm" {
			a.execDisableWithMissing(ctx, b, update, op)
		}
	case ActionStartCreate:
		a.sess.Delete(opID)
		a.handleStartCreate(ctx, b, update, chatID)
	case ActionStartRetry:
		a.sess.Delete(opID)
		a.sendStartCheck(ctx, b, chatID)
	default:
		_, _ = b.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
		})
	}
}

func opForValues(op *PendingOp, values []string) *PendingOp {
	if len(values) == 0 {
		return op
	}
	copy := *op
	copy.Values = append([]string(nil), values...)
	copy.DisableValues = nil
	return &copy
}

func opForDisable(op *PendingOp) *PendingOp {
	if len(op.DisableValues) == 0 {
		return op
	}
	return opForValues(op, op.DisableValues)
}

// opTargetLine names the file an operation is about, for screens that are no
// longer preceded by one saying so.
func opTargetLine(op *PendingOp) string {
	if op.ListPath == "" {
		return ""
	}
	return targetLine(listTarget{Section: op.Section, Type: op.ListType, Path: op.ListPath}) + "\n\n"
}

func (a *App) handleApplyMixed(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	var parts []string
	if len(op.Values) > 0 {
		if err := lists.AddNew(op.ListPath, op.Values, op.ListType, op.Category); err != nil {
			a.logf(op.ChatID, "apply_mixed add_error path=%q err=%v", op.ListPath, err)
			a.answerAndEdit(ctx, b, update, "❌ Ошибка добавления: "+err.Error())
			return
		}
		parts = append(parts, fmt.Sprintf("➕ Добавлено (%d):\n%s", len(op.Values), lists.FormatList(op.Values)))
	}
	if len(op.DisableValues) > 0 {
		if err := lists.Disable(op.ListPath, op.DisableValues, op.ListType, op.Category); err != nil {
			a.logf(op.ChatID, "apply_mixed disable_error path=%q err=%v", op.ListPath, err)
			a.answerAndEdit(ctx, b, update, "❌ Ошибка отключения: "+err.Error())
			return
		}
		parts = append(parts, fmt.Sprintf("⏸ Отключено (%d):\n%s", len(op.DisableValues), lists.FormatDisabledList(op.DisableValues)))
	}
	a.logf(op.ChatID, "apply_mixed success add=%d disable=%d path=%q", len(op.Values), len(op.DisableValues), op.ListPath)
	a.sess.Delete(op.ID)
	a.afterFilesChanged(ctx, b, update, op.ChatID, opTargetLine(op)+strings.Join(parts, "\n\n"))
}

func (a *App) execAddAll(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	if err := lists.AddAll(op.ListPath, op.Values, op.ListType, op.Category); err != nil {
		a.logf(op.ChatID, "add_all write_error path=%q err=%v", op.ListPath, err)
		a.answerAndEdit(ctx, b, update, "Ошибка записи: "+err.Error())
		return
	}
	a.logf(op.ChatID, "add_all success count=%d path=%q", len(op.Values), op.ListPath)
	a.sess.Delete(op.ID)
	a.afterAddSuccess(ctx, b, update, op.ChatID, opTargetLine(op)+"➕ Записи добавлены/включены.")
}

func (a *App) execAddNew(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	classified, err := lists.ClassifyValues(op.ListPath, op.Values)
	if err != nil {
		a.logf(op.ChatID, "add_new classify_error path=%q err=%v", op.ListPath, err)
		a.answerAndEdit(ctx, b, update, "Ошибка чтения файла: "+err.Error())
		return
	}
	newVals, _, _ := lists.GroupByStatus(classified)
	if len(newVals) == 0 {
		a.logf(op.ChatID, "add_new skipped_no_new count=%d", len(op.Values))
		a.sess.Delete(op.ID)
		a.answerAndEdit(ctx, b, update, "ℹ️ Нет новых записей для добавления.")
		return
	}

	if err := lists.AddNew(op.ListPath, op.Values, op.ListType, op.Category); err != nil {
		a.logf(op.ChatID, "add_new write_error path=%q err=%v", op.ListPath, err)
		a.answerAndEdit(ctx, b, update, "Ошибка записи: "+err.Error())
		return
	}
	a.logf(op.ChatID, "add_new success new_count=%d path=%q", len(newVals), op.ListPath)
	a.sess.Delete(op.ID)
	a.afterAddSuccess(ctx, b, update, op.ChatID,
		opTargetLine(op)+fmt.Sprintf("➕ Добавлено (%d):\n%s", len(newVals), lists.FormatList(newVals)))
}

func (a *App) execEnable(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	classified, err := lists.ClassifyValues(op.ListPath, op.Values)
	if err != nil {
		a.logf(op.ChatID, "enable classify_error path=%q err=%v", op.ListPath, err)
		a.answerAndEdit(ctx, b, update, "Ошибка чтения файла: "+err.Error())
		return
	}
	_, _, disabled := lists.GroupByStatus(classified)
	if len(disabled) == 0 {
		a.logf(op.ChatID, "enable skipped_no_disabled count=%d", len(op.Values))
		a.sess.Delete(op.ID)
		a.answerAndEdit(ctx, b, update, "ℹ️ Нет отключённых записей для включения.")
		return
	}

	if err := lists.AddAll(op.ListPath, disabled, op.ListType, op.Category); err != nil {
		a.logf(op.ChatID, "enable write_error path=%q err=%v", op.ListPath, err)
		a.answerAndEdit(ctx, b, update, "Ошибка записи: "+err.Error())
		return
	}
	a.logf(op.ChatID, "enable success count=%d path=%q", len(disabled), op.ListPath)
	a.sess.Delete(op.ID)
	a.afterAddSuccess(ctx, b, update, op.ChatID,
		opTargetLine(op)+fmt.Sprintf("✅ Включено (%d):\n%s", len(disabled), lists.FormatList(disabled)))
}

func (a *App) handleDelete(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	if err := lists.Delete(op.ListPath, op.Values, op.ListType); err != nil {
		a.logf(op.ChatID, "delete write_error path=%q err=%v", op.ListPath, err)
		a.answerAndEdit(ctx, b, update, "Ошибка удаления: "+err.Error())
		return
	}
	a.logf(op.ChatID, "delete success count=%d path=%q", len(op.Values), op.ListPath)
	a.sess.Delete(op.ID)
	a.afterFilesChanged(ctx, b, update, op.ChatID,
		opTargetLine(op)+fmt.Sprintf("🗑 Удалено:\n%s", lists.FormatList(op.Values)))
}

func (a *App) handleDisablePrompt(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	classified, err := lists.ClassifyValues(op.ListPath, op.Values)
	if err != nil {
		a.logf(op.ChatID, "disable classify_error path=%q err=%v", op.ListPath, err)
		a.answerAndEdit(ctx, b, update, "Ошибка чтения файла: "+err.Error())
		return
	}

	newVals, active, disabled := lists.GroupByStatus(classified)

	msg := opTargetLine(op) + fmt.Sprintf(
		"Отключение (%s):\n\nУже отключены:\n%s\n\nБудут отключены:\n%s\n\nОтсутствуют в файле:\n%s",
		typeLabel(op.ListType),
		lists.FormatList(disabled),
		lists.FormatList(active),
		lists.FormatList(newVals),
	)

	a.sess.Delete(op.ID)

	if len(newVals) > 0 {
		a.logf(op.ChatID, "disable requires_add_missing missing=%d active=%d disabled=%d", len(newVals), len(active), len(disabled))
		confirmID := a.sess.Create(PendingOp{ChatID: op.ChatID, Kind: ActionDisableAddMissing, ListType: op.ListType, ListPath: op.ListPath, Section: op.Section, Values: op.Values, Category: op.Category})
		kb := &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{
				{{Text: "⏸ Добавить отключёнными", CallbackData: cbPrefix + confirmID + ":confirm"}},
				{{Text: "❌ Отмена", CallbackData: cbPrefix + confirmID + ":cancel"}},
			},
		}
		a.answerAndEditMarkup(ctx, b, update, msg, kb)
		return
	}

	if len(active) == 0 {
		a.logf(op.ChatID, "disable skipped_already_disabled count=%d", len(op.Values))
		a.answerAndEdit(ctx, b, update, "ℹ️ Все записи уже отключены.")
		return
	}
	a.logf(op.ChatID, "disable confirm_only active=%d", len(active))

	confirmID := a.sess.Create(PendingOp{ChatID: op.ChatID, Kind: ActionDisableConfirm, ListType: op.ListType, ListPath: op.ListPath, Section: op.Section, Values: op.Values, Category: op.Category})
	kb := &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: "✅ Подтвердить", CallbackData: cbPrefix + confirmID + ":confirm"}},
			{{Text: "❌ Отмена", CallbackData: cbPrefix + confirmID + ":cancel"}},
		},
	}
	a.answerAndEditMarkup(ctx, b, update, msg, kb)
}

func (a *App) execDisable(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	if err := lists.DisableExistingOnly(op.ListPath, op.Values, op.ListType); err != nil {
		a.logf(op.ChatID, "disable_existing write_error path=%q err=%v", op.ListPath, err)
		a.answerAndEdit(ctx, b, update, "Ошибка: "+err.Error())
		return
	}
	a.logf(op.ChatID, "disable_existing success count=%d path=%q", len(op.Values), op.ListPath)
	a.sess.Delete(op.ID)
	a.afterFilesChanged(ctx, b, update, op.ChatID, opTargetLine(op)+"⏸ Записи отключены.")
}

func (a *App) execDisableWithMissing(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	if err := lists.Disable(op.ListPath, op.Values, op.ListType, op.Category); err != nil {
		a.logf(op.ChatID, "disable_with_missing write_error path=%q err=%v", op.ListPath, err)
		a.answerAndEdit(ctx, b, update, "Ошибка: "+err.Error())
		return
	}
	a.logf(op.ChatID, "disable_with_missing success count=%d path=%q", len(op.Values), op.ListPath)
	a.sess.Delete(op.ID)
	a.afterFilesChanged(ctx, b, update, op.ChatID, opTargetLine(op)+"⏸ Записи отключены (включая добавленные).")
}

func (a *App) handleStartCreate(ctx context.Context, b *tgbot.Bot, update *models.Update, chatID int64) {
	missing := a.missingFiles(ctx)
	if err := lists.CreateFiles(missing...); err != nil {
		a.logf(chatID, "create_files_error files=%q err=%v", strings.Join(missing, ","), err)
		a.answerAndEdit(ctx, b, update, "Ошибка создания файлов: "+err.Error())
		return
	}
	a.setReady(chatID, true)
	a.logf(chatID, "create_files_success files=%q", strings.Join(missing, ","))
	a.answerAndEditMarkup(ctx, b, update, a.welcomeText(ctx), a.mainMenuInlineKeyboard())
	a.ensureReplyKeyboard(ctx, b, chatID)
}

func (a *App) afterAddSuccess(ctx context.Context, b *tgbot.Bot, update *models.Update, chatID int64, msg string) {
	a.afterFilesChanged(ctx, b, update, chatID, msg)
}

func (a *App) afterFilesChanged(ctx context.Context, b *tgbot.Bot, update *models.Update, chatID int64, msg string) {
	_ = a.svc.MarkFilesChanged()
	a.answerAndEditMarkup(ctx, b, update, msg, a.backToMainMenuInlineKeyboard())
	a.maybeAutoRestart(chatID, b)
}

func (a *App) maybeAutoRestart(chatID int64, b *tgbot.Bot) {
	if a.cfg.GetAutoRestart() && a.cfg.RestartCmd != "" {
		go a.runRestartNotify(context.Background(), b, chatID)
	}
}

func (a *App) runRestartNotify(parentCtx context.Context, b *tgbot.Bot, chatID int64) {
	if a.cfg.RestartCmd == "" {
		a.logf(chatID, "restart skipped reason=restart_cmd_empty")
		_, _ = b.SendMessage(parentCtx, &tgbot.SendMessageParams{
			ChatID: chatID,
			Text:   "Команда перезапуска не настроена.",
		})
		return
	}

	label := a.cfg.ServiceLabel
	a.logf(chatID, "restart started label=%q cmd=%q auto=%t", label, a.cfg.RestartCmd, a.cfg.GetAutoRestart())

	sent, err := b.SendMessage(parentCtx, &tgbot.SendMessageParams{
		ChatID: chatID,
		Text:   fmt.Sprintf("Перезапуск %s…", label),
	})
	if err != nil {
		a.logf(chatID, "restart status_message_error err=%v", err)
		return
	}
	messageID := sent.ID

	rctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	start := time.Now()
	res := service.RunRestartWithProgress(rctx, a.cfg.RestartCmd, func(elapsed time.Duration) {
		if _, err := b.EditMessageText(rctx, &tgbot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: messageID,
			Text:      fmt.Sprintf("Перезапуск %s… (%ds)", label, int(elapsed.Seconds())),
		}); err != nil {
			a.logf(chatID, "restart progress_edit_error err=%v", err)
		}
	})

	text := a.formatRestartResult(rctx, chatID, res, label, int(time.Since(start).Seconds()))
	if res.Success {
		_ = a.svc.MarkRestarted()
		a.logf(chatID, "restart success duration_sec=%d", int(time.Since(start).Seconds()))
	} else {
		errText := "неизвестная ошибка"
		if res.Err != nil {
			errText = res.Err.Error()
		}
		a.logf(chatID, "restart failed err=%q", errText)
	}

	a.editOrResend(context.Background(), b, chatID, messageID, text)
}

// editOrResend updates the status message, retrying on transient network errors.
// If all retries fail, it sends a new message so the final result is not silently lost.
func (a *App) editOrResend(ctx context.Context, b *tgbot.Bot, chatID int64, messageID int, text string) {
	const attempts = 3
	var lastErr error
	kb := a.backToMainMenuInlineKeyboard()
	for i := 0; i < attempts; i++ {
		if i > 0 {
			time.Sleep(time.Duration(i) * time.Second)
		}
		if _, err := b.EditMessageText(ctx, &tgbot.EditMessageTextParams{
			ChatID:      chatID,
			MessageID:   messageID,
			Text:        text,
			ReplyMarkup: kb,
		}); err != nil {
			lastErr = err
			a.logf(chatID, "restart result_edit_error attempt=%d err=%v", i+1, err)
			continue
		}
		return
	}
	a.logf(chatID, "restart result_edit_failed err=%v fallback=send_new_message", lastErr)
	if _, err := b.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID:      chatID,
		Text:        text,
		ReplyMarkup: kb,
	}); err != nil {
		a.logf(chatID, "restart result_send_error err=%v", err)
	}
}

func (a *App) formatRestartResult(ctx context.Context, chatID int64, res service.RestartResult, label string, durationSec int) string {
	if res.Success {
		text := fmt.Sprintf("✅ %s перезапущен (%ds).", label, durationSec)
		if isPodkopCommand(a.cfg.RestartCmd) {
			secs, err := podkop.Sections(ctx)
			if err != nil {
				a.logf(chatID, "podkop_sections_error err=%v", err)
			} else if !anyListBound(secs) {
				text += "\n\n⚠️ Podkop перезапущен, но ни в одной его секции нет привязанных списков.\n" +
					"Откройте «" + btnManage + "», выберите секцию и привяжите файл."
			}
		}
		if res.Output != "" {
			text += "\n\n" + strings.TrimSpace(res.Output)
		}
		return text
	}

	errText := "неизвестная ошибка"
	if res.Err != nil {
		errText = res.Err.Error()
	}
	text := fmt.Sprintf("❌ Ошибка перезапуска %s: %s", label, errText)
	if res.Output != "" {
		text += "\n\n" + strings.TrimSpace(res.Output)
	}
	return text
}

func isPodkopCommand(cmd string) bool {
	return strings.Contains(strings.ToLower(cmd), "podkop")
}

func (a *App) podkopIntegrationText(ctx context.Context) (string, bool) {
	if !isPodkopCommand(a.cfg.RestartCmd) {
		return "", false
	}
	secs, err := podkop.Sections(ctx)
	if err != nil {
		return "Не удалось прочитать конфиг podkop: " + err.Error(), true
	}
	if len(secs) == 0 {
		return "🔗 В конфиге podkop нет секций.", true
	}

	var sb strings.Builder
	sb.WriteString("🔗 Списки в секциях Podkop:")
	for _, s := range secs {
		sb.WriteString("\n\n🗂 " + s.Name)
		if s.ConnectionType != "" {
			sb.WriteString(" · " + s.ConnectionType)
		}
		sb.WriteString("\n" + sectionBindingLine(s, lists.TypeDomain))
		sb.WriteString("\n" + sectionBindingLine(s, lists.TypeIP))
	}
	if !anyListBound(secs) {
		sb.WriteString("\n\nНи один файл не привязан. Откройте «" + btnManage + "» и привяжите файл к нужной секции.")
	}
	return sb.String(), true
}

// anyListBound reports whether podkop reads any list file at all.
func anyListBound(secs []podkop.Section) bool {
	for _, s := range secs {
		if len(s.DomainLists) > 0 || len(s.SubnetLists) > 0 {
			return true
		}
	}
	return false
}

func (a *App) answerAndEdit(ctx context.Context, b *tgbot.Bot, update *models.Update, text string) {
	a.answerAndEditMarkup(ctx, b, update, text, nil)
}

func classifyBuckets(path string, values []string) (newVals, active, disabled []string, err error) {
	if len(values) == 0 {
		return nil, nil, nil, nil
	}
	classified, err := lists.ClassifyValues(path, values)
	if err != nil {
		return nil, nil, nil, err
	}
	newVals, active, disabled = lists.GroupByStatus(classified)
	return newVals, active, disabled, nil
}

func appendFileStatus(sb *strings.Builder, title string, newVals, active, disabled []string) {
	if len(newVals) == 0 && len(active) == 0 && len(disabled) == 0 {
		return
	}
	sb.WriteString("\n\n")
	sb.WriteString(title)
	if len(newVals) > 0 {
		sb.WriteString(fmt.Sprintf("\n🆕 Новые:\n%s", lists.FormatList(newVals)))
	}
	if len(active) > 0 {
		sb.WriteString(fmt.Sprintf("\n✅ Уже активны:\n%s", lists.FormatList(active)))
	}
	if len(disabled) > 0 {
		sb.WriteString(fmt.Sprintf("\n⏸ Уже отключены:\n%s", lists.FormatList(disabled)))
	}
}

func buildListInputMessage(
	label string,
	values []string,
	newVals, active, disabled []string,
) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📥 Получено (%s):\n%s", label, lists.FormatList(values)))
	appendFileStatus(&sb, "📋 В файле:", newVals, active, disabled)
	return sb.String()
}

func buildListInputKeyboard(
	opID string,
	newVals, active, disabled []string,
) ([][]models.InlineKeyboardButton, bool) {
	cancelBtn := models.InlineKeyboardButton{Text: "❌ Отмена", CallbackData: cbPrefix + opID + ":cancel"}

	canAddNew := len(newVals) > 0
	canEnable := len(disabled) > 0
	canAddAll := len(newVals) > 0 && len(disabled) > 0
	canDisable := len(active) > 0
	canDelete := len(active) > 0 || len(disabled) > 0

	var rows [][]models.InlineKeyboardButton
	hasActions := false

	var addRow []models.InlineKeyboardButton
	if canAddNew {
		addRow = append(addRow, models.InlineKeyboardButton{
			Text: "➕ Добавить", CallbackData: cbPrefix + opID + ":add",
		})
		hasActions = true
	}
	if canEnable {
		addRow = append(addRow, models.InlineKeyboardButton{
			Text: "✅ Включить", CallbackData: cbPrefix + opID + ":en",
		})
		hasActions = true
	}
	if canAddAll {
		addRow = append(addRow, models.InlineKeyboardButton{
			Text: "➕ Добавить всё", CallbackData: cbPrefix + opID + ":addall",
		})
		hasActions = true
	}
	if len(addRow) > 0 {
		rows = append(rows, addRow)
	}
	// Always offered: even when every value is already in the file, the user
	// may want to re-file them under a different category.
	rows = append(rows, []models.InlineKeyboardButton{{
		Text: btnAddToCategory, CallbackData: cbPrefix + opID + ":cat",
	}})
	hasActions = true

	if canDelete {
		rows = append(rows, []models.InlineKeyboardButton{{
			Text: "🗑 Удалить", CallbackData: cbPrefix + opID + ":del",
		}})
		hasActions = true
	}

	var bottomRow []models.InlineKeyboardButton
	if canDisable {
		bottomRow = append(bottomRow, models.InlineKeyboardButton{
			Text: "⏸ Отключить", CallbackData: cbPrefix + opID + ":dis",
		})
		hasActions = true
	}
	bottomRow = append(bottomRow, cancelBtn)
	rows = append(rows, bottomRow)

	return rows, hasActions
}

func buildMixedInputMessage(text string) string {
	var domains []string
	var ips []string

	for _, token := range lists.SplitInput(text) {
		switch lists.ClassifyToken(token) {
		case lists.TypeDomain:
			domains = append(domains, token)
		case lists.TypeIP:
			ips = append(ips, token)
		}
	}

	var sb strings.Builder
	sb.WriteString("⚠️ В одном сообщении смешаны разные типы записей.")
	if len(domains) > 0 {
		sb.WriteString("\n\n🌐 Домены:\n")
		sb.WriteString(lists.FormatList(domains))
	}
	if len(ips) > 0 {
		sb.WriteString("\n\n🧩 IP/CIDR:\n")
		sb.WriteString(lists.FormatList(ips))
	}
	sb.WriteString("\n\nОтправьте их отдельно:\n")
	if len(domains) > 0 {
		sb.WriteString("• сначала только домены\n")
	}
	if len(ips) > 0 {
		sb.WriteString("• потом только IP/CIDR\n")
	}
	sb.WriteString("После каждого сообщения бот предложит подходящие действия.")

	return strings.TrimRight(sb.String(), "\n")
}

func (a *App) answerAndEditMarkup(ctx context.Context, b *tgbot.Bot, update *models.Update, text string, kb *models.InlineKeyboardMarkup) {
	_, _ = b.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
	})
	a.editCallbackMessageMarkup(ctx, b, update, text, kb)
}
