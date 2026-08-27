package bot

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"

	"lst-signbox-lists-tgbot/internal/lists"
)

func TestCategoryTokenIsStableAndCaseInsensitive(t *testing.T) {
	if got, want := categoryToken(lists.Uncategorized), uncategorizedToken; got != want {
		t.Fatalf("uncategorized token = %q, want %q", got, want)
	}
	if categoryToken("YouTube") != categoryToken("  youtube ") {
		t.Fatal("token must ignore case and surrounding spaces")
	}
	if categoryToken("YouTube") == categoryToken("Реклама") {
		t.Fatal("different names must not share a token")
	}
	if len(categoryToken("YouTube")) != 8 {
		t.Fatalf("token must stay short, got %q", categoryToken("YouTube"))
	}
}

func TestParseViewCommandRoundTrip(t *testing.T) {
	cases := []struct {
		name   string
		target listTarget
	}{
		{"YouTube", listTarget{Section: "main", Type: lists.TypeDomain, Path: "/etc/lst/domain_list.lst"}},
		{"Реклама", listTarget{Section: "youtube", Type: lists.TypeIP, Path: "/etc/lst/yt_ip.lst"}},
		{lists.Uncategorized, listTarget{Type: lists.TypeDomain, Path: "/etc/lst/domain_list.lst"}},
	}

	for _, c := range cases {
		cmd := viewCategoryCommand(c.target, c.name)
		// Telegram caps a command at 32 characters after the slash.
		if got := len(cmd) - 1; got > 32 {
			t.Fatalf("%q: command too long: %d characters", cmd, got)
		}
		act, ok := parseViewCommand(cmd)
		if !ok {
			t.Fatalf("%q: not parsed", cmd)
		}
		if act.typ != c.target.Type {
			t.Fatalf("%q: list type = %v, want %v", cmd, act.typ, c.target.Type)
		}
		if act.secTok != sectionToken(c.target.Section) {
			t.Fatalf("%q: section token = %q, want %q", cmd, act.secTok, sectionToken(c.target.Section))
		}
		if act.fileTok != pathToken(c.target.Path) {
			t.Fatalf("%q: file token = %q, want %q", cmd, act.fileTok, pathToken(c.target.Path))
		}
		if act.rest != categoryToken(c.name) {
			t.Fatalf("%q: category token = %q, want %q", cmd, act.rest, categoryToken(c.name))
		}
	}
}

// Two sections holding a category of the same name must not produce the same
// link: the entries live in different files.
func TestViewCommandsDifferPerTarget(t *testing.T) {
	a := listTarget{Section: "main", Type: lists.TypeDomain, Path: "/etc/lst/domain_list.lst"}
	b := listTarget{Section: "youtube", Type: lists.TypeDomain, Path: "/etc/lst/yt_domain.lst"}
	if viewCategoryCommand(a, "YouTube") == viewCategoryCommand(b, "YouTube") {
		t.Fatal("commands for different targets must differ")
	}
	if viewCategoryCallback(a, "YouTube") == viewCategoryCallback(b, "YouTube") {
		t.Fatal("callbacks for different targets must differ")
	}
}

// The add command must carry the operation it belongs to, so a tap in an older
// message is applied to that message's list rather than the newest one.
func TestParseAddCommandCarriesOperation(t *testing.T) {
	for _, name := range []string{"YouTube", "Реклама", lists.Uncategorized} {
		cmd := addCategoryCommand("a1b2c3d4", lists.TypeDomain, name)
		gotType, opID, token, ok := parseAddCommand(cmd)
		if !ok {
			t.Fatalf("%q: not parsed", cmd)
		}
		if gotType != lists.TypeDomain {
			t.Fatalf("%q: wrong list type %v", cmd, gotType)
		}
		if opID != "a1b2c3d4" {
			t.Fatalf("%q: op id = %q", cmd, opID)
		}
		if token != categoryToken(name) {
			t.Fatalf("%q: token = %q, want %q", cmd, token, categoryToken(name))
		}
	}

	// Two operations picking the same category produce distinct commands.
	if addCategoryCommand("aaaaaaaa", lists.TypeDomain, "X") == addCategoryCommand("bbbbbbbb", lists.TypeDomain, "X") {
		t.Fatal("commands for different operations must differ")
	}

	// Telegram caps a command at 32 characters after the slash.
	if got := len(addCategoryCommand("a1b2c3d4", lists.TypeDomain, "YouTube")) - 1; got > 32 {
		t.Fatalf("command too long: %d characters", got)
	}
}

// A category button carries no session, so it must survive in an old message:
// the callback data has to round-trip on its own.
func TestParseViewCategoryCallbackRoundTrip(t *testing.T) {
	for _, c := range []struct {
		name   string
		target listTarget
	}{
		{"YouTube", listTarget{Section: "main", Type: lists.TypeDomain, Path: "/etc/lst/domain_list.lst"}},
		{"Реклама", listTarget{Section: "Exclude", Type: lists.TypeIP, Path: "/etc/lst/ip_list.lst"}},
		{lists.Uncategorized, listTarget{Type: lists.TypeDomain, Path: "/etc/lst/domain_list.lst"}},
	} {
		data := viewCategoryCallback(c.target, c.name)
		if len(data) > 64 {
			t.Fatalf("%q: callback data over Telegram's 64-byte limit", data)
		}
		act, ok := parseTargetAction(strings.TrimPrefix(data, menuCbPrefix))
		if !ok {
			t.Fatalf("%q: not parsed", data)
		}
		if act.verb != verbViewCat {
			t.Fatalf("%q: verb = %q", data, act.verb)
		}
		if act.typ != c.target.Type {
			t.Fatalf("%q: list type = %v, want %v", data, act.typ, c.target.Type)
		}
		if act.secTok != sectionToken(c.target.Section) || act.fileTok != pathToken(c.target.Path) {
			t.Fatalf("%q: target tokens = %q/%q", data, act.secTok, act.fileTok)
		}
		if act.rest != categoryToken(c.name) {
			t.Fatalf("%q: category token = %q, want %q", data, act.rest, categoryToken(c.name))
		}
	}

	// The other menu actions must not be mistaken for a target button.
	for _, bad := range []string{"main_menu", "settings", "toggle_auto_restart", "upgrade_go", "manage", "sec_a1b2c3d4", "cat_x_a1b2c3d4"} {
		if _, ok := parseTargetAction(bad); ok {
			t.Fatalf("expected %q to be rejected", bad)
		}
	}
}

// Every button of the section card has to round-trip as well.
func TestParseTargetActionVerbs(t *testing.T) {
	for _, verb := range []string{verbDownload, verbView, verbViewAll, verbViewCats, verbBind} {
		data := targetCallback(verb, lists.TypeIP, sectionToken("main"), pathToken("/etc/lst/ip_list.lst"))
		if len(data) > 64 {
			t.Fatalf("%q: callback data over Telegram's 64-byte limit", data)
		}
		act, ok := parseTargetAction(strings.TrimPrefix(data, menuCbPrefix))
		if !ok {
			t.Fatalf("%q: not parsed", data)
		}
		if act.verb != verb || act.typ != lists.TypeIP {
			t.Fatalf("%q: got verb %q type %v", data, act.verb, act.typ)
		}
	}

	// A verb without a file is how a screen says "the section's only file".
	act, ok := parseTargetAction("view_d_a1b2c3d4")
	if !ok || act.fileTok != "" {
		t.Fatalf("expected a fileless action, got %+v ok=%v", act, ok)
	}
}

func TestParseCommandTolerance(t *testing.T) {
	// Telegram appends "@botname" in groups and may leave trailing text.
	act, ok := parseViewCommand("/cd_a1b2c3d4_e5f6a7b8_9c0d1e2f@mybot  ")
	if !ok || act.secTok != "a1b2c3d4" || act.fileTok != "e5f6a7b8" || act.rest != "9c0d1e2f" {
		t.Fatalf("botname suffix not handled: %+v ok=%v", act, ok)
	}
	for _, bad := range []string{"", "/cd_", "/cx_abc", "/cdabc", "hello", "/gd_op_cat", "/cd_op_cat", "/cd_a1b2c3d4"} {
		if _, ok := parseViewCommand(bad); ok {
			t.Fatalf("expected %q to be rejected as a view command", bad)
		}
	}
	for _, bad := range []string{"", "/gd_", "/gd_onlyone", "/gx_op_cat", "/cd_a1b2c3d4"} {
		if _, _, _, ok := parseAddCommand(bad); ok {
			t.Fatalf("expected %q to be rejected as an add command", bad)
		}
	}
}

func TestViewAndAddCommandsDoNotCollide(t *testing.T) {
	view := viewCategoryCommand(listTarget{Section: "main", Type: lists.TypeDomain, Path: "/etc/lst/domain_list.lst"}, "YouTube")
	add := addCategoryCommand("a1b2c3d4", lists.TypeDomain, "YouTube")
	if view == add {
		t.Fatal("view and add commands must differ")
	}
	if _, ok := parseViewCommand(add); ok {
		t.Fatal("an add command must not parse as a view command")
	}
	if _, _, _, ok := parseAddCommand(view); ok {
		t.Fatal("a view command must not parse as an add command")
	}
}

func TestBuildListRenderPacksMessages(t *testing.T) {
	blocks := []string{strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)}
	cats := []lists.CategoryInfo{{Name: "A"}, {Name: "B"}, {Name: "C"}}

	render := buildListRender("head", blocks, cats, 100)
	if len(render.Oversized) != 0 {
		t.Fatalf("nothing should be oversized: %+v", render.Oversized)
	}
	if len(render.Messages) != 2 {
		t.Fatalf("want 2 messages, got %d: %v", len(render.Messages), render.Messages)
	}
	for i, m := range render.Messages {
		if len(m) > 100 {
			t.Fatalf("message %d exceeds the limit: %d", i, len(m))
		}
	}
	// Every block must survive somewhere.
	joined := strings.Join(render.Messages, "")
	for _, b := range blocks {
		if !strings.Contains(joined, b) {
			t.Fatal("a block was dropped")
		}
	}
}

func TestBuildListRenderReportsOversized(t *testing.T) {
	blocks := []string{"small", strings.Repeat("x", 500)}
	cats := []lists.CategoryInfo{{Name: "Small"}, {Name: "Huge"}}

	render := buildListRender("head", blocks, cats, 100)
	if len(render.Oversized) != 1 || render.Oversized[0].Name != "Huge" {
		t.Fatalf("unexpected oversized: %+v", render.Oversized)
	}
	if len(render.Messages) != 1 || !strings.Contains(render.Messages[0], "small") {
		t.Fatalf("unexpected messages: %v", render.Messages)
	}
}

func TestCategoryBlockEscapesAndCollapses(t *testing.T) {
	info := lists.CategoryInfo{Name: "A<b>", Active: 1, Disabled: 1}
	entries := []lists.LineEntry{
		{Value: "ok.com"},
		{Value: "off.com", Disabled: true},
	}

	got := categoryBlock(info, entries)
	if strings.Contains(got, "<b>A<b>") {
		t.Fatalf("category name was not escaped: %q", got)
	}
	if !strings.Contains(got, "A&lt;b&gt;") {
		t.Fatalf("expected escaped name, got %q", got)
	}
	if !strings.Contains(got, "<blockquote expandable>") {
		t.Fatalf("expected an expandable quote, got %q", got)
	}
	if !strings.Contains(got, "⏸ off.com") {
		t.Fatalf("disabled entry not marked: %q", got)
	}
}

func TestCategoryPickerTextListsCommands(t *testing.T) {
	cats := []lists.CategoryInfo{
		{Name: "YouTube", Active: 12, Disabled: 3},
		{Name: lists.Uncategorized, Active: 7},
	}

	got := categoryPickerText("Заголовок", "Подсказка:", cats,
		func(name string) string { return addCategoryCommand("a1b2c3d4", lists.TypeDomain, name) })

	for _, want := range []string{
		"Заголовок",
		"Подсказка:",
		addCategoryCommand("a1b2c3d4", lists.TypeDomain, "YouTube"),
		"YouTube — 12 ✅ / 3 ⏸",
		lists.UncategorizedLabel + " — 7 ✅",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("picker text missing %q:\n%s", want, got)
		}
	}
}

func categoryButtons(rows [][]models.InlineKeyboardButton) []models.InlineKeyboardButton {
	var out []models.InlineKeyboardButton
	for _, row := range rows {
		out = append(out, row...)
	}
	return out
}

func TestCategoryPickRowsKeepsEveryCategory(t *testing.T) {
	cats := []lists.CategoryInfo{
		{Name: "YouTube", Active: 12, Disabled: 3},
		{Name: lists.Uncategorized, Active: 7},
	}

	rows, ok := categoryPickRows(cats, func(name string) string {
		return cbPrefix + "a1b2c3d4:" + cbPickPrefix + categoryToken(name)
	})
	if !ok {
		t.Fatal("a short list must fit the keyboard")
	}

	buttons := categoryButtons(rows)
	if len(buttons) != len(cats) {
		t.Fatalf("want %d buttons, got %d", len(cats), len(buttons))
	}
	for i, want := range []string{"YouTube · 12✅ 3⏸", lists.UncategorizedLabel + " · 7✅"} {
		if buttons[i].Text != want {
			t.Fatalf("button %d: text = %q, want %q", i, buttons[i].Text, want)
		}
		if len(buttons[i].CallbackData) > 64 {
			t.Fatalf("button %d: callback data over Telegram's 64-byte limit: %q", i, buttons[i].CallbackData)
		}
	}

	// An empty list still fits: the picker adds "new category" itself.
	if _, ok := categoryPickRows(nil, func(string) string { return "x" }); !ok {
		t.Fatal("an empty list must fit the keyboard")
	}

	// Too many categories fall back to the command list.
	many := make([]lists.CategoryInfo, categoryButtonLimit+1)
	for i := range many {
		many[i] = lists.CategoryInfo{Name: fmt.Sprintf("cat-%d", i), Active: 1}
	}
	if _, ok := categoryPickRows(many, func(string) string { return "x" }); ok {
		t.Fatal("a list past the limit must fall back to commands")
	}
}

// Short categories share a row so more of them fit on screen; a long name
// keeps a row of its own instead of squeezing its neighbours.
func TestCategoryPickRowsPacksShortNames(t *testing.T) {
	short := make([]lists.CategoryInfo, 6)
	for i := range short {
		short[i] = lists.CategoryInfo{Name: fmt.Sprintf("к%d", i), Active: 1}
	}
	rows, _ := categoryPickRows(short, func(string) string { return "x" })
	if len(rows) != 2 {
		t.Fatalf("want 6 short categories on 2 rows, got %d rows: %v", len(rows), rows)
	}
	for i, row := range rows {
		if len(row) != categoryRowMax {
			t.Fatalf("row %d: want %d buttons, got %d", i, categoryRowMax, len(row))
		}
	}

	long := []lists.CategoryInfo{
		{Name: strings.Repeat("длинное имя ", 3), Active: 1},
		{Name: "к1", Active: 1},
		{Name: "к2", Active: 1},
	}
	rows, _ = categoryPickRows(long, func(string) string { return "x" })
	if len(rows[0]) != 1 {
		t.Fatalf("a long name must not share its row: %v", rows[0])
	}
	if got := len(categoryButtons(rows)); got != len(long) {
		t.Fatalf("want %d buttons, got %d", len(long), got)
	}
}

// An empty list used to say "нажмите на команду с нужной категорией" with no
// commands under it.
func TestCategoryPickHintCoversTheEmptyList(t *testing.T) {
	empty := categoryPickHint(0)
	if !strings.Contains(empty, "нет") {
		t.Fatalf("empty hint must say there are no categories yet, got %q", empty)
	}
	if strings.Contains(empty, "Выберите") {
		t.Fatalf("empty hint must not ask to pick one, got %q", empty)
	}
	if got := categoryPickHint(3); got == empty {
		t.Fatal("a non-empty list needs its own hint")
	}
}

func TestPluralEntries(t *testing.T) {
	cases := map[int]string{
		0:   "0 записей",
		1:   "1 запись",
		3:   "3 записи",
		5:   "5 записей",
		11:  "11 записей",
		14:  "14 записей",
		21:  "21 запись",
		22:  "22 записи",
		25:  "25 записей",
		111: "111 записей",
	}
	for n, want := range cases {
		if got := pluralEntries(n); got != want {
			t.Fatalf("pluralEntries(%d) = %q, want %q", n, got, want)
		}
	}
}
