package bot

import (
	"context"
	"fmt"
	pathpkg "path"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"lst-signbox-lists-tgbot/internal/lists"
	"lst-signbox-lists-tgbot/internal/podkop"
)

const (
	btnManage      = "🗂 Управление записями"
	btnBackToSecs  = "⬅️ К секциям"
	btnBackToSec   = "⬅️ К секции"
	btnCustomPath  = "✍️ Свой путь"
	btnBindConfirm = "✅ Привязать"
)

// Verbs of the callbacks that carry a target. Every one of them is followed by
// "_<list type>_<section>", and all but bind by an optional "_<file>", because
// a section can hold several files and a button must say which one it meant.
const (
	verbDownload = "dl"
	verbView     = "view"
	verbViewAll  = "all"
	verbViewCats = "cats"
	verbViewCat  = "cat"
	verbBind     = "bind"
)

// targetAction is a menu callback that points at a list inside a section.
type targetAction struct {
	verb    string
	typ     lists.EntryType
	secTok  string
	fileTok string
	// rest is whatever the verb adds after the file, currently a category.
	rest string
}

func targetCallback(verb string, t lists.EntryType, secTok, fileTok string) string {
	cb := menuCbPrefix + verb + "_" + listTypeToken(t) + "_" + secTok
	if fileTok != "" {
		cb += "_" + fileTok
	}
	return cb
}

func parseTargetAction(action string) (targetAction, bool) {
	parts := strings.Split(action, "_")
	if len(parts) < 3 || len(parts) > 5 {
		return targetAction{}, false
	}
	t, ok := listTypeFromToken(parts[1])
	if !ok {
		return targetAction{}, false
	}
	act := targetAction{verb: parts[0], typ: t, secTok: parts[2]}
	if len(parts) > 3 {
		act.fileTok = parts[3]
	}
	if len(parts) > 4 {
		act.rest = parts[4]
	}
	switch act.verb {
	case verbDownload, verbView, verbViewAll, verbViewCats, verbViewCat, verbBind:
		return act, true
	}
	return targetAction{}, false
}

// showSections is the first screen behind "Управление записями".
func (a *App) showSections(ctx context.Context, b *tgbot.Bot, update *models.Update) {
	secs := a.sections(ctx)

	var rows [][]models.InlineKeyboardButton
	for _, s := range secs {
		rows = append(rows, []models.InlineKeyboardButton{{
			Text:         sectionButtonLabel(s),
			CallbackData: menuCbPrefix + "sec_" + sectionToken(s.Name),
		}})
	}
	rows = append(rows, []models.InlineKeyboardButton{{
		Text: menuBtnMainMenu, CallbackData: menuCbPrefix + "main_menu",
	}})

	text := "🗂 Секции Podkop\n\nВыберите секцию — дальше просмотр и выгрузка её списков."
	if len(secs) == 1 && secs[0].Name == "" {
		text = "🗂 Списки\n\nКонфиг podkop прочитать не удалось, поэтому доступна только пара файлов из настроек бота."
	}
	a.answerAndEditMarkup(ctx, b, update, text, &models.InlineKeyboardMarkup{InlineKeyboard: rows})
}

// showSectionCard is the screen where the four actions live.
func (a *App) showSectionCard(ctx context.Context, b *tgbot.Bot, update *models.Update, secTok string) {
	s, err := a.findSection(ctx, secTok)
	if err != nil {
		a.answerAndEditMarkup(ctx, b, update, targetError(err), a.backToSectionsInlineKeyboard())
		return
	}

	head := "🗂 " + sectionDisplayName(s.Name)
	if s.ConnectionType != "" {
		head += " · " + s.ConnectionType
	}
	text := head + "\n\n" +
		sectionBindingLine(s, lists.TypeDomain) + "\n" +
		sectionBindingLine(s, lists.TypeIP)

	var rows [][]models.InlineKeyboardButton
	for _, t := range []lists.EntryType{lists.TypeDomain, lists.TypeIP} {
		rows = append(rows, a.sectionActionRows(s, t, secTok)...)
	}
	rows = append(rows, []models.InlineKeyboardButton{
		{Text: btnBackToSecs, CallbackData: menuCbPrefix + "manage"},
		{Text: menuBtnMainMenu, CallbackData: menuCbPrefix + "main_menu"},
	})
	a.answerAndEditMarkup(ctx, b, update, text, &models.InlineKeyboardMarkup{InlineKeyboard: rows})
}

// sectionActionRows is either the pair of buttons for a bound list or the
// offer to bind one.
func (a *App) sectionActionRows(s podkop.Section, t lists.EntryType, secTok string) [][]models.InlineKeyboardButton {
	kind := "домены"
	if t == lists.TypeIP {
		kind = "IP"
	}
	if len(s.Lists(t)) == 0 {
		// The fallback section is not in podkop's config, so there is nothing
		// there to bind a file to.
		if s.Name == "" {
			return nil
		}
		return [][]models.InlineKeyboardButton{{{
			Text:         "🔗 Привязать файл " + kind,
			CallbackData: targetCallback(verbBind, t, secTok, ""),
		}}}
	}
	return [][]models.InlineKeyboardButton{{
		{Text: "📋 Показать " + kind, CallbackData: targetCallback(verbView, t, secTok, "")},
		{Text: "📥 Скачать " + kind, CallbackData: targetCallback(verbDownload, t, secTok, "")},
	}}
}

func (a *App) backToSectionsInlineKeyboard() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: btnBackToSecs, CallbackData: menuCbPrefix + "manage"}},
			{{Text: menuBtnMainMenu, CallbackData: menuCbPrefix + "main_menu"}},
		},
	}
}

func (a *App) backToSectionInlineKeyboard(secTok string) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: btnBackToSec, CallbackData: menuCbPrefix + "sec_" + secTok}},
			{{Text: menuBtnMainMenu, CallbackData: menuCbPrefix + "main_menu"}},
		},
	}
}

// handleTargetAction runs a callback that points at a list. A verb arriving
// without a file first asks which file it meant, unless the section has only
// one — then there is nothing to ask.
func (a *App) handleTargetAction(ctx context.Context, b *tgbot.Bot, update *models.Update, act targetAction) {
	chatID := update.CallbackQuery.Message.Message.Chat.ID

	if act.verb == verbBind {
		a.startBind(ctx, b, update, act)
		return
	}

	if act.fileTok == "" {
		s, err := a.findSection(ctx, act.secTok)
		if err != nil {
			a.answerAndEditMarkup(ctx, b, update, targetError(err), a.backToSectionsInlineKeyboard())
			return
		}
		if paths := s.Lists(act.typ); len(paths) > 1 {
			a.showFilePicker(ctx, b, update, act, paths)
			return
		}
	}

	tgt, err := a.resolveTarget(ctx, act.secTok, act.fileTok, act.typ)
	if err != nil {
		a.logf(chatID, "target_resolve_error verb=%s sec=%s file=%s err=%v", act.verb, act.secTok, act.fileTok, err)
		a.answerAndEditMarkup(ctx, b, update, targetError(err), a.backToSectionsInlineKeyboard())
		return
	}

	switch act.verb {
	case verbDownload:
		a.logf(chatID, "menu download section=%q type=%s path=%q", tgt.Section, typeLabel(tgt.Type), tgt.Path)
		a.sendListFile(ctx, b, chatID, tgt)
	case verbView:
		a.logf(chatID, "menu view section=%q type=%s path=%q", tgt.Section, typeLabel(tgt.Type), tgt.Path)
		a.showListMenu(ctx, b, update, tgt)
	case verbViewAll:
		a.logf(chatID, "menu view_all section=%q type=%s", tgt.Section, typeLabel(tgt.Type))
		a.sendFullList(ctx, b, update, tgt)
	case verbViewCats:
		a.logf(chatID, "menu view_cats section=%q type=%s", tgt.Section, typeLabel(tgt.Type))
		a.sendCategoryPicker(ctx, b, update, tgt)
	case verbViewCat:
		a.logf(chatID, "menu view_category section=%q type=%s token=%s", tgt.Section, typeLabel(tgt.Type), act.rest)
		a.openCategoryByToken(ctx, b, a.replyTo(update, chatID), tgt, act.rest)
	}
}

// showFilePicker asks which of a section's files the action meant.
func (a *App) showFilePicker(ctx context.Context, b *tgbot.Bot, update *models.Update, act targetAction, paths []string) {
	var rows [][]models.InlineKeyboardButton
	for _, p := range paths {
		rows = append(rows, []models.InlineKeyboardButton{{
			Text:         "📄 " + fileLabel(p),
			CallbackData: targetCallback(act.verb, act.typ, act.secTok, pathToken(p)),
		}})
	}
	rows = append(rows, []models.InlineKeyboardButton{
		{Text: btnBackToSec, CallbackData: menuCbPrefix + "sec_" + act.secTok},
		{Text: menuBtnMainMenu, CallbackData: menuCbPrefix + "main_menu"},
	})

	text := fmt.Sprintf("📄 В секции несколько файлов (%s). Выберите нужный:\n\n%s",
		typeLabel(act.typ), strings.Join(paths, "\n"))
	a.answerAndEditMarkup(ctx, b, update, text, &models.InlineKeyboardMarkup{InlineKeyboard: rows})
}

// suggestBindPath keeps a new file next to the ones the bot already manages,
// named after the section it belongs to.
func (a *App) suggestBindPath(sectionName string, t lists.EntryType) string {
	base := a.cfg.DomainList
	if t == lists.TypeIP {
		base = a.cfg.IPList
	}
	if base == "" {
		base = "/etc/lst-signbox-lists-tgbot/domain_list.lst"
		if t == lists.TypeIP {
			base = "/etc/lst-signbox-lists-tgbot/ip_list.lst"
		}
	}
	if sectionName == "" {
		return base
	}
	return pathpkg.Join(pathpkg.Dir(base), sectionName+"_"+pathpkg.Base(base))
}

// startBind offers a path for a list the section does not have yet.
func (a *App) startBind(ctx context.Context, b *tgbot.Bot, update *models.Update, act targetAction) {
	chatID := update.CallbackQuery.Message.Message.Chat.ID

	s, err := a.findSection(ctx, act.secTok)
	if err != nil {
		a.answerAndEditMarkup(ctx, b, update, targetError(err), a.backToSectionsInlineKeyboard())
		return
	}
	if s.Name == "" {
		a.answerAndEditMarkup(ctx, b, update,
			"ℹ️ Эта секция не из podkop — привязывать файл некуда.", a.backToSectionsInlineKeyboard())
		return
	}
	a.logf(chatID, "bind_prompt section=%q type=%s", s.Name, typeLabel(act.typ))
	a.showBindConfirm(ctx, b, a.replyTo(update, chatID), s.Name, act.typ, a.suggestBindPath(s.Name, act.typ))
}

// showBindConfirm is the last screen before the podkop config is touched.
func (a *App) showBindConfirm(ctx context.Context, b *tgbot.Bot, reply pickReply, sectionName string, t lists.EntryType, path string) {
	opID := a.sess.Create(PendingOp{
		ChatID:   reply.chatID,
		Kind:     ActionBind,
		Section:  sectionName,
		ListType: t,
		ListPath: path,
	})
	text := fmt.Sprintf(
		"🔗 Привязать файл (%s) к секции «%s»:\n\n%s\n\nФайл будет создан, если его ещё нет, а путь добавлен в /etc/config/podkop. После этого podkop нужно перезапустить.",
		typeLabel(t), sectionName, path)
	kb := &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: btnBindConfirm, CallbackData: cbPrefix + opID + ":confirm"}},
			{{Text: btnCustomPath, CallbackData: cbPrefix + opID + ":custom"}},
			{{Text: btnCancel, CallbackData: cbPrefix + opID + ":cancel"}},
		},
	}
	reply.send(ctx, b, text, kb)
}

// promptBindPath asks for a path typed by hand.
func (a *App) promptBindPath(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	a.sess.Await(op.ChatID, awaitBindPath, op.ID)
	a.answerAndEditMarkup(ctx, b, update,
		"✍️ Пришлите абсолютный путь к файлу одним сообщением, например:\n"+
			a.suggestBindPath(op.Section, op.ListType)+
			"\n\nДопустимы латиница, цифры и символы . _ - /",
		a.cancelKeyboard(op.ID))
}

// handleBindPathText validates a hand-typed path and asks to confirm it.
func (a *App) handleBindPathText(ctx context.Context, b *tgbot.Bot, chatID int64, op *PendingOp, text string) {
	path := strings.TrimSpace(text)
	if err := podkop.ValidatePath(path); err != nil {
		// A rejected path is a typo, not a reason to unwind the flow.
		a.sess.Await(chatID, awaitBindPath, op.ID)
		a.sendPlain(ctx, b, chatID, "❌ "+err.Error()+"\n\nПришлите другой путь.", a.cancelKeyboard(op.ID))
		return
	}
	a.sess.Delete(op.ID)
	a.showBindConfirm(ctx, b, a.replyTo(nil, chatID), op.Section, op.ListType, path)
}

// execBind writes the binding into podkop's config and creates the file.
func (a *App) execBind(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	if err := podkop.Bind(ctx, op.Section, op.ListType, op.ListPath); err != nil {
		a.logf(op.ChatID, "bind_error section=%q type=%s path=%q err=%v", op.Section, typeLabel(op.ListType), op.ListPath, err)
		a.answerAndEditMarkup(ctx, b, update, "❌ Не удалось привязать файл: "+err.Error(), a.backToSectionsInlineKeyboard())
		return
	}
	if err := lists.CreateFiles(op.ListPath); err != nil {
		a.logf(op.ChatID, "bind_create_file_error path=%q err=%v", op.ListPath, err)
		a.answerAndEditMarkup(ctx, b, update,
			"⚠️ Путь добавлен в podkop, но файл создать не удалось: "+err.Error(),
			a.backToSectionsInlineKeyboard())
		return
	}
	a.logf(op.ChatID, "bind_success section=%q type=%s path=%q", op.Section, typeLabel(op.ListType), op.ListPath)
	a.sess.Delete(op.ID)

	// The binding only takes effect on the next podkop restart, which is
	// exactly what the stale banner and its button are for.
	_ = a.svc.MarkFilesChanged()
	a.answerAndEditMarkup(ctx, b, update,
		fmt.Sprintf("🔗 Файл привязан к секции «%s»:\n%s", op.Section, op.ListPath),
		a.backToSectionInlineKeyboard(sectionToken(op.Section)))
}
