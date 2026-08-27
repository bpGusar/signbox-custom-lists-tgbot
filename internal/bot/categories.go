package bot

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/go-telegram/bot/models"

	"lst-signbox-lists-tgbot/internal/lists"
)

// Categories are picked with inline buttons. Past categoryButtonLimit the
// keyboard would be taller than the screen, so the pickers fall back to
// tappable commands: Telegram turns any "/token" into a link that is sent
// straight back to the bot, which keeps a long list inside the message.
const (
	cmdViewPrefix      = "/c"
	cmdAddPrefix       = "/g"
	uncategorizedToken = "none"
)

// Actions carried by the picker buttons.
const (
	// cbPickPrefix is followed by the token of the picked category.
	cbPickPrefix = "pick_"
	// cbNewCategory asks for the name of a category that does not exist yet.
	cbNewCategory = "new"
)

const (
	// categoryButtonLimit is how many categories still make a workable keyboard.
	categoryButtonLimit = 48
	// categoryRowWidth is roughly how many characters fit across a phone
	// screen, and categoryRowMax how many buttons stay tappable in one row.
	// Together they decide how many categories share a row.
	categoryRowWidth = 26
	categoryRowMax   = 3
)

// listMessageMaxLen leaves headroom under Telegram's 4096 limit. HTML markup
// does not count towards it, so measuring the marked-up text is conservative.
const listMessageMaxLen = tgMaxMessageLen - 200

var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// esc escapes the three characters Telegram's HTML parse mode reserves.
func esc(s string) string { return htmlEscaper.Replace(s) }

func listTypeToken(t lists.EntryType) string {
	if t == lists.TypeDomain {
		return "d"
	}
	return "i"
}

func listTypeFromToken(s string) (lists.EntryType, bool) {
	switch s {
	case "d":
		return lists.TypeDomain, true
	case "i":
		return lists.TypeIP, true
	}
	return lists.TypeUnknown, false
}

// categoryToken derives a short, stable id from the category name, so links in
// old messages keep working instead of drifting with the list order.
func categoryToken(name string) string {
	if name == lists.Uncategorized {
		return uncategorizedToken
	}
	sum := sha1.Sum([]byte(strings.ToLower(strings.TrimSpace(name))))
	return hex.EncodeToString(sum[:4])
}

// viewCategoryCommand and viewCategoryCallback both spell out the whole
// target — list type, section, file — so a tap in an old message opens the
// category in the file it was drawn from, not in whatever is current.
func viewCategoryCommand(tgt listTarget, name string) string {
	return cmdViewPrefix + targetSuffix(tgt) + "_" + categoryToken(name)
}

// targetSuffix is the "<list type>_<section>_<file>" the view links carry.
func targetSuffix(tgt listTarget) string {
	return listTypeToken(tgt.Type) + "_" + sectionToken(tgt.Section) + "_" + pathToken(tgt.Path)
}

// addCategoryCommand carries the operation id, because a tapped command tells
// the bot nothing about the message it was tapped in: without it, a command
// from an older message would be applied to whatever was picked most recently.
func addCategoryCommand(opID string, t lists.EntryType, name string) string {
	return cmdAddPrefix + listTypeToken(t) + "_" + opID + "_" + categoryToken(name)
}

func viewCategoryCallback(tgt listTarget, name string) string {
	return menuCbPrefix + verbViewCat + "_" + targetSuffix(tgt) + "_" + categoryToken(name)
}

// splitCommand strips the "@botname" suffix Telegram adds in groups and any
// trailing text, then peels off the list-type letter.
func splitCommand(text, prefix string) (lists.EntryType, string, bool) {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return lists.TypeUnknown, "", false
	}
	cmd := strings.SplitN(fields[0], "@", 2)[0]
	if !strings.HasPrefix(cmd, prefix) {
		return lists.TypeUnknown, "", false
	}
	typeTok, rest, ok := strings.Cut(strings.TrimPrefix(cmd, prefix), "_")
	if !ok || rest == "" {
		return lists.TypeUnknown, "", false
	}
	listType, ok := listTypeFromToken(typeTok)
	if !ok {
		return lists.TypeUnknown, "", false
	}
	return listType, rest, true
}

// parseViewCommand reads "/cd_<section>_<file>_<category>".
func parseViewCommand(text string) (targetAction, bool) {
	listType, rest, ok := splitCommand(text, cmdViewPrefix)
	if !ok {
		return targetAction{}, false
	}
	parts := strings.Split(rest, "_")
	if len(parts) != 3 {
		return targetAction{}, false
	}
	for _, p := range parts {
		if p == "" {
			return targetAction{}, false
		}
	}
	return targetAction{
		verb:    verbViewCat,
		typ:     listType,
		secTok:  parts[0],
		fileTok: parts[1],
		rest:    parts[2],
	}, true
}

// parseAddCommand reads "/gd_<operation>_<category>".
func parseAddCommand(text string) (lists.EntryType, string, string, bool) {
	listType, rest, ok := splitCommand(text, cmdAddPrefix)
	if !ok {
		return lists.TypeUnknown, "", "", false
	}
	opID, token, ok := strings.Cut(rest, "_")
	if !ok || opID == "" || token == "" || strings.Contains(token, "_") {
		return lists.TypeUnknown, "", "", false
	}
	return listType, opID, token, true
}

// resolveCategoryToken maps a token back to the category name currently in the
// file. Tokens outlive the message they were sent from, so a category deleted
// in the meantime simply resolves to nothing.
func resolveCategoryToken(path, token string) (string, bool, error) {
	if token == uncategorizedToken {
		return lists.Uncategorized, true, nil
	}
	cats, err := lists.Categories(path)
	if err != nil {
		return "", false, err
	}
	for _, c := range cats {
		if categoryToken(c.Name) == token {
			return c.Name, true, nil
		}
	}
	return "", false, nil
}

func countsLabel(c lists.CategoryInfo) string {
	if c.Disabled == 0 {
		return fmt.Sprintf("%d ✅", c.Active)
	}
	if c.Active == 0 {
		return fmt.Sprintf("%d ⏸", c.Disabled)
	}
	return fmt.Sprintf("%d ✅ / %d ⏸", c.Active, c.Disabled)
}

// shortCounts is countsLabel squeezed for a button, where every character
// costs screen width.
func shortCounts(c lists.CategoryInfo) string {
	if c.Disabled == 0 {
		return fmt.Sprintf("%d✅", c.Active)
	}
	if c.Active == 0 {
		return fmt.Sprintf("%d⏸", c.Disabled)
	}
	return fmt.Sprintf("%d✅ %d⏸", c.Active, c.Disabled)
}

func categoryButtonLabel(c lists.CategoryInfo) string {
	return c.DisplayName() + " · " + shortCounts(c)
}

// categoryPickRows lays the categories out as buttons, packing two or three
// short ones into a row and giving a long name a row of its own. It gives up
// when there are more categories than a keyboard can show comfortably, leaving
// the caller to fall back to categoryPickerText.
func categoryPickRows(cats []lists.CategoryInfo, callback func(name string) string) ([][]models.InlineKeyboardButton, bool) {
	if len(cats) > categoryButtonLimit {
		return nil, false
	}

	var rows [][]models.InlineKeyboardButton
	var row []models.InlineKeyboardButton
	width := 0

	for _, c := range cats {
		label := categoryButtonLabel(c)
		w := utf8.RuneCountInString(label)
		if len(row) > 0 && (len(row) >= categoryRowMax || width+w > categoryRowWidth) {
			rows = append(rows, row)
			row, width = nil, 0
		}
		row = append(row, models.InlineKeyboardButton{
			Text:         label,
			CallbackData: callback(c.Name),
		})
		width += w
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return rows, true
}

// categoryPickHint tells the user what to do with the keyboard below, which
// depends on whether there is anything to pick at all: an empty list must not
// read as "выберите категорию" with nothing to choose from.
func categoryPickHint(n int) string {
	if n == 0 {
		return "Категорий пока нет — создайте первую."
	}
	return "Выберите категорию кнопкой ниже или создайте новую."
}

// categoryPickerText is the fallback for lists too long for a keyboard: the
// categories become tappable commands inside the message. It stays plain text:
// Telegram linkifies commands regardless of parse mode, so there is nothing
// here that needs escaping.
func categoryPickerText(header, hint string, cats []lists.CategoryInfo, command func(string) string) string {
	var sb strings.Builder
	sb.WriteString(header)
	if hint != "" {
		sb.WriteString("\n\n")
		sb.WriteString(hint)
	}
	sb.WriteString("\n")
	for _, c := range cats {
		sb.WriteString(fmt.Sprintf("\n%s — %s — %s", command(c.Name), c.DisplayName(), countsLabel(c)))
	}
	return sb.String()
}

// pluralEntries spells a count of entries out, so a header reads "25 записей"
// instead of a bare number in brackets.
func pluralEntries(n int) string {
	form := "записей"
	if mod100 := n % 100; mod100 < 11 || mod100 > 14 {
		switch n % 10 {
		case 1:
			form = "запись"
		case 2, 3, 4:
			form = "записи"
		}
	}
	return fmt.Sprintf("%d %s", n, form)
}

// entryLines renders one category's entries, marking the disabled ones.
func entryLines(entries []lists.LineEntry, escape bool) []string {
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		value := e.Value
		if escape {
			value = esc(value)
		}
		if e.Disabled {
			value = "⏸ " + value
		}
		lines = append(lines, value)
	}
	return lines
}

// categoryBlock renders a category as a heading plus an expandable quote, so a
// long list collapses to a few lines instead of flooding the chat.
func categoryBlock(c lists.CategoryInfo, entries []lists.LineEntry) string {
	head := fmt.Sprintf("📂 <b>%s</b> — %s", esc(c.DisplayName()), countsLabel(c))
	if len(entries) == 0 {
		return head + "\n<i>пусто</i>"
	}
	return head + "\n<blockquote expandable>" + strings.Join(entryLines(entries, true), "\n") + "</blockquote>"
}

// listRender is a full list view already cut into sendable messages.
type listRender struct {
	Messages []string
	// Oversized names the categories that could not fit into a message on
	// their own and have to be sent as a file instead.
	Oversized []lists.CategoryInfo
}

// buildListRender packs category blocks into as few messages as possible,
// never splitting a category across two of them.
func buildListRender(header string, blocks []string, cats []lists.CategoryInfo, limit int) listRender {
	var out listRender
	current := header

	flush := func() {
		if strings.TrimSpace(current) != "" {
			out.Messages = append(out.Messages, current)
		}
		current = ""
	}

	for i, block := range blocks {
		if len(block) > limit {
			out.Oversized = append(out.Oversized, cats[i])
			continue
		}
		candidate := block
		if current != "" {
			candidate = current + "\n\n" + block
		}
		if len(candidate) > limit {
			flush()
			current = block
			continue
		}
		current = candidate
	}
	flush()
	return out
}

// summaryLine totals a whole list for the view menu.
func summaryLine(cats []lists.CategoryInfo) (active, disabled, named int) {
	for _, c := range cats {
		active += c.Active
		disabled += c.Disabled
		if c.Name != lists.Uncategorized {
			named++
		}
	}
	return active, disabled, named
}
