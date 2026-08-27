package bot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"lst-signbox-lists-tgbot/internal/lists"
)

const (
	btnShowAll       = "📄 Показать все"
	btnShowCategory  = "📂 Показать категорию"
	btnAddToCategory = "📂 Добавить в категорию"
	btnCategories    = "📂 Категории"
	btnCatDisableAll = "⏸ Отключить все"
	btnCatEnableAll  = "✅ Включить все"
	btnCatRename     = "✏️ Переименовать"
	btnCatDeleteKeep = "🗑 Удалить категорию"
	btnCatDeleteAll  = "🗑 Удалить с записями"
	btnCatMerge      = "📦 Переместить записи"
	btnNewCategory   = "🆕 Новая категория"
	btnCancel        = "❌ Отмена"
)

func listTitle(t lists.EntryType) string {
	if t == lists.TypeDomain {
		return "Домены"
	}
	return "IP/CIDR"
}

// showListMenu is the first screen behind "Показать домены"/"Показать IP":
// a summary plus the choice between the whole list and a single category.
func (a *App) showListMenu(ctx context.Context, b *tgbot.Bot, update *models.Update, tgt listTarget) {
	chatID := update.CallbackQuery.Message.Message.Chat.ID
	back := a.backToSectionInlineKeyboard(sectionToken(tgt.Section))

	cats, err := lists.Categories(tgt.Path)
	if err != nil {
		a.logf(chatID, "list_menu read_error type=%s path=%q err=%v", typeLabel(tgt.Type), tgt.Path, err)
		a.answerAndEditMarkup(ctx, b, update,
			fmt.Sprintf("❌ Не удалось прочитать %s: %v", typeLabel(tgt.Type), err), back)
		return
	}

	active, disabled, named := summaryLine(cats)
	text := fmt.Sprintf("📋 %s — %s\n%s\n\n", listTitle(tgt.Type), sectionDisplayName(tgt.Section), tgt.Path)
	if active+disabled == 0 {
		text += "Список пуст."
		a.answerAndEditMarkup(ctx, b, update, text, back)
		return
	}

	text += fmt.Sprintf("Всего: %d (%d ✅ / %d ⏸)\nКатегорий: %d", active+disabled, active, disabled, named)
	if named != len(cats) {
		text += fmt.Sprintf(" + «%s»", lists.UncategorizedLabel)
	}

	kb := &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{
				{Text: btnShowAll, CallbackData: targetCallback(verbViewAll, tgt.Type, sectionToken(tgt.Section), pathToken(tgt.Path))},
				{Text: btnShowCategory, CallbackData: targetCallback(verbViewCats, tgt.Type, sectionToken(tgt.Section), pathToken(tgt.Path))},
			},
			{
				{Text: btnBackToSec, CallbackData: menuCbPrefix + "sec_" + sectionToken(tgt.Section)},
				{Text: menuBtnMainMenu, CallbackData: menuCbPrefix + "main_menu"},
			},
		},
	}
	a.answerAndEditMarkup(ctx, b, update, text, kb)
}

// sendFullList renders every category as a heading plus an expandable quote,
// spread over as many messages as the 4096-character limit demands.
func (a *App) sendFullList(ctx context.Context, b *tgbot.Bot, update *models.Update, tgt listTarget) {
	chatID := update.CallbackQuery.Message.Message.Chat.ID
	t, path := tgt.Type, tgt.Path
	back := a.backToSectionInlineKeyboard(sectionToken(tgt.Section))

	cats, err := lists.Categories(path)
	if err != nil {
		a.logf(chatID, "view_all read_error type=%s err=%v", typeLabel(t), err)
		a.answerAndEditMarkup(ctx, b, update, "❌ Не удалось прочитать файл: "+err.Error(), back)
		return
	}
	if len(cats) == 0 {
		a.answerAndEditMarkup(ctx, b, update, "📋 "+listTitle(t)+"\n\nСписок пуст.", back)
		return
	}

	shown := make([]lists.CategoryInfo, 0, len(cats))
	blocks := make([]string, 0, len(cats))
	groups := make([][]lists.LineEntry, 0, len(cats))
	for _, c := range cats {
		entries, err := lists.CategoryEntries(path, c.Name, t)
		if err != nil {
			a.logf(chatID, "view_all category_error name=%q err=%v", c.Name, err)
			continue
		}
		shown = append(shown, c)
		groups = append(groups, entries)
		blocks = append(blocks, categoryBlock(c, entries))
	}

	active, disabled, _ := summaryLine(cats)
	header := fmt.Sprintf("📋 <b>%s</b> · %s — %d (%d ✅ / %d ⏸)",
		esc(listTitle(t)), esc(sectionDisplayName(tgt.Section)), active+disabled, active, disabled)
	render := buildListRender(header, blocks, shown, listMessageMaxLen)

	a.answerCallback(ctx, b, update)
	for i, msg := range render.Messages {
		params := &tgbot.SendMessageParams{
			ChatID:    chatID,
			Text:      msg,
			ParseMode: models.ParseModeHTML,
		}
		if i == len(render.Messages)-1 && len(render.Oversized) == 0 {
			params.ReplyMarkup = back
		}
		if _, err := b.SendMessage(ctx, params); err != nil {
			a.logf(chatID, "view_all send_error part=%d err=%v", i, err)
		}
	}

	// Categories too big for a message of their own go out as files, so
	// "показать все" never silently drops entries.
	for i, c := range render.Oversized {
		idx := indexOfCategory(shown, c.Name)
		if idx < 0 {
			continue
		}
		a.sendCategoryFile(ctx, b, chatID, tgt, c, groups[idx], i == len(render.Oversized)-1)
	}
	a.logf(chatID, "view_all sent type=%s messages=%d files=%d", typeLabel(t), len(render.Messages), len(render.Oversized))
}

func indexOfCategory(cats []lists.CategoryInfo, name string) int {
	for i, c := range cats {
		if lists.SameCategory(c.Name, name) {
			return i
		}
	}
	return -1
}

func (a *App) sendCategoryFile(ctx context.Context, b *tgbot.Bot, chatID int64, tgt listTarget, c lists.CategoryInfo, entries []lists.LineEntry, withKeyboard bool) {
	body := strings.Join(entryLines(entries, false), "\n") + "\n"
	params := &tgbot.SendDocumentParams{
		ChatID: chatID,
		Document: &models.InputFileUpload{
			Filename: fmt.Sprintf("%s-%s.lst", listTypeToken(tgt.Type), categoryToken(c.Name)),
			Data:     bytes.NewReader([]byte(body)),
		},
		Caption: fmt.Sprintf("📂 %s — %s (не помещается в сообщение)", c.DisplayName(), countsLabel(c)),
	}
	if withKeyboard {
		params.ReplyMarkup = a.backToSectionInlineKeyboard(sectionToken(tgt.Section))
	}
	if _, err := b.SendDocument(ctx, params); err != nil {
		a.logf(chatID, "category_file_error name=%q err=%v", c.Name, err)
	}
}

// sendCategoryPicker offers the categories as buttons, falling back to
// tappable commands when there are more of them than a keyboard can hold.
func (a *App) sendCategoryPicker(ctx context.Context, b *tgbot.Bot, update *models.Update, tgt listTarget) {
	back := a.backToSectionInlineKeyboard(sectionToken(tgt.Section))

	cats, err := lists.Categories(tgt.Path)
	if err != nil {
		a.answerAndEditMarkup(ctx, b, update, "❌ Не удалось прочитать файл: "+err.Error(), back)
		return
	}
	if len(cats) == 0 {
		a.answerAndEditMarkup(ctx, b, update, "📋 "+listTitle(tgt.Type)+"\n\nСписок пуст.", back)
		return
	}

	header := fmt.Sprintf("📂 Категории — %s · %s", strings.ToLower(listTitle(tgt.Type)), sectionDisplayName(tgt.Section))
	if rows, ok := categoryPickRows(cats, func(name string) string { return viewCategoryCallback(tgt, name) }); ok {
		rows = append(rows, []models.InlineKeyboardButton{
			{Text: btnBackToSec, CallbackData: menuCbPrefix + "sec_" + sectionToken(tgt.Section)},
			{Text: menuBtnMainMenu, CallbackData: menuCbPrefix + "main_menu"},
		})
		a.answerAndEditMarkup(ctx, b, update, header+"\n\nВыберите категорию:",
			&models.InlineKeyboardMarkup{InlineKeyboard: rows})
		return
	}

	text := categoryPickerText(header, tooManyForButtonsHint, cats,
		func(name string) string { return viewCategoryCommand(tgt, name) })
	a.answerAndEditMarkup(ctx, b, update, truncateForMessage(text, listMessageMaxLen), back)
}

// handleViewCategoryCommand answers a "/cd_<token>" tap from a picker message.
func (a *App) handleViewCategoryCommand(ctx context.Context, b *tgbot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}
	chatID := update.Message.Chat.ID
	a.deleteMessage(ctx, b, chatID, update.Message.ID)

	act, ok := parseViewCommand(update.Message.Text)
	if !ok {
		return
	}
	tgt, err := a.resolveTarget(ctx, act.secTok, act.fileTok, act.typ)
	if err != nil {
		a.sendPlain(ctx, b, chatID, targetError(err), a.backToSectionsInlineKeyboard())
		return
	}
	a.openCategoryByToken(ctx, b, a.replyTo(nil, chatID), tgt, act.rest)
}

// openCategoryByToken resolves a category picked in a message — by button or
// by command — and shows its card.
func (a *App) openCategoryByToken(ctx context.Context, b *tgbot.Bot, reply pickReply, tgt listTarget, token string) {
	back := a.backToSectionInlineKeyboard(sectionToken(tgt.Section))

	name, found, err := resolveCategoryToken(tgt.Path, token)
	if err != nil {
		a.logf(reply.chatID, "category_view read_error token=%s err=%v", token, err)
		reply.send(ctx, b, "❌ Не удалось прочитать файл: "+err.Error(), back)
		return
	}
	if !found {
		a.logf(reply.chatID, "category_view unknown token=%s type=%s", token, typeLabel(tgt.Type))
		reply.send(ctx, b, "🤷 Такой категории больше нет — откройте список заново.", back)
		return
	}
	a.logf(reply.chatID, "category_view name=%q type=%s section=%q", name, typeLabel(tgt.Type), tgt.Section)
	a.sendCategoryCard(ctx, b, reply, tgt, name)
}

// sendCategoryCard shows one category and the actions available on it. Opened
// from a picker button it replaces that message, so browsing categories stays
// in one place instead of piling cards up in the chat.
func (a *App) sendCategoryCard(ctx context.Context, b *tgbot.Bot, reply pickReply, tgt listTarget, name string) {
	chatID := reply.chatID
	t, path := tgt.Type, tgt.Path
	back := a.backToSectionInlineKeyboard(sectionToken(tgt.Section))

	cats, err := lists.Categories(path)
	if err != nil {
		reply.send(ctx, b, "❌ Не удалось прочитать файл: "+err.Error(), back)
		return
	}
	idx := indexOfCategory(cats, name)
	if idx < 0 {
		reply.send(ctx, b, "🤷 Такой категории больше нет — откройте список заново.", back)
		return
	}
	info := cats[idx]

	entries, err := lists.CategoryEntries(path, name, t)
	if err != nil {
		reply.send(ctx, b, "❌ Не удалось прочитать категорию: "+err.Error(), back)
		return
	}

	opID := a.sess.Create(PendingOp{
		ChatID:   chatID,
		Kind:     ActionCategory,
		ListType: t,
		ListPath: path,
		Section:  tgt.Section,
		Category: name,
	})
	kb := a.categoryCardKeyboard(opID, tgt, info, len(cats))

	block := categoryBlock(info, entries)
	if len(block) > listMessageMaxLen {
		a.sendCategoryFile(ctx, b, chatID, tgt, info, entries, false)
		block = fmt.Sprintf("📂 <b>%s</b> — %s\n<i>список отправлен отдельным файлом</i>", esc(info.DisplayName()), countsLabel(info))
	}
	reply.sendHTML(ctx, b, block, kb)
}

func (a *App) categoryCardKeyboard(opID string, tgt listTarget, info lists.CategoryInfo, totalCats int) *models.InlineKeyboardMarkup {
	var rows [][]models.InlineKeyboardButton

	var stateRow []models.InlineKeyboardButton
	if info.Active > 0 {
		stateRow = append(stateRow, models.InlineKeyboardButton{
			Text: btnCatDisableAll, CallbackData: cbPrefix + opID + ":off",
		})
	}
	if info.Disabled > 0 {
		stateRow = append(stateRow, models.InlineKeyboardButton{
			Text: btnCatEnableAll, CallbackData: cbPrefix + opID + ":on",
		})
	}
	if len(stateRow) > 0 {
		rows = append(rows, stateRow)
	}

	if totalCats > 1 && info.Total() > 0 {
		rows = append(rows, []models.InlineKeyboardButton{{
			Text: btnCatMerge, CallbackData: cbPrefix + opID + ":mv",
		}})
	}
	if info.Name != lists.Uncategorized {
		rows = append(rows, []models.InlineKeyboardButton{{
			Text: btnCatRename, CallbackData: cbPrefix + opID + ":ren",
		}})
		rows = append(rows, []models.InlineKeyboardButton{
			{Text: btnCatDeleteKeep, CallbackData: cbPrefix + opID + ":delk"},
			{Text: btnCatDeleteAll, CallbackData: cbPrefix + opID + ":delw"},
		})
	}
	rows = append(rows, []models.InlineKeyboardButton{
		{Text: btnCategories, CallbackData: targetCallback(verbViewCats, tgt.Type, sectionToken(tgt.Section), pathToken(tgt.Path))},
		{Text: menuBtnMainMenu, CallbackData: menuCbPrefix + "main_menu"},
	})
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// handleCategoryAction runs the buttons on a category card.
func (a *App) handleCategoryAction(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp, action string) {
	label := lists.CategoryDisplayName(op.Category)

	switch action {
	case "off", "on":
		enabled := action == "on"
		changed, err := lists.SetCategoryEnabled(op.ListPath, op.Category, enabled, op.ListType)
		if err != nil {
			a.logf(op.ChatID, "category_state error name=%q enabled=%t err=%v", op.Category, enabled, err)
			a.answerAndEditMarkup(ctx, b, update, categoryError(err), a.backToMainMenuInlineKeyboard())
			return
		}
		a.logf(op.ChatID, "category_state name=%q enabled=%t changed=%d", op.Category, enabled, changed)
		if changed == 0 {
			a.answerAndEditMarkup(ctx, b, update, fmt.Sprintf("ℹ️ В «%s» нечего менять.", label), a.backToMainMenuInlineKeyboard())
			return
		}
		verb := "⏸ Отключено"
		if enabled {
			verb = "✅ Включено"
		}
		a.sess.Delete(op.ID)
		a.afterFilesChanged(ctx, b, update, op.ChatID, fmt.Sprintf("%s в «%s»: %d", verb, label, changed))

	case "ren":
		a.sess.Await(op.ChatID, awaitRename, op.ID)
		a.answerAndEditMarkup(ctx, b, update,
			fmt.Sprintf("✏️ Пришлите новое имя для «%s» одним сообщением (до %d символов).", label, lists.MaxCategoryNameLen),
			a.cancelKeyboard(op.ID))

	case "mv":
		a.showMergePicker(ctx, b, update, op)

	case "delk":
		a.answerAndEditMarkup(ctx, b, update,
			fmt.Sprintf("🗑 Удалить категорию «%s»?\n\nЗаписи останутся в файле и переедут в «%s».", label, lists.UncategorizedLabel),
			a.confirmKeyboard(op.ID, "delk!"))

	case "delk!":
		moved, err := lists.DeleteCategoryKeepEntries(op.ListPath, op.Category, op.ListType)
		if err != nil {
			a.logf(op.ChatID, "category_delete_keep error name=%q err=%v", op.Category, err)
			a.answerAndEditMarkup(ctx, b, update, categoryError(err), a.backToMainMenuInlineKeyboard())
			return
		}
		a.logf(op.ChatID, "category_delete_keep name=%q moved=%d", op.Category, moved)
		a.sess.Delete(op.ID)
		a.afterFilesChanged(ctx, b, update, op.ChatID,
			fmt.Sprintf("🗑 Категория «%s» удалена. Записей перенесено в «%s»: %d", label, lists.UncategorizedLabel, moved))

	case "delw":
		a.answerAndEditMarkup(ctx, b, update,
			fmt.Sprintf("🗑 Удалить категорию «%s» вместе со всеми записями?\n\nЭто необратимо.", label),
			a.confirmKeyboard(op.ID, "delw!"))

	case "delw!":
		removed, err := lists.DeleteCategoryWithEntries(op.ListPath, op.Category, op.ListType)
		if err != nil {
			a.logf(op.ChatID, "category_delete_all error name=%q err=%v", op.Category, err)
			a.answerAndEditMarkup(ctx, b, update, categoryError(err), a.backToMainMenuInlineKeyboard())
			return
		}
		a.logf(op.ChatID, "category_delete_all name=%q removed=%d", op.Category, removed)
		a.sess.Delete(op.ID)
		a.afterFilesChanged(ctx, b, update, op.ChatID,
			fmt.Sprintf("🗑 Категория «%s» удалена вместе с записями (%d).", label, removed))

	default:
		a.answerCallback(ctx, b, update)
	}
}

// tooManyForButtonsHint introduces the command list the pickers fall back to.
const tooManyForButtonsHint = "Категорий слишком много для кнопок — нажмите на команду с нужной:"

// showCategoryPick asks which category a pending operation should land in:
// one button per category, plus a button for a category that does not exist
// yet. Only a list too long for a keyboard falls back to commands.
func (a *App) showCategoryPick(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp, header string, cats []lists.CategoryInfo) {
	newRow := []models.InlineKeyboardButton{{
		Text: btnNewCategory, CallbackData: cbPrefix + op.ID + ":" + cbNewCategory,
	}}
	cancelRow := []models.InlineKeyboardButton{{
		Text: btnCancel, CallbackData: cbPrefix + op.ID + ":cancel",
	}}

	if rows, ok := categoryPickRows(cats, func(name string) string {
		return cbPrefix + op.ID + ":" + cbPickPrefix + categoryToken(name)
	}); ok {
		rows = append(rows, newRow, cancelRow)
		a.answerAndEditMarkup(ctx, b, update, header+"\n\n"+categoryPickHint(len(cats)),
			&models.InlineKeyboardMarkup{InlineKeyboard: rows})
		return
	}

	text := categoryPickerText(header, tooManyForButtonsHint, cats,
		func(name string) string { return addCategoryCommand(op.ID, op.ListType, name) })
	a.answerAndEditMarkup(ctx, b, update, truncateForMessage(text, listMessageMaxLen),
		&models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{newRow, cancelRow}})
}

// showMergePicker offers the categories the current one can be poured into.
func (a *App) showMergePicker(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	cats, err := lists.Categories(op.ListPath)
	if err != nil {
		a.answerAndEditMarkup(ctx, b, update, "❌ Не удалось прочитать файл: "+err.Error(), a.backToMainMenuInlineKeyboard())
		return
	}

	targets := make([]lists.CategoryInfo, 0, len(cats))
	for _, c := range cats {
		if !lists.SameCategory(c.Name, op.Category) {
			targets = append(targets, c)
		}
	}

	a.showCategoryPick(ctx, b, update, op,
		fmt.Sprintf("📦 Куда переместить записи из «%s»?", lists.CategoryDisplayName(op.Category)), targets)
}

// handleAddCategoryPrompt turns the pending add into a category pick.
func (a *App) handleAddCategoryPrompt(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	cats, err := lists.Categories(op.ListPath)
	if err != nil {
		a.answerAndEditMarkup(ctx, b, update, "❌ Не удалось прочитать файл: "+err.Error(), a.backToMainMenuInlineKeyboard())
		return
	}
	a.logf(op.ChatID, "add_category_prompt count=%d categories=%d", len(op.Values), len(cats))
	a.showCategoryPick(ctx, b, update, op,
		fmt.Sprintf("📂 В какую категорию добавить %s?", pluralEntries(len(op.Values))), cats)
}

// handleCategoryPick applies a picker button: the tapped category becomes the
// target of whatever the operation was waiting for.
func (a *App) handleCategoryPick(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp, token string) {
	chatID := update.CallbackQuery.Message.Message.Chat.ID

	name, found, err := resolveCategoryToken(op.ListPath, token)
	if err != nil {
		a.logf(chatID, "category_pick read_error token=%s err=%v", token, err)
		a.answerAndEditMarkup(ctx, b, update, "❌ Не удалось прочитать файл: "+err.Error(), a.backToMainMenuInlineKeyboard())
		return
	}
	if !found {
		a.logf(chatID, "category_pick unknown token=%s", token)
		a.answerAndEditMarkup(ctx, b, update, "🤷 Такой категории больше нет — откройте выбор заново.", a.backToMainMenuInlineKeyboard())
		return
	}
	a.applyCategoryPick(ctx, b, a.replyTo(update, chatID), op, name)
}

// handleAddCategoryCommand answers a "/gd_<token>" tap: it applies the chat's
// pending pick to the chosen category.
func (a *App) handleAddCategoryCommand(ctx context.Context, b *tgbot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}
	chatID := update.Message.Chat.ID
	a.deleteMessage(ctx, b, chatID, update.Message.ID)

	t, opID, token, ok := parseAddCommand(update.Message.Text)
	if !ok {
		return
	}
	op, ok := a.sess.Get(opID)
	if !ok || op.ChatID != chatID {
		a.sendPlain(ctx, b, chatID, "⏳ Операция устарела. Отправьте список снова.", a.backToMainMenuInlineKeyboard())
		return
	}
	if op.ListType != t {
		a.sendPlain(ctx, b, chatID, "⚠️ Эта категория из другого списка. Откройте выбор заново.", a.backToMainMenuInlineKeyboard())
		return
	}

	name, found, err := resolveCategoryToken(op.ListPath, token)
	if err != nil {
		a.sendPlain(ctx, b, chatID, "❌ Не удалось прочитать файл: "+err.Error(), a.backToMainMenuInlineKeyboard())
		return
	}
	if !found {
		a.sendPlain(ctx, b, chatID, "🤷 Такой категории больше нет — откройте выбор заново.", a.backToMainMenuInlineKeyboard())
		return
	}
	a.applyCategoryPick(ctx, b, a.replyTo(nil, chatID), op, name)
}

// promptNewCategory asks for the name of a category that does not exist yet.
func (a *App) promptNewCategory(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	a.sess.Await(op.ChatID, awaitNewCategory, op.ID)
	a.logf(op.ChatID, "new_category_prompt op_kind=%d", op.Kind)
	a.answerAndEditMarkup(ctx, b, update,
		fmt.Sprintf("🆕 Пришлите название новой категории одним сообщением (до %d символов).", lists.MaxCategoryNameLen),
		a.cancelKeyboard(op.ID))
}

// handleAwaitedText consumes a plain message that the bot asked for.
func (a *App) handleAwaitedText(ctx context.Context, b *tgbot.Bot, update *models.Update, kind awaitKind, opID, text string) {
	chatID := update.Message.Chat.ID

	// The user may ignore the prompt and just send the next list instead of a
	// name. Anything that parses cleanly as domains or IPs is treated as list
	// input rather than silently becoming a category called "vk.com".
	if parsed := lists.ParseInput(text); !parsed.Empty && !parsed.Mixed && len(parsed.Invalid) == 0 && len(parsed.Valid) > 0 {
		a.logf(chatID, "await_abandoned kind=%d reason=list_input", kind)
		a.handleListInput(ctx, b, update)
		return
	}

	op, ok := a.sess.Get(opID)
	if !ok {
		a.sendPlain(ctx, b, chatID, "⏳ Операция устарела. Начните заново.", a.backToMainMenuInlineKeyboard())
		return
	}

	// A path is not a category name, so it is validated by its own rules.
	if kind == awaitBindPath {
		a.handleBindPathText(ctx, b, chatID, op, text)
		return
	}

	name, err := lists.ValidateCategoryName(text)
	if err != nil {
		// Keep waiting: a rejected name is a typo, not a reason to unwind the
		// whole flow.
		a.sess.Await(chatID, kind, opID)
		a.sendPlain(ctx, b, chatID, "❌ "+err.Error()+"\n\nПришлите другое имя.", a.cancelKeyboard(opID))
		return
	}

	switch kind {
	case awaitNewCategory:
		a.applyCategoryPick(ctx, b, a.replyTo(nil, chatID), op, name)
	case awaitRename:
		a.execRenameCategory(ctx, b, chatID, op, name)
	}
}

func (a *App) execRenameCategory(ctx context.Context, b *tgbot.Bot, chatID int64, op *PendingOp, name string) {
	err := lists.RenameCategory(op.ListPath, op.Category, name, op.ListType)
	if errors.Is(err, lists.ErrCategoryExists) {
		a.sess.Await(chatID, awaitRename, op.ID)
		a.sendPlain(ctx, b, chatID, fmt.Sprintf("❌ Категория «%s» уже есть. Пришлите другое имя.", name), a.cancelKeyboard(op.ID))
		return
	}
	if err != nil {
		a.logf(chatID, "category_rename error old=%q new=%q err=%v", op.Category, name, err)
		a.sendPlain(ctx, b, chatID, categoryError(err), a.backToMainMenuInlineKeyboard())
		return
	}
	a.logf(chatID, "category_rename old=%q new=%q", op.Category, name)
	a.sess.Delete(op.ID)
	a.filesChanged(ctx, b, chatID,
		fmt.Sprintf("✏️ Категория «%s» переименована в «%s».", lists.CategoryDisplayName(op.Category), name),
		a.backToMainMenuInlineKeyboard())
}

// pickReply routes the result of a category flow: a pick made with a button
// replaces the message it was tapped in, while one typed as text — a brand new
// category name — gets a message of its own.
type pickReply struct {
	app    *App
	chatID int64
	update *models.Update
}

func (a *App) replyTo(update *models.Update, chatID int64) pickReply {
	return pickReply{app: a, chatID: chatID, update: update}
}

func (r pickReply) send(ctx context.Context, b *tgbot.Bot, text string, kb *models.InlineKeyboardMarkup) {
	if r.update != nil {
		r.app.answerAndEditMarkup(ctx, b, r.update, text, kb)
		return
	}
	r.app.sendPlain(ctx, b, r.chatID, text, kb)
}

// sendHTML is send for text that carries markup, such as a category card.
func (r pickReply) sendHTML(ctx context.Context, b *tgbot.Bot, text string, kb *models.InlineKeyboardMarkup) {
	if r.update != nil {
		r.app.answerCallback(ctx, b, r.update)
		r.app.editCallbackMessage(ctx, b, r.update, text, kb, models.ParseModeHTML)
		return
	}
	if _, err := b.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID:      r.chatID,
		Text:        text,
		ParseMode:   models.ParseModeHTML,
		ReplyMarkup: kb,
	}); err != nil {
		r.app.logf(r.chatID, "category_card send_error err=%v", err)
	}
}

// changed is send for a result that touched the list files.
func (r pickReply) changed(ctx context.Context, b *tgbot.Bot, text string, kb *models.InlineKeyboardMarkup) {
	if r.update == nil {
		r.app.filesChanged(ctx, b, r.chatID, text, kb)
		return
	}
	// The edit helper prepends the stale banner on its own.
	_ = r.app.svc.MarkFilesChanged()
	r.app.answerAndEditMarkup(ctx, b, r.update, text, kb)
	r.app.maybeAutoRestart(r.chatID, b)
}

// applyCategoryPick runs whatever the chat was picking a category for.
func (a *App) applyCategoryPick(ctx context.Context, b *tgbot.Bot, reply pickReply, op *PendingOp, category string) {
	a.sess.ClearAwait(reply.chatID)

	switch op.Kind {
	case ActionAdd:
		a.execAddToCategory(ctx, b, reply, op, category)
	case ActionCategory:
		a.execMergeCategory(ctx, b, reply, op, category)
	default:
		reply.send(ctx, b, "⏳ Операция устарела. Начните заново.", a.backToMainMenuInlineKeyboard())
	}
}

// execAddToCategory adds the pending values into category and reports the ones
// that already live somewhere else, offering to move them.
func (a *App) execAddToCategory(ctx context.Context, b *tgbot.Bot, reply pickReply, op *PendingOp, category string) {
	chatID := reply.chatID

	classified, err := lists.ClassifyValues(op.ListPath, op.Values)
	if err != nil {
		a.logf(chatID, "add_category classify_error path=%q err=%v", op.ListPath, err)
		reply.send(ctx, b, "❌ Ошибка чтения файла: "+err.Error(), a.backToMainMenuInlineKeyboard())
		return
	}
	newVals, _, disabled := lists.GroupByStatus(classified)
	misplaced := lists.Misplaced(classified, category)
	label := lists.CategoryDisplayName(category)

	if len(newVals) > 0 {
		if err := lists.AddNew(op.ListPath, op.Values, op.ListType, category); err != nil {
			a.logf(chatID, "add_category write_error path=%q err=%v", op.ListPath, err)
			reply.send(ctx, b, "❌ Ошибка записи: "+err.Error(), a.backToMainMenuInlineKeyboard())
			return
		}
	}
	a.sess.Delete(op.ID)

	var sb strings.Builder
	if len(newVals) > 0 {
		sb.WriteString(fmt.Sprintf("➕ Добавлено в «%s» (%d):\n%s", label, len(newVals), lists.FormatList(newVals)))
	} else {
		sb.WriteString(fmt.Sprintf("ℹ️ Новых записей для «%s» нет.", label))
	}
	if len(disabled) > 0 {
		sb.WriteString(fmt.Sprintf("\n\n⏸ Уже в файле, но отключены (%d):\n%s", len(disabled), lists.FormatList(disabled)))
	}

	kb := a.backToMainMenuInlineKeyboard()
	if len(misplaced) > 0 {
		sb.WriteString(fmt.Sprintf("\n\n📂 Уже в других категориях (%d):\n%s", len(misplaced), formatMisplaced(misplaced)))
		moveID := a.sess.Create(PendingOp{
			ChatID:   chatID,
			Kind:     ActionMove,
			ListType: op.ListType,
			ListPath: op.ListPath,
			Section:  op.Section,
			Values:   sortedKeys(misplaced),
			Category: category,
			Origin:   misplaced,
		})
		kb = &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{
				{{Text: fmt.Sprintf("📦 Переместить в «%s»", label), CallbackData: cbPrefix + moveID + ":confirm"}},
				{{Text: menuBtnMainMenu, CallbackData: menuCbPrefix + "main_menu"}},
			},
		}
	}

	a.logf(chatID, "add_category name=%q added=%d misplaced=%d path=%q", category, len(newVals), len(misplaced), op.ListPath)
	if len(newVals) == 0 {
		reply.send(ctx, b, sb.String(), kb)
		return
	}
	reply.changed(ctx, b, sb.String(), kb)
}

func (a *App) execMergeCategory(ctx context.Context, b *tgbot.Bot, reply pickReply, op *PendingOp, target string) {
	chatID := reply.chatID

	moved, err := lists.MergeCategory(op.ListPath, op.Category, target, op.ListType)
	if err != nil {
		a.logf(chatID, "category_merge error from=%q to=%q err=%v", op.Category, target, err)
		reply.send(ctx, b, categoryError(err), a.backToMainMenuInlineKeyboard())
		return
	}
	a.logf(chatID, "category_merge from=%q to=%q moved=%d", op.Category, target, moved)
	a.sess.Delete(op.ID)
	if moved == 0 {
		reply.send(ctx, b, "ℹ️ Переносить нечего.", a.backToMainMenuInlineKeyboard())
		return
	}
	reply.changed(ctx, b, fmt.Sprintf("📦 Перенесено в «%s»: %d (из «%s»).",
		lists.CategoryDisplayName(target), moved, lists.CategoryDisplayName(op.Category)), a.backToMainMenuInlineKeyboard())
}

// execMove retags the values that turned up in the wrong category.
func (a *App) execMove(ctx context.Context, b *tgbot.Bot, update *models.Update, op *PendingOp) {
	moved, err := lists.MoveToCategory(op.ListPath, op.Values, op.Category, op.ListType)
	if err != nil {
		a.logf(op.ChatID, "move error target=%q err=%v", op.Category, err)
		a.answerAndEditMarkup(ctx, b, update, categoryError(err), a.backToMainMenuInlineKeyboard())
		return
	}
	a.logf(op.ChatID, "move target=%q moved=%d", op.Category, moved)
	a.sess.Delete(op.ID)
	if moved == 0 {
		a.answerAndEditMarkup(ctx, b, update, "ℹ️ Переносить нечего.", a.backToMainMenuInlineKeyboard())
		return
	}
	a.afterFilesChanged(ctx, b, update, op.ChatID,
		fmt.Sprintf("📦 Перемещено в «%s» (%d):\n%s", lists.CategoryDisplayName(op.Category), moved, lists.FormatList(op.Values)))
}

func formatMisplaced(m map[string]string) string {
	keys := sortedKeys(m)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("• %s — %s", k, lists.CategoryDisplayName(m[k])))
	}
	return strings.Join(lines, "\n")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Stable output so the same set of values always reads the same way.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && strings.ToLower(keys[j]) < strings.ToLower(keys[j-1]); j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

func categoryError(err error) string {
	if errors.Is(err, lists.ErrCategoryNotFound) {
		return "🤷 Такой категории больше нет — откройте список заново."
	}
	return "❌ Ошибка: " + err.Error()
}

func (a *App) cancelKeyboard(opID string) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: btnCancel, CallbackData: cbPrefix + opID + ":cancel"}},
		},
	}
}

func (a *App) confirmKeyboard(opID, action string) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: "✅ Подтвердить", CallbackData: cbPrefix + opID + ":" + action}},
			{{Text: btnCancel, CallbackData: cbPrefix + opID + ":cancel"}},
		},
	}
}

func (a *App) sendPlain(ctx context.Context, b *tgbot.Bot, chatID int64, text string, kb *models.InlineKeyboardMarkup) {
	if _, err := b.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID:      chatID,
		Text:        text,
		ReplyMarkup: kb,
	}); err != nil {
		a.logf(chatID, "send_message_error err=%v", err)
	}
}

// filesChanged is the send-a-new-message twin of afterFilesChanged, for flows
// driven by a command instead of a callback.
func (a *App) filesChanged(ctx context.Context, b *tgbot.Bot, chatID int64, text string, kb *models.InlineKeyboardMarkup) {
	_ = a.svc.MarkFilesChanged()
	if banner := a.svc.StaleBanner(); banner != "" {
		text = banner + "\n\n" + text
		if reason := a.restartHiddenReason(); reason != "" {
			text += "\n\n" + reason
		}
	}
	a.sendPlain(ctx, b, chatID, text, kb)
	a.maybeAutoRestart(chatID, b)
}

// deleteMessage removes the command message a tapped link produced, so the
// chat is not littered with "/cd_a1b2c3d4" lines.
func (a *App) deleteMessage(ctx context.Context, b *tgbot.Bot, chatID int64, messageID int) {
	if _, err := b.DeleteMessage(ctx, &tgbot.DeleteMessageParams{
		ChatID:    chatID,
		MessageID: messageID,
	}); err != nil {
		a.logf(chatID, "delete_message_error id=%d err=%v", messageID, err)
	}
}

func (a *App) answerCallback(ctx context.Context, b *tgbot.Bot, update *models.Update) {
	if update.CallbackQuery == nil {
		return
	}
	_, _ = b.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
	})
}
