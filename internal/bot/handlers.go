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
		text := "Отсутствуют файлы:\n" + strings.Join(missing, "\n")
		kb := &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{
				{{Text: "Создать файлы", CallbackData: cbPrefix + a.sess.Create(chatID, ActionStartCreate, 0, "", nil)}},
				{{Text: "Проверить снова", CallbackData: cbPrefix + a.sess.Create(chatID, ActionStartRetry, 0, "", nil)}},
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
	text := a.welcomeText()
	_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID:      chatID,
		Text:        text,
		ReplyMarkup: a.mainMenuKeyboard(),
	})
	if kb := a.staleKeyboard(); kb != nil {
		_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
			ChatID:      chatID,
			Text:        "Доступны действия по сервису:",
			ReplyMarkup: kb,
		})
	}
}

func (a *App) welcomeText() string {
	text := "Бот готов к работе.\n\n" +
		"Отправьте список доменов или IP/CIDR через запятую или с новой строки.\n" +
		"Бот определит тип и предложит действия.\n\n" +
		fmt.Sprintf("Домены: %s\nIP: %s", a.cfg.DomainList, a.cfg.IPList)

	if banner := a.svc.StaleBanner(); banner != "" {
		text = banner + "\n\n" + text
		if reason := a.restartHiddenReason(); reason != "" {
			text += "\n\n" + reason
		}
	}
	return text
}

func (a *App) staleKeyboard() *models.InlineKeyboardMarkup {
	rows := make([][]models.InlineKeyboardButton, 0, 2)
	if row := a.restartButtonRow(); row != nil {
		rows = append(rows, row)
	}
	if row := a.integrationButtonRow(); row != nil {
		rows = append(rows, row)
	}
	if len(rows) > 0 {
		return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
	}
	return nil
}

func (a *App) restartButtonRow() []models.InlineKeyboardButton {
	st, _ := a.svc.Load()
	if st == nil || !st.ServiceStale || a.cfg.RestartCmd == "" {
		return nil
	}
	label := "Перезапустить " + a.cfg.ServiceLabel
	id := a.sess.Create(0, ActionRestart, 0, "", nil)
	return []models.InlineKeyboardButton{{Text: label, CallbackData: cbPrefix + id}}
}

func (a *App) integrationButtonRow() []models.InlineKeyboardButton {
	if !isPodkopCommand(a.cfg.RestartCmd) {
		return nil
	}
	id := a.sess.Create(0, ActionCheckIntegration, 0, "", nil)
	return []models.InlineKeyboardButton{{Text: "Проверить интеграцию Podkop", CallbackData: cbPrefix + id}}
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
	return &models.ReplyKeyboardMarkup{
		Keyboard: [][]models.KeyboardButton{
			{
				{Text: menuBtnDownloadIP},
				{Text: menuBtnDownloadDomains},
			},
		},
		ResizeKeyboard:        true,
		IsPersistent:          true,
		InputFieldPlaceholder: "Введите домены/IP через запятую или с новой строки",
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
	default:
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
		msg := "Невалидные записи:\n" + lists.FormatList(parsed.Invalid)
		_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{ChatID: chatID, Text: msg})
		return
	}
	if parsed.Mixed {
		a.logf(chatID, "list_input mixed_types valid_count=%d", len(parsed.Valid))
		_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
			ChatID: chatID,
			Text:   "Отправьте только домены или только IP/CIDR в одном сообщении.",
		})
		return
	}

	path := listPath(a.cfg, parsed.Type)
	label := typeLabel(parsed.Type)
	items := lists.FormatList(parsed.Valid)
	a.logf(chatID, "list_input accepted type=%s count=%d path=%q", label, len(parsed.Valid), path)

	msg := fmt.Sprintf("Получено (%s):\n%s", label, items)
	opID := a.sess.Create(chatID, ActionAdd, parsed.Type, path, parsed.Valid)

	rows := [][]models.InlineKeyboardButton{
		{
			{Text: "Добавить", CallbackData: cbPrefix + opID + ":add"},
			{Text: "Удалить", CallbackData: cbPrefix + opID + ":del"},
		},
		{
			{Text: "Отключить", CallbackData: cbPrefix + opID + ":dis"},
			{Text: "Отмена", CallbackData: cbPrefix + opID + ":cancel"},
		},
	}
	if rk := a.restartButtonRow(); rk != nil {
		rows = append(rows, rk)
	}
	if ik := a.integrationButtonRow(); ik != nil {
		rows = append(rows, ik)
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
			Text:            "Операция устарела. Отправьте список снова.",
		})
		return
	}
	a.logf(chatID, "callback op_kind=%d op_id=%s action=%s", op.Kind, opID, action)

	if action == "cancel" {
		a.sess.Delete(opID)
		a.answerAndEdit(ctx, b, update, "Операция отменена.")
		return
	}

	switch op.Kind {
	case ActionAdd:
		switch action {
		case "add":
			a.handleAdd(ctx, b, update, op)
		case "del":
			a.handleDelete(ctx, b, update, op)
		case "dis":
			a.handleDisablePrompt(ctx, b, update, op)
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
	case ActionRestart:
		a.handleRestart(ctx, b, update, opID)
	case ActionCheckIntegration:
		a.sess.Delete(opID)
		a.handleCheckIntegration(ctx, b, update)
	default:
		_, _ = b.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
		})
	}
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
		allID := a.sess.Create(op.ChatID, ActionAddAll, op.ListType, op.ListPath, op.Values)
		newID := a.sess.Create(op.ChatID, ActionAddNew, op.ListType, op.ListPath, op.Values)
		a.sess.Delete(op.ID)

		kb := &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{
				{{Text: "Добавить всё", CallbackData: cbPrefix + allID + ":confirm"}},
				{{Text: "Только новые", CallbackData: cbPrefix + newID + ":confirm"}},
				{{Text: "Отмена", CallbackData: cbPrefix + allID + ":cancel"}},
			},
		}
		a.answerAndEditMarkup(ctx, b, update, msg, kb)
		return
	}

	if len(newVals) == 0 {
		a.logf(op.ChatID, "add skipped_all_exist count=%d", len(op.Values))
		a.sess.Delete(op.ID)
		a.answerAndEdit(ctx, b, update, "Все записи уже есть в списке.")
		return
	}

	if err := lists.AddNew(op.ListPath, op.Values); err != nil {
		a.logf(op.ChatID, "add write_error path=%q err=%v", op.ListPath, err)
		a.answerAndEdit(ctx, b, update, "Ошибка записи: "+err.Error())
		return
	}
	a.logf(op.ChatID, "add success new_count=%d path=%q", len(newVals), op.ListPath)
	a.sess.Delete(op.ID)
	_ = a.svc.MarkFilesChanged()
	a.answerAndEdit(ctx, b, update, fmt.Sprintf("Добавлено (%d):\n%s", len(newVals), lists.FormatList(newVals)))
}

func (a *App) execAddAll(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	if err := lists.AddAll(op.ListPath, op.Values); err != nil {
		a.logf(op.ChatID, "add_all write_error path=%q err=%v", op.ListPath, err)
		a.answerAndEdit(ctx, b, update, "Ошибка записи: "+err.Error())
		return
	}
	a.logf(op.ChatID, "add_all success count=%d path=%q", len(op.Values), op.ListPath)
	a.sess.Delete(op.ID)
	_ = a.svc.MarkFilesChanged()
	a.answerAndEdit(ctx, b, update, "Записи добавлены/включены.")
}

func (a *App) execAddNew(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	if err := lists.AddNew(op.ListPath, op.Values); err != nil {
		a.logf(op.ChatID, "add_new write_error path=%q err=%v", op.ListPath, err)
		a.answerAndEdit(ctx, b, update, "Ошибка записи: "+err.Error())
		return
	}
	a.logf(op.ChatID, "add_new success count=%d path=%q", len(op.Values), op.ListPath)
	a.sess.Delete(op.ID)
	_ = a.svc.MarkFilesChanged()
	a.answerAndEdit(ctx, b, update, "Новые записи добавлены.")
}

func (a *App) handleDelete(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	if err := lists.Delete(op.ListPath, op.Values); err != nil {
		a.logf(op.ChatID, "delete write_error path=%q err=%v", op.ListPath, err)
		a.answerAndEdit(ctx, b, update, "Ошибка удаления: "+err.Error())
		return
	}
	a.logf(op.ChatID, "delete success count=%d path=%q", len(op.Values), op.ListPath)
	a.sess.Delete(op.ID)
	_ = a.svc.MarkFilesChanged()
	a.answerAndEdit(ctx, b, update, fmt.Sprintf("Удалено:\n%s", lists.FormatList(op.Values)))
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
		confirmID := a.sess.Create(op.ChatID, ActionDisableAddMissing, op.ListType, op.ListPath, op.Values)
		kb := &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{
				{{Text: "Добавить отключёнными", CallbackData: cbPrefix + confirmID + ":confirm"}},
				{{Text: "Отмена", CallbackData: cbPrefix + confirmID + ":cancel"}},
			},
		}
		a.answerAndEditMarkup(ctx, b, update, msg, kb)
		return
	}

	if len(active) == 0 {
		a.logf(op.ChatID, "disable skipped_already_disabled count=%d", len(op.Values))
		a.answerAndEdit(ctx, b, update, "Все записи уже отключены.")
		return
	}
	a.logf(op.ChatID, "disable confirm_only active=%d", len(active))

	confirmID := a.sess.Create(op.ChatID, ActionDisableConfirm, op.ListType, op.ListPath, op.Values)
	kb := &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: "Подтвердить", CallbackData: cbPrefix + confirmID + ":confirm"}},
			{{Text: "Отмена", CallbackData: cbPrefix + confirmID + ":cancel"}},
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
	_ = a.svc.MarkFilesChanged()
	a.answerAndEdit(ctx, b, update, "Записи отключены.")
}

func (a *App) execDisableWithMissing(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	if err := lists.Disable(op.ListPath, op.Values); err != nil {
		a.logf(op.ChatID, "disable_with_missing write_error path=%q err=%v", op.ListPath, err)
		a.answerAndEdit(ctx, b, update, "Ошибка: "+err.Error())
		return
	}
	a.logf(op.ChatID, "disable_with_missing success count=%d path=%q", len(op.Values), op.ListPath)
	a.sess.Delete(op.ID)
	_ = a.svc.MarkFilesChanged()
	a.answerAndEdit(ctx, b, update, "Записи отключены (включая добавленные).")
}

func (a *App) handleStartCreate(ctx context.Context, b *tgbot.Bot, update *models.Update, chatID int64) {
	if err := lists.CreateFiles(a.cfg.DomainList, a.cfg.IPList); err != nil {
		a.logf(chatID, "create_files_error domain=%q ip=%q err=%v", a.cfg.DomainList, a.cfg.IPList, err)
		a.answerAndEdit(ctx, b, update, "Ошибка создания файлов: "+err.Error())
		return
	}
	a.ready[chatID] = true
	a.logf(chatID, "create_files_success domain=%q ip=%q", a.cfg.DomainList, a.cfg.IPList)
	a.answerAndEdit(ctx, b, update, a.welcomeText())
}

func (a *App) handleRestart(ctx context.Context, b *tgbot.Bot, update *models.Update, opID string) {
	if a.cfg.RestartCmd == "" {
		a.logf(update.CallbackQuery.Message.Message.Chat.ID, "restart skipped reason=restart_cmd_empty")
		a.answerAndEdit(ctx, b, update, "Команда перезапуска не настроена.")
		return
	}

	chatID := update.CallbackQuery.Message.Message.Chat.ID
	messageID := update.CallbackQuery.Message.Message.ID

	_, _ = b.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
	})

	a.sess.Delete(opID)
	label := a.cfg.ServiceLabel
	a.logf(chatID, "restart started label=%q cmd=%q", label, a.cfg.RestartCmd)

	_, _ = b.EditMessageText(ctx, &tgbot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: messageID,
		Text:      fmt.Sprintf("Перезапуск %s…", label),
	})

	// Callback context can be short-lived; run restart in detached context
	// so the command is not cancelled right after handler returns.
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

	if res.Success {
		_ = a.svc.MarkRestarted()
		a.logf(chatID, "restart success duration_sec=%d", int(time.Since(start).Seconds()))
		text := fmt.Sprintf("✅ %s перезапущен (%ds).", label, int(time.Since(start).Seconds()))
		if isPodkopCommand(a.cfg.RestartCmd) {
			bindings, err := service.CheckPodkopBindings(rctx, a.cfg.DomainList, a.cfg.IPList)
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
		_, _ = b.EditMessageText(rctx, &tgbot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: messageID,
			Text:      text,
		})
		return
	}

	errText := "неизвестная ошибка"
	if res.Err != nil {
		errText = res.Err.Error()
	}
	a.logf(chatID, "restart failed err=%q", errText)
	text := fmt.Sprintf("❌ Ошибка перезапуска %s: %s", label, errText)
	if res.Output != "" {
		text += "\n\n" + strings.TrimSpace(res.Output)
	}
	_, _ = b.EditMessageText(rctx, &tgbot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: messageID,
		Text:      text,
	})
}

func isPodkopCommand(cmd string) bool {
	return strings.Contains(strings.ToLower(cmd), "podkop")
}

func (a *App) handleCheckIntegration(ctx context.Context, b *tgbot.Bot, update *models.Update) {
	if !isPodkopCommand(a.cfg.RestartCmd) {
		a.answerAndEdit(ctx, b, update, "Проверка интеграции доступна только для podkop.")
		return
	}
	st, err := service.CheckPodkopBindings(ctx, a.cfg.DomainList, a.cfg.IPList)
	if err != nil {
		a.answerAndEdit(ctx, b, update, "Не удалось проверить интеграцию Podkop: "+err.Error())
		return
	}

	domainLine := "❌ не подключен"
	if st.DomainBound {
		domainLine = "✅ подключен"
	}
	ipLine := "❌ не подключен"
	if st.IPBound {
		ipLine = "✅ подключен"
	}
	text := "Проверка интеграции Podkop:\n" +
		"• local_domain_lists: " + domainLine + "\n" +
		"• local_subnet_lists: " + ipLine

	if !st.DomainBound || !st.IPBound {
		text += "\n\nДобавьте пути списков в нужную секцию Podkop (Sections -> Local lists):\n" +
			"• " + a.cfg.DomainList + "\n" +
			"• " + a.cfg.IPList
	}

	a.answerAndEdit(ctx, b, update, text)
}

func (a *App) answerAndEdit(ctx context.Context, b *tgbot.Bot, update *models.Update, text string) {
	a.answerAndEditMarkup(ctx, b, update, text, nil)
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
	if rk := a.restartButtonRow(); rk != nil {
		if kb == nil {
			kb = &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{rk}}
		} else {
			kb.InlineKeyboard = append(kb.InlineKeyboard, rk)
		}
	}
	if ik := a.integrationButtonRow(); ik != nil {
		if kb == nil {
			kb = &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{ik}}
		} else {
			kb.InlineKeyboard = append(kb.InlineKeyboard, ik)
		}
	}
	if kb != nil {
		params.ReplyMarkup = kb
	}
	_, _ = b.EditMessageText(ctx, params)
}
