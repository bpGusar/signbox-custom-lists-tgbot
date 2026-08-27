package bot

import (
	"context"
	"fmt"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// Picks that choose where a pending list of entries should land. They travel
// in the session namespace, because the entries themselves live in the
// session and a pick is meaningless without them.
const (
	cbSectionPrefix = "sec_"
	cbFilePrefix    = "fl_"
)

// askAddTarget asks which section — and which of its files — the entries just
// sent should go into. With a single candidate there is nothing to ask, and
// the target is spelled out on the action screen anyway.
func (a *App) askAddTarget(ctx context.Context, b *tgbot.Bot, reply pickReply, op *PendingOp) {
	secs := a.sectionsWith(ctx, op.ListType)
	if len(secs) == 0 {
		a.logf(reply.chatID, "add_target none_bound type=%s", typeLabel(op.ListType))
		a.sess.Delete(op.ID)
		reply.send(ctx, b, fmt.Sprintf(
			"🔗 Ни в одной секции podkop не привязан список (%s).\n\nОткройте «%s», выберите секцию и привяжите файл.",
			typeLabel(op.ListType), btnManage), a.backToSectionsInlineKeyboard())
		return
	}
	if len(secs) == 1 {
		if paths := secs[0].Lists(op.ListType); len(paths) == 1 {
			a.showAddActions(ctx, b, reply, op, listTarget{Section: secs[0].Name, Type: op.ListType, Path: paths[0]})
			return
		}
	}

	var rows [][]models.InlineKeyboardButton
	for _, s := range secs {
		label := sectionDisplayName(s.Name)
		if n := len(s.Lists(op.ListType)); n > 1 {
			label += fmt.Sprintf(" · %d файла", n)
		}
		rows = append(rows, []models.InlineKeyboardButton{{
			Text:         "🗂 " + label,
			CallbackData: cbPrefix + op.ID + ":" + cbSectionPrefix + sectionToken(s.Name),
		}})
	}
	rows = append(rows, []models.InlineKeyboardButton{{
		Text: btnCancel, CallbackData: cbPrefix + op.ID + ":cancel",
	}})

	text := fmt.Sprintf("📥 %s — %s\n\nВ какую секцию добавить?", typeLabel(op.ListType), pluralEntries(len(op.Values)))
	reply.send(ctx, b, a.withBanner(text), &models.InlineKeyboardMarkup{InlineKeyboard: rows})
}

// handleAddSectionPick narrows a pending add down to one section, asking for
// the file too when the section is fed from several.
func (a *App) handleAddSectionPick(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp, token string) {
	chatID := update.CallbackQuery.Message.Message.Chat.ID

	s, err := a.findSection(ctx, token)
	if err != nil {
		a.answerAndEditMarkup(ctx, b, update, targetError(err), a.backToMainMenuInlineKeyboard())
		return
	}
	paths := s.Lists(op.ListType)
	if len(paths) == 0 {
		a.answerAndEditMarkup(ctx, b, update, targetError(errNotBound), a.backToSectionsInlineKeyboard())
		return
	}
	a.logf(chatID, "add_target section=%q files=%d", s.Name, len(paths))

	if len(paths) == 1 {
		a.showAddActions(ctx, b, a.replyTo(update, chatID), op, listTarget{Section: s.Name, Type: op.ListType, Path: paths[0]})
		return
	}

	// The section travels in a fresh operation, so the file buttons below know
	// which section they belong to.
	next := a.reissue(op, func(o *PendingOp) { o.Section = s.Name })

	var rows [][]models.InlineKeyboardButton
	for _, p := range paths {
		rows = append(rows, []models.InlineKeyboardButton{{
			Text:         "📄 " + fileLabel(p),
			CallbackData: cbPrefix + next.ID + ":" + cbFilePrefix + pathToken(p),
		}})
	}
	rows = append(rows, []models.InlineKeyboardButton{{
		Text: btnCancel, CallbackData: cbPrefix + next.ID + ":cancel",
	}})

	text := fmt.Sprintf("📄 В секции «%s» несколько файлов (%s). В какой добавить?\n\n%s",
		sectionDisplayName(s.Name), typeLabel(op.ListType), strings.Join(paths, "\n"))
	a.answerAndEditMarkup(ctx, b, update, text, &models.InlineKeyboardMarkup{InlineKeyboard: rows})
}

// handleAddFilePick applies the file picked inside a section.
func (a *App) handleAddFilePick(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp, token string) {
	chatID := update.CallbackQuery.Message.Message.Chat.ID

	tgt, err := a.resolveTarget(ctx, sectionToken(op.Section), token, op.ListType)
	if err != nil {
		a.answerAndEditMarkup(ctx, b, update, targetError(err), a.backToSectionsInlineKeyboard())
		return
	}
	a.showAddActions(ctx, b, a.replyTo(update, chatID), op, tgt)
}

// showAddActions is the screen the list input has always ended at — add,
// enable, disable, delete — now bound to one specific file.
func (a *App) showAddActions(ctx context.Context, b *tgbot.Bot, reply pickReply, op *PendingOp, tgt listTarget) {
	chatID := reply.chatID

	validNew, validActive, validDisabled, err := classifyBuckets(tgt.Path, op.Values)
	if err != nil {
		a.logf(chatID, "list_input classify_error path=%q err=%v", tgt.Path, err)
		a.sess.Delete(op.ID)
		reply.send(ctx, b, "❌ Ошибка чтения файла: "+err.Error(), a.backToSectionsInlineKeyboard())
		return
	}

	next := a.reissue(op, func(o *PendingOp) {
		o.Section = tgt.Section
		o.ListPath = tgt.Path
	})
	a.logf(chatID, "list_input target section=%q path=%q new=%d active=%d disabled=%d",
		tgt.Section, tgt.Path, len(validNew), len(validActive), len(validDisabled))

	msg := targetLine(tgt) + "\n\n" +
		buildListInputMessage(typeLabel(tgt.Type), op.Values, validNew, validActive, validDisabled)
	if others := a.sharedWith(ctx, tgt); len(others) > 0 {
		msg += "\n\nℹ️ Этот файл используют и другие секции: " + strings.Join(others, ", ")
	}

	rows, hasActions := buildListInputKeyboard(next.ID, validNew, validActive, validDisabled)
	if !hasActions {
		msg += "\n\nℹ️ Нечего делать — все записи уже в нужном состоянии."
	}
	reply.send(ctx, b, a.withBanner(msg), &models.InlineKeyboardMarkup{InlineKeyboard: rows})
}

// reissue stores op again with a field or two changed. Operations are handed
// out as ids that buttons carry, so a step forward is a new id rather than an
// edit of the old one — a stale button then resolves to nothing instead of
// acting on a target the user has since moved past.
func (a *App) reissue(op *PendingOp, apply func(*PendingOp)) *PendingOp {
	copied := *op
	copied.ID = ""
	apply(&copied)
	id := a.sess.Create(copied)
	a.sess.Delete(op.ID)
	next, _ := a.sess.Get(id)
	return next
}

// withBanner prepends the "service not restarted yet" notice, the way the
// callback edit helper does on its own.
func (a *App) withBanner(text string) string {
	banner := a.svc.StaleBanner()
	if banner == "" || strings.Contains(text, "⚠️") {
		return text
	}
	text = banner + "\n\n" + text
	if reason := a.restartHiddenReason(); reason != "" {
		text += "\n\n" + reason
	}
	return text
}
