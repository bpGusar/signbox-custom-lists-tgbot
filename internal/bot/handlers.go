package bot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"lst-signbox-lists-tgbot/internal/lists"
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
	missing := lists.MissingFiles(a.cfg.DomainList, a.cfg.IPList)
	if len(missing) > 0 {
		a.logf(chatID, "start_check missing_files=%s", strings.Join(missing, ","))
		text := "📂 Отсутствуют файлы:\n" + strings.Join(missing, "\n")
		kb := &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{
				{{Text: "📝 Создать файлы", CallbackData: cbPrefix + a.sess.Create(chatID, ActionStartCreate, 0, "", nil, nil)}},
				{{Text: "🔄 Проверить снова", CallbackData: cbPrefix + a.sess.Create(chatID, ActionStartRetry, 0, "", nil, nil)}},
			},
		}
		_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
			ChatID:      chatID,
			Text:        text,
			ReplyMarkup: kb,
		})
		return
	}

	a.ready[chatID] = true
	a.logf(chatID, "start_check ready=true")
	text := a.welcomeText(ctx)
	_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID:      chatID,
		Text:        text,
		ReplyMarkup: a.mainMenuKeyboard(),
	})
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
		"Бот определит тип, проверит файл и предложит действия.\n\n" +
		fmt.Sprintf("📄 Домены: %s\n📄 IP: %s", a.cfg.DomainList, a.cfg.IPList) +
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

	switch a.verChecker.Check(rctx) {
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

func (a *App) handleShowMenu(ctx context.Context, b *tgbot.Bot, update *models.Update) {
	chatID := update.Message.Chat.ID
	a.logf(chatID, "command=/menu")
	_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID:      chatID,
		Text:        "⌨️ Меню",
		ReplyMarkup: a.mainMenuKeyboard(),
	})
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
		return "Кнопка перезапуска не показана: не настроен restart_cmd.\n" +
			"Настройка через UCI:\n" +
			"uci set lst-signbox-lists-tgbot.main.restart_cmd='/etc/init.d/sing-box restart'\n" +
			"uci commit lst-signbox-lists-tgbot\n" +
			"/etc/init.d/lst-signbox-lists-tgbot restart\n" +
			"Или через ENV: LST_SIGNBOX_LISTS_TGBOT_RESTART_CMD='...'."
	}
	return ""
}

func (a *App) mainMenuKeyboard() *models.ReplyKeyboardMarkup {
	rows := [][]models.KeyboardButton{
		{
			{Text: menuBtnDownloadIP},
			{Text: menuBtnDownloadDomains},
		},
		{
			{Text: menuBtnViewIP},
			{Text: menuBtnViewDomains},
		},
	}
	if a.cfg.RestartCmd != "" {
		rows = append(rows, []models.KeyboardButton{{Text: a.menuBtnRestart()}})
	}
	if isPodkopCommand(a.cfg.RestartCmd) {
		rows = append(rows, []models.KeyboardButton{{Text: menuBtnCheckPodkop}})
	}
	return &models.ReplyKeyboardMarkup{
		Keyboard:              rows,
		ResizeKeyboard:        true,
		InputFieldPlaceholder: "домены или IP/CIDR",
	}
}

func (a *App) handleMenuAction(ctx context.Context, b *tgbot.Bot, chatID int64, text string) bool {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case strings.ToLower(menuBtnDownloadIP):
		a.logf(chatID, "menu download_ip")
		a.sendListFile(ctx, b, chatID, a.cfg.IPList, "ip_list.lst", "IP-список")
		return true
	case strings.ToLower(menuBtnDownloadDomains):
		a.logf(chatID, "menu download_domains")
		a.sendListFile(ctx, b, chatID, a.cfg.DomainList, "domain_list.lst", "список доменов")
		return true
	case strings.ToLower(menuBtnViewIP):
		a.logf(chatID, "menu view_ip")
		a.sendListContent(ctx, b, chatID, a.cfg.IPList, "IP-список")
		return true
	case strings.ToLower(menuBtnViewDomains):
		a.logf(chatID, "menu view_domains")
		a.sendListContent(ctx, b, chatID, a.cfg.DomainList, "список доменов")
		return true
	case strings.ToLower(menuBtnCheckPodkop):
		a.logf(chatID, "menu check_podkop")
		a.sendPodkopIntegrationCheck(ctx, b, chatID)
		return true
	default:
		if a.cfg.RestartCmd != "" && strings.EqualFold(strings.TrimSpace(text), a.menuBtnRestart()) {
			a.logf(chatID, "menu restart")
			a.runRestartNotify(ctx, b, chatID)
			return true
		}
		return false
	}
}

func (a *App) sendListFile(ctx context.Context, b *tgbot.Bot, chatID int64, path, fallbackName, label string) {
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

	filename := filepath.Base(path)
	if filename == "" || filename == "." || filename == string(filepath.Separator) {
		filename = fallbackName
	}

	_, err = b.SendDocument(ctx, &tgbot.SendDocumentParams{
		ChatID: chatID,
		Document: &models.InputFileUpload{
			Filename: filename,
			Data:     f,
		},
		Caption: fmt.Sprintf("%s (%s)", label, path),
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

func (a *App) sendListContent(ctx context.Context, b *tgbot.Bot, chatID int64, path, label string) {
	data, err := os.ReadFile(path)
	if err != nil {
		a.logf(chatID, "view_file_error label=%q path=%q err=%v", label, path, err)
		_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
			ChatID: chatID,
			Text:   fmt.Sprintf("Не удалось открыть %s (%s): %v", label, path, err),
		})
		return
	}

	content := strings.TrimRight(string(data), "\r\n")
	header := fmt.Sprintf("%s (%s):", label, path)
	if content == "" {
		a.sendLongText(ctx, b, chatID, header+"\n\nФайл пуст.")
		a.logf(chatID, "view_file_sent label=%q path=%q empty=true", label, path)
		return
	}

	a.sendLongText(ctx, b, chatID, header+"\n\n"+content)
	a.logf(chatID, "view_file_sent label=%q path=%q bytes=%d", label, path, len(data))
}

func (a *App) sendLongText(ctx context.Context, b *tgbot.Bot, chatID int64, text string) {
	for text != "" {
		chunk := text
		if len(chunk) > tgMaxMessageLen {
			chunk = text[:tgMaxMessageLen]
			if i := strings.LastIndex(chunk, "\n"); i > tgMaxMessageLen/2 {
				chunk = text[:i+1]
			}
		}
		_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
			ChatID: chatID,
			Text:   chunk,
		})
		text = text[len(chunk):]
	}
}

func (a *App) sendPodkopIntegrationCheck(ctx context.Context, b *tgbot.Bot, chatID int64) {
	text, ok := a.podkopIntegrationText(ctx)
	if !ok {
		_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
			ChatID: chatID,
			Text:   "ℹ️ Проверка интеграции доступна только для podkop.",
		})
		return
	}
	_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID: chatID,
		Text:   text,
	})
}

func (a *App) handleListInput(ctx context.Context, b *tgbot.Bot, update *models.Update) {
	chatID := update.Message.Chat.ID
	text := update.Message.Text

	parsed := lists.ParseInput(text)
	if parsed.Empty {
		a.logf(chatID, "list_input empty")
		return
	}
	if len(parsed.Invalid) > 0 {
		a.logf(chatID, "list_input invalid_count=%d", len(parsed.Invalid))
		msg := "❌ Невалидные записи:\n" + lists.FormatList(parsed.Invalid)
		_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{ChatID: chatID, Text: msg})
		return
	}
	if parsed.Mixed {
		a.logf(chatID, "list_input mixed_types count=%d", len(parsed.Valid))
		_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
			ChatID: chatID,
			Text:   "⚠️ Отправьте только домены или только IP/CIDR в одном сообщении.",
		})
		return
	}

	path := listPath(a.cfg, parsed.Type)
	label := typeLabel(parsed.Type)
	a.logf(chatID, "list_input accepted type=%s count=%d path=%q",
		label, len(parsed.Valid), path)

	validNew, validActive, validDisabled, err := classifyBuckets(path, parsed.Valid)
	if err != nil {
		a.logf(chatID, "list_input classify_error path=%q err=%v", path, err)
		_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
			ChatID: chatID,
			Text:   "❌ Ошибка чтения файла: " + err.Error(),
		})
		return
	}

	opID := a.sess.Create(chatID, ActionAdd, parsed.Type, path, parsed.Valid, nil)
	msg := buildListInputMessage(label, parsed.Valid, validNew, validActive, validDisabled)
	rows, hasActions := buildListInputKeyboard(opID, validNew, validActive, validDisabled)
	if !hasActions {
		msg += "\n\nℹ️ Нечего делать — все записи уже в нужном состоянии."
	}
	kb := &models.InlineKeyboardMarkup{InlineKeyboard: rows}

	reply := msg
	if banner := a.svc.StaleBanner(); banner != "" {
		reply = banner + "\n\n" + msg
		if reason := a.restartHiddenReason(); reason != "" {
			reply += "\n\n" + reason
		}
	}

	_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID:      chatID,
		Text:        reply,
		ReplyMarkup: kb,
	})
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
		a.answerAndEdit(ctx, b, update, "❌ Операция отменена.")
		return
	}

	switch op.Kind {
	case ActionAdd:
		switch action {
		case "apply":
			a.handleApplyMixed(ctx, b, update, op)
		case "add":
			a.handleAdd(ctx, b, update, op)
		case "del":
			vals := op.Values
			if len(vals) == 0 {
				vals = op.DisableValues
			}
			a.handleDelete(ctx, b, update, opForValues(op, vals))
		case "dis":
			a.handleDisablePrompt(ctx, b, update, opForDisable(op))
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

func (a *App) handleApplyMixed(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	var parts []string
	if len(op.Values) > 0 {
		if err := lists.AddNew(op.ListPath, op.Values); err != nil {
			a.logf(op.ChatID, "apply_mixed add_error path=%q err=%v", op.ListPath, err)
			a.answerAndEdit(ctx, b, update, "❌ Ошибка добавления: "+err.Error())
			return
		}
		parts = append(parts, fmt.Sprintf("➕ Добавлено (%d):\n%s", len(op.Values), lists.FormatList(op.Values)))
	}
	if len(op.DisableValues) > 0 {
		if err := lists.Disable(op.ListPath, op.DisableValues); err != nil {
			a.logf(op.ChatID, "apply_mixed disable_error path=%q err=%v", op.ListPath, err)
			a.answerAndEdit(ctx, b, update, "❌ Ошибка отключения: "+err.Error())
			return
		}
		parts = append(parts, fmt.Sprintf("⏸ Отключено (%d):\n%s", len(op.DisableValues), lists.FormatDisabledList(op.DisableValues)))
	}
	a.logf(op.ChatID, "apply_mixed success add=%d disable=%d path=%q", len(op.Values), len(op.DisableValues), op.ListPath)
	a.sess.Delete(op.ID)
	a.afterFilesChanged(ctx, b, update, op.ChatID, strings.Join(parts, "\n\n"))
}

func (a *App) handleAdd(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	classified, err := lists.ClassifyValues(op.ListPath, op.Values)
	if err != nil {
		a.logf(op.ChatID, "add classify_error path=%q err=%v", op.ListPath, err)
		a.answerAndEdit(ctx, b, update, "Ошибка чтения файла: "+err.Error())
		return
	}

	newVals, active, disabled := lists.GroupByStatus(classified)

	if len(disabled) > 0 {
		a.logf(op.ChatID, "add requires_choice disabled=%d new=%d active=%d", len(disabled), len(newVals), len(active))
		msg := fmt.Sprintf(
			"Добавление (%s):\n\nБудут включены (были отключены):\n%s\n\nНовые:\n%s\n\nУже активны (пропустятся):\n%s",
			typeLabel(op.ListType),
			lists.FormatList(disabled),
			lists.FormatList(newVals),
			lists.FormatList(active),
		)
		allID := a.sess.Create(op.ChatID, ActionAddAll, op.ListType, op.ListPath, op.Values, nil)
		newID := a.sess.Create(op.ChatID, ActionAddNew, op.ListType, op.ListPath, op.Values, nil)
		a.sess.Delete(op.ID)

		kb := &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{
				{{Text: "➕ Добавить всё", CallbackData: cbPrefix + allID + ":confirm"}},
				{{Text: "✨ Только новые", CallbackData: cbPrefix + newID + ":confirm"}},
				{{Text: "❌ Отмена", CallbackData: cbPrefix + allID + ":cancel"}},
			},
		}
		a.answerAndEditMarkup(ctx, b, update, msg, kb)
		return
	}

	if len(newVals) == 0 {
		a.logf(op.ChatID, "add skipped_all_exist count=%d", len(op.Values))
		a.sess.Delete(op.ID)
		a.answerAndEdit(ctx, b, update, "ℹ️ Все записи уже есть в списке.")
		return
	}

	if err := lists.AddNew(op.ListPath, op.Values); err != nil {
		a.logf(op.ChatID, "add write_error path=%q err=%v", op.ListPath, err)
		a.answerAndEdit(ctx, b, update, "Ошибка записи: "+err.Error())
		return
	}
	a.logf(op.ChatID, "add success new_count=%d path=%q", len(newVals), op.ListPath)
	a.sess.Delete(op.ID)
	a.afterAddSuccess(ctx, b, update, op.ChatID, fmt.Sprintf("➕ Добавлено (%d):\n%s", len(newVals), lists.FormatList(newVals)))
}

func (a *App) execAddAll(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	if err := lists.AddAll(op.ListPath, op.Values); err != nil {
		a.logf(op.ChatID, "add_all write_error path=%q err=%v", op.ListPath, err)
		a.answerAndEdit(ctx, b, update, "Ошибка записи: "+err.Error())
		return
	}
	a.logf(op.ChatID, "add_all success count=%d path=%q", len(op.Values), op.ListPath)
	a.sess.Delete(op.ID)
	a.afterAddSuccess(ctx, b, update, op.ChatID, "➕ Записи добавлены/включены.")
}

func (a *App) execAddNew(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	if err := lists.AddNew(op.ListPath, op.Values); err != nil {
		a.logf(op.ChatID, "add_new write_error path=%q err=%v", op.ListPath, err)
		a.answerAndEdit(ctx, b, update, "Ошибка записи: "+err.Error())
		return
	}
	a.logf(op.ChatID, "add_new success count=%d path=%q", len(op.Values), op.ListPath)
	a.sess.Delete(op.ID)
	a.afterAddSuccess(ctx, b, update, op.ChatID, "✨ Новые записи добавлены.")
}

func (a *App) handleDelete(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	if err := lists.Delete(op.ListPath, op.Values); err != nil {
		a.logf(op.ChatID, "delete write_error path=%q err=%v", op.ListPath, err)
		a.answerAndEdit(ctx, b, update, "Ошибка удаления: "+err.Error())
		return
	}
	a.logf(op.ChatID, "delete success count=%d path=%q", len(op.Values), op.ListPath)
	a.sess.Delete(op.ID)
	a.afterFilesChanged(ctx, b, update, op.ChatID, fmt.Sprintf("🗑 Удалено:\n%s", lists.FormatList(op.Values)))
}

func (a *App) handleDisablePrompt(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	classified, err := lists.ClassifyValues(op.ListPath, op.Values)
	if err != nil {
		a.logf(op.ChatID, "disable classify_error path=%q err=%v", op.ListPath, err)
		a.answerAndEdit(ctx, b, update, "Ошибка чтения файла: "+err.Error())
		return
	}

	newVals, active, disabled := lists.GroupByStatus(classified)

	msg := fmt.Sprintf(
		"Отключение (%s):\n\nУже отключены:\n%s\n\nБудут отключены:\n%s\n\nОтсутствуют в файле:\n%s",
		typeLabel(op.ListType),
		lists.FormatList(disabled),
		lists.FormatList(active),
		lists.FormatList(newVals),
	)

	a.sess.Delete(op.ID)

	if len(newVals) > 0 {
		a.logf(op.ChatID, "disable requires_add_missing missing=%d active=%d disabled=%d", len(newVals), len(active), len(disabled))
		confirmID := a.sess.Create(op.ChatID, ActionDisableAddMissing, op.ListType, op.ListPath, op.Values, nil)
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

	confirmID := a.sess.Create(op.ChatID, ActionDisableConfirm, op.ListType, op.ListPath, op.Values, nil)
	kb := &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: "✅ Подтвердить", CallbackData: cbPrefix + confirmID + ":confirm"}},
			{{Text: "❌ Отмена", CallbackData: cbPrefix + confirmID + ":cancel"}},
		},
	}
	a.answerAndEditMarkup(ctx, b, update, msg, kb)
}

func (a *App) execDisable(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	if err := lists.DisableExistingOnly(op.ListPath, op.Values); err != nil {
		a.logf(op.ChatID, "disable_existing write_error path=%q err=%v", op.ListPath, err)
		a.answerAndEdit(ctx, b, update, "Ошибка: "+err.Error())
		return
	}
	a.logf(op.ChatID, "disable_existing success count=%d path=%q", len(op.Values), op.ListPath)
	a.sess.Delete(op.ID)
	a.afterFilesChanged(ctx, b, update, op.ChatID, "⏸ Записи отключены.")
}

func (a *App) execDisableWithMissing(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	if err := lists.Disable(op.ListPath, op.Values); err != nil {
		a.logf(op.ChatID, "disable_with_missing write_error path=%q err=%v", op.ListPath, err)
		a.answerAndEdit(ctx, b, update, "Ошибка: "+err.Error())
		return
	}
	a.logf(op.ChatID, "disable_with_missing success count=%d path=%q", len(op.Values), op.ListPath)
	a.sess.Delete(op.ID)
	a.afterFilesChanged(ctx, b, update, op.ChatID, "⏸ Записи отключены (включая добавленные).")
}

func (a *App) handleStartCreate(ctx context.Context, b *tgbot.Bot, update *models.Update, chatID int64) {
	if err := lists.CreateFiles(a.cfg.DomainList, a.cfg.IPList); err != nil {
		a.logf(chatID, "create_files_error domain=%q ip=%q err=%v", a.cfg.DomainList, a.cfg.IPList, err)
		a.answerAndEdit(ctx, b, update, "Ошибка создания файлов: "+err.Error())
		return
	}
	a.ready[chatID] = true
	a.logf(chatID, "create_files_success domain=%q ip=%q", a.cfg.DomainList, a.cfg.IPList)
	a.answerAndEdit(ctx, b, update, a.welcomeText(ctx))
}

func (a *App) afterAddSuccess(ctx context.Context, b *tgbot.Bot, update *models.Update, chatID int64, msg string) {
	a.afterFilesChanged(ctx, b, update, chatID, msg)
}

func (a *App) afterFilesChanged(ctx context.Context, b *tgbot.Bot, update *models.Update, chatID int64, msg string) {
	_ = a.svc.MarkFilesChanged()
	a.answerAndEdit(ctx, b, update, msg)
	a.maybeAutoRestart(chatID, b)
}

func (a *App) maybeAutoRestart(chatID int64, b *tgbot.Bot) {
	if a.cfg.AutoRestart && a.cfg.RestartCmd != "" {
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
	a.logf(chatID, "restart started label=%q cmd=%q auto=%t", label, a.cfg.RestartCmd, a.cfg.AutoRestart)

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
		_, _ = b.EditMessageText(rctx, &tgbot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: messageID,
			Text:      fmt.Sprintf("Перезапуск %s… (%ds)", label, int(elapsed.Seconds())),
		})
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

	_, _ = b.EditMessageText(rctx, &tgbot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: messageID,
		Text:      text,
	})
}

func (a *App) formatRestartResult(ctx context.Context, chatID int64, res service.RestartResult, label string, durationSec int) string {
	if res.Success {
		text := fmt.Sprintf("✅ %s перезапущен (%ds).", label, durationSec)
		if isPodkopCommand(a.cfg.RestartCmd) {
			bindings, err := service.CheckPodkopBindings(ctx, a.cfg.DomainList, a.cfg.IPList)
			if err != nil {
				a.logf(chatID, "podkop_binding_check_error err=%v", err)
			} else if !bindings.DomainBound || !bindings.IPBound {
				missing := make([]string, 0, 2)
				if !bindings.DomainBound {
					missing = append(missing, "• local_domain_lists: "+a.cfg.DomainList)
				}
				if !bindings.IPBound {
					missing = append(missing, "• local_subnet_lists: "+a.cfg.IPList)
				}
				text += "\n\n⚠️ Podkop перезапущен, но файлы не привязаны в /etc/config/podkop:\n" +
					strings.Join(missing, "\n") +
					"\n\nДобавьте эти пути в нужной секции Podkop (Sections -> Local lists), затем перезапустите Podkop снова."
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
	st, err := service.CheckPodkopBindings(ctx, a.cfg.DomainList, a.cfg.IPList)
	if err != nil {
		return "Не удалось проверить интеграцию Podkop: " + err.Error(), true
	}

	domainLine := "❌ не подключен"
	if st.DomainBound {
		domainLine = "✅ подключен"
	}
	ipLine := "❌ не подключен"
	if st.IPBound {
		ipLine = "✅ подключен"
	}
	text := "🔗 Проверка интеграции Podkop:\n" +
		"• local_domain_lists: " + domainLine + "\n" +
		"• local_subnet_lists: " + ipLine

	if !st.DomainBound || !st.IPBound {
		text += "\n\nДобавьте пути списков в нужную секцию Podkop (Sections -> Local lists):\n" +
			"• " + a.cfg.DomainList + "\n" +
			"• " + a.cfg.IPList
	}
	return text, true
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

	canAdd := len(newVals) > 0 || len(disabled) > 0
	canDisable := len(active) > 0 || len(newVals) > 0
	canDelete := len(active) > 0 || len(disabled) > 0

	var rows [][]models.InlineKeyboardButton
	hasActions := false

	var topRow []models.InlineKeyboardButton
	if canAdd {
		topRow = append(topRow, models.InlineKeyboardButton{
			Text: "➕ Добавить", CallbackData: cbPrefix + opID + ":add",
		})
		hasActions = true
	}
	if canDelete {
		topRow = append(topRow, models.InlineKeyboardButton{
			Text: "🗑 Удалить", CallbackData: cbPrefix + opID + ":del",
		})
		hasActions = true
	}
	if len(topRow) > 0 {
		rows = append(rows, topRow)
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

func (a *App) answerAndEditMarkup(ctx context.Context, b *tgbot.Bot, update *models.Update, text string, kb *models.InlineKeyboardMarkup) {
	_, _ = b.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
	})

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
	}
	if kb != nil {
		params.ReplyMarkup = kb
	}
	_, _ = b.EditMessageText(ctx, params)
}
