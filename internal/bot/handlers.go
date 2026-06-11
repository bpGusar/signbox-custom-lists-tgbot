package bot

import (
	"context"
	"fmt"
	"strings"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"lists-tg/internal/lists"
	"lists-tg/internal/service"
)

const cbPrefix = "s:"

func (a *App) handleStart(ctx context.Context, b *tgbot.Bot, update *models.Update) {
	chatID := update.Message.Chat.ID
	a.sendStartCheck(ctx, b, chatID)
}

func (a *App) sendStartCheck(ctx context.Context, b *tgbot.Bot, chatID int64) {
	missing := lists.MissingFiles(a.cfg.DomainList, a.cfg.IPList)
	if len(missing) > 0 {
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
	text := a.welcomeText()
	_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID:      chatID,
		Text:        text,
		ReplyMarkup: a.staleKeyboard(),
	})
}

func (a *App) welcomeText() string {
	text := "Бот готов к работе.\n\n" +
		"Отправьте список доменов или IP/CIDR через запятую.\n" +
		"Бот определит тип и предложит действия.\n\n" +
		fmt.Sprintf("Домены: %s\nIP: %s", a.cfg.DomainList, a.cfg.IPList)

	if banner := a.svc.StaleBanner(); banner != "" {
		text = banner + "\n\n" + text
	}
	return text
}

func (a *App) staleKeyboard() *models.InlineKeyboardMarkup {
	if row := a.restartButtonRow(); row != nil {
		return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{row}}
	}
	return nil
}

func (a *App) restartButtonRow() []models.InlineKeyboardButton {
	if a.cfg.RestartCmd == "" {
		return nil
	}
	st, _ := a.svc.Load()
	if st == nil || !st.ServiceStale {
		return nil
	}
	label := "Перезапустить " + a.cfg.ServiceLabel
	id := a.sess.Create(0, ActionRestart, 0, "", nil)
	return []models.InlineKeyboardButton{{Text: label, CallbackData: cbPrefix + id}}
}

func (a *App) handleListInput(ctx context.Context, b *tgbot.Bot, update *models.Update) {
	chatID := update.Message.Chat.ID
	text := update.Message.Text

	parsed := lists.ParseInput(text)
	if parsed.Empty {
		return
	}
	if len(parsed.Invalid) > 0 {
		msg := "Невалидные записи:\n" + lists.FormatList(parsed.Invalid)
		_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{ChatID: chatID, Text: msg})
		return
	}
	if parsed.Mixed {
		_, _ = b.SendMessage(ctx, &tgbot.SendMessageParams{
			ChatID: chatID,
			Text:   "Отправьте только домены или только IP/CIDR в одном сообщении.",
		})
		return
	}

	path := listPath(a.cfg, parsed.Type)
	label := typeLabel(parsed.Type)
	items := lists.FormatList(parsed.Valid)

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
	kb := &models.InlineKeyboardMarkup{InlineKeyboard: rows}

	reply := msg
	if banner := a.svc.StaleBanner(); banner != "" {
		reply = banner + "\n\n" + msg
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
		_, _ = b.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:          "Операция устарела. Отправьте список снова.",
		})
		return
	}

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
	default:
		_, _ = b.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
		})
	}
}

func (a *App) handleAdd(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	classified, err := lists.ClassifyValues(op.ListPath, op.Values)
	if err != nil {
		a.answerAndEdit(ctx, b, update, "Ошибка чтения файла: "+err.Error())
		return
	}

	newVals, active, disabled := lists.GroupByStatus(classified)

	if len(disabled) > 0 {
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
		a.sess.Delete(op.ID)
		a.answerAndEdit(ctx, b, update, "Все записи уже есть в списке.")
		return
	}

	if err := lists.AddNew(op.ListPath, op.Values); err != nil {
		a.answerAndEdit(ctx, b, update, "Ошибка записи: "+err.Error())
		return
	}
	a.sess.Delete(op.ID)
	_ = a.svc.MarkFilesChanged()
	a.answerAndEdit(ctx, b, update, fmt.Sprintf("Добавлено (%d):\n%s", len(newVals), lists.FormatList(newVals)))
}

func (a *App) execAddAll(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	if err := lists.AddAll(op.ListPath, op.Values); err != nil {
		a.answerAndEdit(ctx, b, update, "Ошибка записи: "+err.Error())
		return
	}
	a.sess.Delete(op.ID)
	_ = a.svc.MarkFilesChanged()
	a.answerAndEdit(ctx, b, update, "Записи добавлены/включены.")
}

func (a *App) execAddNew(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	if err := lists.AddNew(op.ListPath, op.Values); err != nil {
		a.answerAndEdit(ctx, b, update, "Ошибка записи: "+err.Error())
		return
	}
	a.sess.Delete(op.ID)
	_ = a.svc.MarkFilesChanged()
	a.answerAndEdit(ctx, b, update, "Новые записи добавлены.")
}

func (a *App) handleDelete(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	if err := lists.Delete(op.ListPath, op.Values); err != nil {
		a.answerAndEdit(ctx, b, update, "Ошибка удаления: "+err.Error())
		return
	}
	a.sess.Delete(op.ID)
	_ = a.svc.MarkFilesChanged()
	a.answerAndEdit(ctx, b, update, fmt.Sprintf("Удалено:\n%s", lists.FormatList(op.Values)))
}

func (a *App) handleDisablePrompt(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	classified, err := lists.ClassifyValues(op.ListPath, op.Values)
	if err != nil {
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
		a.answerAndEdit(ctx, b, update, "Все записи уже отключены.")
		return
	}

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
		a.answerAndEdit(ctx, b, update, "Ошибка: "+err.Error())
		return
	}
	a.sess.Delete(op.ID)
	_ = a.svc.MarkFilesChanged()
	a.answerAndEdit(ctx, b, update, "Записи отключены.")
}

func (a *App) execDisableWithMissing(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	if err := lists.Disable(op.ListPath, op.Values); err != nil {
		a.answerAndEdit(ctx, b, update, "Ошибка: "+err.Error())
		return
	}
	a.sess.Delete(op.ID)
	_ = a.svc.MarkFilesChanged()
	a.answerAndEdit(ctx, b, update, "Записи отключены (включая добавленные).")
}

func (a *App) handleStartCreate(ctx context.Context, b *tgbot.Bot, update *models.Update, chatID int64) {
	if err := lists.CreateFiles(a.cfg.DomainList, a.cfg.IPList); err != nil {
		a.answerAndEdit(ctx, b, update, "Ошибка создания файлов: "+err.Error())
		return
	}
	a.ready[chatID] = true
	a.answerAndEdit(ctx, b, update, a.welcomeText())
}

func (a *App) handleRestart(ctx context.Context, b *tgbot.Bot, update *models.Update, opID string) {
	if a.cfg.RestartCmd == "" {
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

	_, _ = b.EditMessageText(ctx, &tgbot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: messageID,
		Text:      fmt.Sprintf("Перезапуск %s…", label),
	})

	rctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	start := time.Now()
	res := service.RunRestartWithProgress(rctx, a.cfg.RestartCmd, func(elapsed time.Duration) {
		_, _ = b.EditMessageText(ctx, &tgbot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: messageID,
			Text:      fmt.Sprintf("Перезапуск %s… (%ds)", label, int(elapsed.Seconds())),
		})
	})

	if res.Success {
		_ = a.svc.MarkRestarted()
		text := fmt.Sprintf("✅ %s перезапущен (%ds).", label, int(time.Since(start).Seconds()))
		if res.Output != "" {
			text += "\n\n" + strings.TrimSpace(res.Output)
		}
		_, _ = b.EditMessageText(ctx, &tgbot.EditMessageTextParams{
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
	text := fmt.Sprintf("❌ Ошибка перезапуска %s: %s", label, errText)
	if res.Output != "" {
		text += "\n\n" + strings.TrimSpace(res.Output)
	}
	_, _ = b.EditMessageText(ctx, &tgbot.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: messageID,
		Text:      text,
	})
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
