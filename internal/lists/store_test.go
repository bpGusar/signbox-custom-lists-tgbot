package lists

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func tempList(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "list.lst")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readRaw(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A file written before categories existed must round-trip unchanged in shape:
// every entry uncategorized, and no directives added to it.
func TestLegacyFileStaysFlat(t *testing.T) {
	path := tempList(t, "a.com\nb.com\n// c.com\n")

	entries, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("want 3 entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.Category != Uncategorized {
			t.Fatalf("entry %q got category %q, want uncategorized", e.Value, e.Category)
		}
	}

	if err := AddNew(path, []string{"d.com"}, TypeDomain, Uncategorized); err != nil {
		t.Fatal(err)
	}
	want := "a.com\nb.com\n// c.com\nd.com\n"
	if got := readRaw(t, path); got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

// Named categories come first and the uncategorized bucket last, behind an
// explicit marker so re-reading puts the entries back where they were.
func TestCategoryLayoutRoundTrip(t *testing.T) {
	path := tempList(t, "")

	if err := AddNew(path, []string{"plain.com"}, TypeDomain, Uncategorized); err != nil {
		t.Fatal(err)
	}
	if err := AddNew(path, []string{"ytimg.com", "youtube.com"}, TypeDomain, "YouTube"); err != nil {
		t.Fatal(err)
	}
	if err := AddNew(path, []string{"ads.example.com"}, TypeDomain, "Реклама"); err != nil {
		t.Fatal(err)
	}

	want := "// #cat: YouTube\nyoutube.com\nytimg.com\n// #cat: Реклама\nads.example.com\n// #uncat\nplain.com\n"
	if got := readRaw(t, path); got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}

	entries, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	byValue := make(map[string]string, len(entries))
	for _, e := range entries {
		byValue[e.Value] = e.Category
	}
	for value, want := range map[string]string{
		"youtube.com":     "YouTube",
		"ytimg.com":       "YouTube",
		"ads.example.com": "Реклама",
		"plain.com":       Uncategorized,
	} {
		if byValue[value] != want {
			t.Fatalf("%s: got category %q, want %q", value, byValue[value], want)
		}
	}

	// Writing the same content again must not drift.
	if err := AddNew(path, nil, TypeDomain, Uncategorized); err != nil {
		t.Fatal(err)
	}
	if got := readRaw(t, path); got != want {
		t.Fatalf("second write drifted:\n%q", got)
	}
}

// A disabled entry inside a section belongs to that section, not to the
// uncategorized bucket.
func TestDisabledEntryKeepsCategory(t *testing.T) {
	path := tempList(t, "// #cat: YouTube\nyoutube.com\n// ytimg.com\n// #uncat\nplain.com\n")

	cats, err := Categories(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cats) != 2 {
		t.Fatalf("want 2 categories, got %d: %+v", len(cats), cats)
	}
	if cats[0].Name != "YouTube" || cats[0].Active != 1 || cats[0].Disabled != 1 {
		t.Fatalf("unexpected YouTube counts: %+v", cats[0])
	}
	if cats[1].Name != Uncategorized || cats[1].Active != 1 {
		t.Fatalf("uncategorized must come last: %+v", cats[1])
	}
}

func TestSetCategoryEnabled(t *testing.T) {
	path := tempList(t, "// #cat: YouTube\nyoutube.com\nytimg.com\n// #uncat\nplain.com\n")

	changed, err := SetCategoryEnabled(path, "YouTube", false, TypeDomain)
	if err != nil {
		t.Fatal(err)
	}
	if changed != 2 {
		t.Fatalf("want 2 changed, got %d", changed)
	}
	want := "// #cat: YouTube\n// youtube.com\n// ytimg.com\n// #uncat\nplain.com\n"
	if got := readRaw(t, path); got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}

	// The uncategorized bucket is untouched by a category-wide switch.
	entries, _ := ReadFile(path)
	for _, e := range entries {
		if e.Value == "plain.com" && e.Disabled {
			t.Fatal("plain.com must stay enabled")
		}
	}

	if changed, err = SetCategoryEnabled(path, "youtube", true, TypeDomain); err != nil {
		t.Fatal(err)
	}
	if changed != 2 {
		t.Fatalf("want 2 re-enabled, got %d", changed)
	}
	if got := readRaw(t, path); got != "// #cat: YouTube\nyoutube.com\nytimg.com\n// #uncat\nplain.com\n" {
		t.Fatalf("re-enable failed: %q", got)
	}
}

func TestDeleteCategoryKeepEntries(t *testing.T) {
	path := tempList(t, "// #cat: YouTube\nyoutube.com\n// #uncat\nplain.com\n")

	moved, err := DeleteCategoryKeepEntries(path, "YouTube", TypeDomain)
	if err != nil {
		t.Fatal(err)
	}
	if moved != 1 {
		t.Fatalf("want 1 moved, got %d", moved)
	}
	// With no named categories left the marker is unnecessary.
	if got := readRaw(t, path); got != "plain.com\nyoutube.com\n" {
		t.Fatalf("got %q", got)
	}
}

func TestDeleteCategoryWithEntries(t *testing.T) {
	path := tempList(t, "// #cat: YouTube\nyoutube.com\nytimg.com\n// #uncat\nplain.com\n")

	removed, err := DeleteCategoryWithEntries(path, "YouTube", TypeDomain)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("want 2 removed, got %d", removed)
	}
	if got := readRaw(t, path); got != "plain.com\n" {
		t.Fatalf("got %q", got)
	}

	if _, err := DeleteCategoryWithEntries(path, "YouTube", TypeDomain); !errors.Is(err, ErrCategoryNotFound) {
		t.Fatalf("want ErrCategoryNotFound, got %v", err)
	}
}

func TestRenameCategory(t *testing.T) {
	path := tempList(t, "// #cat: YouTube\nyoutube.com\n// #cat: Ads\nads.example.com\n")

	if err := RenameCategory(path, "YouTube", "Видео", TypeDomain); err != nil {
		t.Fatal(err)
	}
	if got := readRaw(t, path); got != "// #cat: Видео\nyoutube.com\n// #cat: Ads\nads.example.com\n" {
		t.Fatalf("got %q", got)
	}

	if err := RenameCategory(path, "Видео", "ads", TypeDomain); !errors.Is(err, ErrCategoryExists) {
		t.Fatalf("want ErrCategoryExists, got %v", err)
	}
}

func TestMoveToCategoryAndMisplaced(t *testing.T) {
	path := tempList(t, "// #cat: YouTube\nyoutube.com\n// #cat: Ads\nads.example.com\n")

	classified, err := ClassifyValues(path, []string{"ads.example.com", "new.com"})
	if err != nil {
		t.Fatal(err)
	}
	misplaced := Misplaced(classified, "YouTube")
	if len(misplaced) != 1 || misplaced["ads.example.com"] != "Ads" {
		t.Fatalf("unexpected misplaced: %+v", misplaced)
	}

	moved, err := MoveToCategory(path, []string{"ads.example.com"}, "YouTube", TypeDomain)
	if err != nil {
		t.Fatal(err)
	}
	if moved != 1 {
		t.Fatalf("want 1 moved, got %d", moved)
	}
	// "Ads" is left empty and therefore dropped.
	if got := readRaw(t, path); got != "// #cat: YouTube\nads.example.com\nyoutube.com\n" {
		t.Fatalf("got %q", got)
	}
}

func TestMergeCategory(t *testing.T) {
	path := tempList(t, "// #cat: A\na.com\n// #cat: B\nb.com\n")

	moved, err := MergeCategory(path, "A", "B", TypeDomain)
	if err != nil {
		t.Fatal(err)
	}
	if moved != 1 {
		t.Fatalf("want 1 moved, got %d", moved)
	}
	if got := readRaw(t, path); got != "// #cat: B\na.com\nb.com\n" {
		t.Fatalf("got %q", got)
	}
}

// Adding a value that already sits elsewhere must not duplicate it or silently
// change its category.
func TestAddNewKeepsExistingCategory(t *testing.T) {
	path := tempList(t, "// #cat: A\na.com\n")

	if err := AddNew(path, []string{"a.com", "b.com"}, TypeDomain, "B"); err != nil {
		t.Fatal(err)
	}
	if got := readRaw(t, path); got != "// #cat: A\na.com\n// #cat: B\nb.com\n" {
		t.Fatalf("got %q", got)
	}
}

// Disable adds the values missing from the file straight into the target
// category, already switched off.
func TestDisableAddsMissingIntoCategory(t *testing.T) {
	path := tempList(t, "// #cat: A\na.com\n")

	if err := Disable(path, []string{"a.com", "b.com"}, TypeDomain, "A"); err != nil {
		t.Fatal(err)
	}
	if got := readRaw(t, path); got != "// #cat: A\n// a.com\n// b.com\n" {
		t.Fatalf("got %q", got)
	}
}

// IP lists keep insertion order; only domains are sorted.
func TestIPListKeepsOrder(t *testing.T) {
	path := tempList(t, "")

	if err := AddNew(path, []string{"10.0.0.1", "1.1.1.1"}, TypeIP, "VPN"); err != nil {
		t.Fatal(err)
	}
	if got := readRaw(t, path); got != "// #cat: VPN\n10.0.0.1\n1.1.1.1\n" {
		t.Fatalf("got %q", got)
	}
}

func TestValidateCategoryName(t *testing.T) {
	if _, err := ValidateCategoryName("  YouTube  "); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name, _ := ValidateCategoryName("  YouTube  "); name != "YouTube" {
		t.Fatalf("want trimmed name, got %q", name)
	}
	for _, bad := range []string{"", "   ", "#cat", "/menu", UncategorizedLabel} {
		if _, err := ValidateCategoryName(bad); err == nil {
			t.Fatalf("expected rejection of %q", bad)
		}
	}
}

// Directive-looking comments must never be mistaken for disabled entries.
func TestDirectiveLinesAreNotEntries(t *testing.T) {
	path := tempList(t, "// #cat: A\na.com\n// #some note\nb.com\n")

	entries, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d: %+v", len(entries), entries)
	}
	// The unknown directive closes the section, like "// #uncat" does.
	if entries[0].Category != "A" || entries[1].Category != Uncategorized {
		t.Fatalf("unexpected categories: %+v", entries)
	}
}

// Merging into a category that does not exist yet is how "/gnew" retargets a
// whole category.
func TestMergeCategoryIntoNewName(t *testing.T) {
	path := tempList(t, "// #cat: A\na.com\n// #uncat\nplain.com\n")

	moved, err := MergeCategory(path, "A", "Fresh", TypeDomain)
	if err != nil {
		t.Fatal(err)
	}
	if moved != 1 {
		t.Fatalf("want 1 moved, got %d", moved)
	}
	if got := readRaw(t, path); got != "// #cat: Fresh\na.com\n// #uncat\nplain.com\n" {
		t.Fatalf("got %q", got)
	}
}

func TestMergeIntoUncategorized(t *testing.T) {
	path := tempList(t, "// #cat: A\na.com\n// #uncat\nplain.com\n")

	moved, err := MergeCategory(path, "A", Uncategorized, TypeDomain)
	if err != nil {
		t.Fatal(err)
	}
	if moved != 1 {
		t.Fatalf("want 1 moved, got %d", moved)
	}
	if got := readRaw(t, path); got != "a.com\nplain.com\n" {
		t.Fatalf("got %q", got)
	}
}
