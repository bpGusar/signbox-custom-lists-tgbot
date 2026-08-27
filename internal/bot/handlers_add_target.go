package bot

import (
	"context"
	"fmt"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"lst-signbox-lists-tgbot/internal/lists"
	"lst-signbox-lists-tgbot/internal/podkop"
)

// Picks that choose where a pending list of entries should land. They travel
// in the session namespace, because the entries themselves live in the
// session and a pick is meaningless without them.
const (
	cbSectionPrefix = "sec_"
	cbFilePrefix    = "fl_"
)

// askAddTarget asks which podkop section the entries just sent should go
// into. The question is always asked when there are sections to choose
// between, even if only one of them has a file today: the point of the screen
// is to decide where the entries land, and a section without a file can get
// one on the spot.
func (a *App) askAddTarget(ctx context.Context, b *tgbot.Bot, reply pickReply, op *PendingOp) {
	secs := a.sections(ctx)

	// Without a podkop config there is one synthetic section and nothing to
	// pick between, so the question would be noise.
	if len(secs) == 1 && secs[0].Name == "" {
		paths := secs[0].Lists(op.ListType)
		if len(paths) == 1 {
			a.showAddActions(ctx, b, reply, op, listTarget{Type: op.ListType, Path: paths[0]}, "")
			return
		}
		if len(paths) == 0 {
			a.logf(reply.chatID, "add_target no_file type=%s", typeLabel(op.ListType))
			a.sess.Delete(op.ID)
			reply.send(ctx, b, fmt.Sprintf("❌ Файл для списка (%s) не настроен.", typeLabel(op.ListType)),
				a.backToMainMenuInlineKeyboard())
			return
		}
	}

	rows := addTargetRows(secs, op.ListType, op.ID)
	if len(rows) == 0 {
		a.logf(reply.chatID, "add_target no_sections type=%s", typeLabel(op.ListType))
		a.sess.Delete(op.ID)
		reply.send(ctx, b, "🔗 В конфиге podkop нет секций, куда можно добавить записи.",
			a.backToMainMenuInlineKeyboard())
		return
	}
	rows = append(rows, []models.InlineKeyboardButton{{
		Text: btnCancel, CallbackData: cbPrefix + op.ID + ":cancel",
	}})

	text := fmt.Sprintf("📥 %s — %s\n\nВ какую секцию добавить?", typeLabel(op.ListType), pluralEntries(len(op.Values)))
	reply.send(ctx, b, a.withBanner(text), &models.InlineKeyboardMarkup{InlineKeyboard: rows})
}

// addTargetRows lays the sections out for the "куда добавить?" screen. A
// section with no file for this list is offered too, marked as such: picking
// it binds a file and comes back to the same entries.
func addTargetRows(secs []podkop.Section, t lists.EntryType, opID string) [][]models.InlineKeyboardButton {
	var rows [][]models.InlineKeyboardButton
	for _, s := range secs {
		label := "🗂 " + sectionDisplayName(s.Name)
		switch n := len(s.Lists(t)); {
		case n == 0:
			// The fallback pair is not in podkop's config, so it cannot be
			// given a file here.
			if s.Name == "" {
				continue
			}
			label = "🔗 " + s.Name + " · нет файла"
		case n > 1:
			label += fmt.Sprintf(" · %d файла", n)
		}
		rows = append(rows, []models.InlineKeyboardButton{{
			Text:         label,
			CallbackData: cbPrefix + opID + ":" + cbSectionPrefix + sectionToken(s.Name),
		}})
	}
	return rows
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
	a.logf(chatID, "add_target section=%q files=%d", s.Name, len(paths))

	if len(paths) == 0 {
		if s.Name == "" {
			a.answerAndEditMarkup(ctx, b, update, targetError(errNotBound), a.backToSectionsInlineKeyboard())
			return
		}
		// The entries ride along, so the flow returns to them once the file
		// exists instead of making the user paste the list again.
		a.sess.Delete(op.ID)
		a.showBindConfirm(ctx, b, a.replyTo(update, chatID), PendingOp{
			ChatID:   chatID,
			Section:  s.Name,
			ListType: op.ListType,
			Values:   op.Values,
		}, a.suggestBindPath(s.Name, op.ListType))
		return
	}

	if len(paths) == 1 {
		a.showAddActions(ctx, b, a.replyTo(update, chatID), op, listTarget{Section: s.Name, Type: op.ListType, Path: paths[0]}, "")
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
	a.showAddActions(ctx, b, a.replyTo(update, chatID), op, tgt, "")
}

// showAddActions is the screen the list input has always ended at — add,
// enable, disable, delete — now bound to one specific file.
func (a *App) showAddActions(ctx context.Context, b *tgbot.Bot, reply pickReply, op *PendingOp, tgt listTarget, note string) {
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

	msg := note + targetLine(tgt) + "\n\n" +
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
