package bot

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"lst-signbox-lists-tgbot/internal/podkop"
)

const (
	btnSavedFiles      = "📂 Сохранённые файлы"
	savedFilesPageSize = 8
)

func (a *App) savedFiles() ([]podkop.SavedListFile, error) {
	if a.readSavedFiles != nil {
		return a.readSavedFiles()
	}
	return podkop.SavedListFiles(listsDir)
}

func (a *App) findSavedFile(token string) (podkop.SavedListFile, error) {
	files, err := a.savedFiles()
	if err != nil {
		return podkop.SavedListFile{}, err
	}
	for _, file := range files {
		if pathToken(file.Path) == token {
			return file, nil
		}
	}
	return podkop.SavedListFile{}, fmt.Errorf("файл больше недоступен — откройте список файлов заново")
}

func (a *App) showSavedFiles(ctx context.Context, b *tgbot.Bot, reply pickReply, op *PendingOp, page int) {
	files, err := a.savedFiles()
	if err != nil {
		a.logf(reply.chatID, "saved_files read_error err=%v", err)
		reply.send(ctx, b, "❌ Не удалось открыть сохранённые файлы: "+err.Error(), a.backToSectionsInlineKeyboard())
		return
	}
	if op != nil {
		files = slices.Clone(files)
		files = slices.DeleteFunc(files, func(file podkop.SavedListFile) bool { return file.Type != op.ListType })
	}
	pages := max(1, (len(files)+savedFilesPageSize-1)/savedFilesPageSize)
	page = max(0, min(page, pages-1))
	start := page * savedFilesPageSize
	end := min(start+savedFilesPageSize, len(files))

	text := "📂 Сохранённые файлы\n" + listsDir
	if op != nil {
		text += fmt.Sprintf("\n\nВыберите файл (%s) для секции «%s».", typeLabel(op.ListType), op.Section)
	} else {
		text += "\n\nВыберите файл, чтобы привязать его к секции."
	}
	if len(files) == 0 {
		text += "\n\nПодходящих файлов .lst нет."
	}
	if pages > 1 {
		text += fmt.Sprintf("\n\nСтраница %d из %d", page+1, pages)
	}

	callback := func(action string) string {
		if op != nil {
			return cbPrefix + op.ID + ":" + action
		}
		return menuCbPrefix + action
	}
	var rows [][]models.InlineKeyboardButton
	for _, file := range files[start:end] {
		text += fmt.Sprintf("\n\n📄 %s\n%s · %d ✅ / %d ⏸", file.Path, typeLabel(file.Type), file.Active, file.Disabled)
		rows = append(rows, []models.InlineKeyboardButton{{
			Text: "📄 " + fileLabel(file.Path), CallbackData: callback("savedfile_" + pathToken(file.Path)),
		}})
	}
	var nav []models.InlineKeyboardButton
	if page > 0 {
		nav = append(nav, models.InlineKeyboardButton{Text: "⬅️ Назад", CallbackData: callback(fmt.Sprintf("savedpage_%d", page-1))})
	}
	if page+1 < pages {
		nav = append(nav, models.InlineKeyboardButton{Text: "Далее ➡️", CallbackData: callback(fmt.Sprintf("savedpage_%d", page+1))})
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	if op != nil {
		rows = append(rows, []models.InlineKeyboardButton{
			{Text: "⬅️ К привязке", CallbackData: callback("bindback")},
			{Text: btnCancel, CallbackData: callback("cancel")},
		})
	} else {
		rows = append(rows, a.backToSectionsInlineKeyboard().InlineKeyboard...)
	}
	reply.send(ctx, b, text, &models.InlineKeyboardMarkup{InlineKeyboard: rows})
}

func (a *App) handleSavedFilesAction(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp, action string) bool {
	chatID := update.CallbackQuery.Message.Message.Chat.ID
	if raw, ok := strings.CutPrefix(action, "savedpage_"); ok {
		if page, err := strconv.Atoi(raw); err == nil {
			a.showSavedFiles(ctx, b, a.replyTo(update, chatID), op, page)
		}
		return true
	}
	if token, ok := strings.CutPrefix(action, "savedfile_"); ok {
		if op != nil {
			a.pickSavedFile(ctx, b, update, *op, token)
		} else {
			a.showSavedFileSections(ctx, b, update, token)
		}
		return true
	}
	return false
}

func (a *App) showSavedFileSections(ctx context.Context, b *tgbot.Bot, update *models.Update, token string) {
	file, err := a.findSavedFile(token)
	if err != nil {
		a.answerAndEditMarkup(ctx, b, update, "❌ "+err.Error(), a.backToSectionsInlineKeyboard())
		return
	}
	text := fmt.Sprintf("📄 %s\n%s · %d ✅ / %d ⏸", file.Path, typeLabel(file.Type), file.Active, file.Disabled)
	var rows [][]models.InlineKeyboardButton
	var bound []string
	for _, sec := range a.sections(ctx) {
		if slices.Contains(sec.Lists(file.Type), file.Path) {
			bound = append(bound, sec.Name)
			continue
		}
		rows = append(rows, []models.InlineKeyboardButton{{
			Text: "🗂 " + sec.Name, CallbackData: targetCallback(verbSavedBind, file.Type, sectionToken(sec.Name), token),
		}})
	}
	if len(bound) > 0 {
		text += "\n\nУже привязан: " + strings.Join(bound, ", ")
	}
	if len(rows) == 0 {
		text += "\n\nНет секций, к которым можно привязать этот файл."
	} else {
		text += "\n\nК какой секции привязать?"
	}
	rows = append(rows, []models.InlineKeyboardButton{{Text: btnSavedFiles, CallbackData: menuCbPrefix + "savedpage_0"}})
	rows = append(rows, a.backToSectionsInlineKeyboard().InlineKeyboard...)
	a.answerAndEditMarkup(ctx, b, update, text, &models.InlineKeyboardMarkup{InlineKeyboard: rows})
}

func (a *App) startSavedBind(ctx context.Context, b *tgbot.Bot, update *models.Update, act targetAction) {
	s, err := a.findSection(ctx, act.secTok)
	if err != nil {
		a.answerAndEditMarkup(ctx, b, update, targetError(err), a.backToSectionsInlineKeyboard())
		return
	}
	a.pickSavedFile(ctx, b, update, PendingOp{
		ChatID: update.CallbackQuery.Message.Message.Chat.ID, Section: s.Name, ListType: act.typ,
	}, act.fileTok)
}

func (a *App) pickSavedFile(ctx context.Context, b *tgbot.Bot, update *models.Update, base PendingOp, token string) {
	file, err := a.findSavedFile(token)
	if err != nil {
		a.answerAndEditMarkup(ctx, b, update, "❌ "+err.Error(), a.backToSectionsInlineKeyboard())
		return
	}
	if file.Type != base.ListType {
		a.answerAndEditMarkup(ctx, b, update, "❌ Тип файла не подходит. Откройте список файлов заново.",
			a.backToSectionInlineKeyboard(sectionToken(base.Section)))
		return
	}
	a.sess.Delete(base.ID)
	base.ExistingFile = true
	a.showBindConfirm(ctx, b, a.replyTo(update, base.ChatID), base, file.Path)
}
