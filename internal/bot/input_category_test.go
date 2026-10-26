package bot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lst-signbox-lists-tgbot/internal/lists"
)

func TestAddByMessageCategories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "domain_list.lst")
	parsed := lists.ParseInput("// #cat: xda\nxda-developers.com\nxdaforums.com\n// #cat: cursor\ncursor-cdn.com\ncursor.com\ncursor.sh")
	op := &PendingOp{ListType: parsed.Type, ListPath: path, Values: parsed.Valid, ValueCategories: parsed.Categories}

	if got, want := categoriesLine(op, op.Values), "📂 Категории: xda (2), cursor (3)"; got != want {
		t.Fatalf("categories line = %q, want %q", got, want)
	}

	if err := writeByCategory(op, op.Values, func(vals []string, cat string) error {
		return lists.AddNew(path, vals, op.ListType, cat)
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "// #cat: xda\nxda-developers.com\nxdaforums.com\n// #cat: cursor\ncursor-cdn.com\ncursor.com\ncursor.sh\n"
	if string(data) != want {
		t.Fatalf("file =\n%s\nwant\n%s", data, want)
	}
}

func TestGroupByCategoryFallsBackToOpCategory(t *testing.T) {
	op := &PendingOp{Category: "picked"}
	groups := groupByCategory(op, []string{"a.com", "b.com"})
	if len(groups) != 1 || groups[0].category != "picked" || strings.Join(groups[0].values, ",") != "a.com,b.com" {
		t.Fatalf("groups = %+v", groups)
	}
	if categoriesLine(op, []string{"a.com"}) != "" {
		t.Fatal("no categories line expected without headers")
	}
}

func TestListStartingWithCommentIsNotCommand(t *testing.T) {
	if isCommand("// #cat: netflix\nnetflix.com") || isCommand("// kick.com") {
		t.Fatal("a list opening with // must reach the list parser")
	}
	if !isCommand("/menu") {
		t.Fatal("/menu is a command")
	}
}
