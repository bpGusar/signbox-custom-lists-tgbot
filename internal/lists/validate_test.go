package lists

import "testing"

func TestParseInputDomains(t *testing.T) {
	r := ParseInput("test.com, example.org")
	if r.Mixed || len(r.Invalid) > 0 || r.Type != TypeDomain {
		t.Fatalf("unexpected: %+v", r)
	}
	if len(r.Valid) != 2 {
		t.Fatalf("want 2 valid, got %d", len(r.Valid))
	}
}

func TestParseInputIP(t *testing.T) {
	r := ParseInput("1.1.1.1, 10.0.0.0/8")
	if r.Type != TypeIP || len(r.Valid) != 2 {
		t.Fatalf("unexpected: %+v", r)
	}
}

func TestParseInputMixed(t *testing.T) {
	r := ParseInput("test.com, 1.1.1.1")
	if !r.Mixed {
		t.Fatal("expected mixed")
	}
	if r.Empty {
		t.Fatal("mixed input must not be empty")
	}
}

func TestParseInputMixedMultiline(t *testing.T) {
	r := ParseInput("example.com, github.com\nkick.com\n192.168.1.0/24")
	if !r.Mixed {
		t.Fatalf("expected mixed, got %+v", r)
	}
	if r.Empty {
		t.Fatalf("mixed input must not be empty, got %+v", r)
	}
}

func TestParseInputMixedUserReport(t *testing.T) {
	r := ParseInput("kick.com\nrevanced.app\n104.20.32.0/20")
	if !r.Mixed {
		t.Fatalf("expected mixed, got %+v", r)
	}
	if r.Empty {
		t.Fatalf("mixed input must not be empty, got %+v", r)
	}
	if len(r.Invalid) > 0 {
		t.Fatalf("unexpected invalid: %+v", r.Invalid)
	}
}

func TestParseInputDisabledLines(t *testing.T) {
	r := ParseInput("active.com\n// disabled.com")
	if r.Mixed || len(r.Invalid) > 0 || r.Type != TypeDomain {
		t.Fatalf("unexpected: %+v", r)
	}
	if len(r.Valid) != 2 {
		t.Fatalf("valid: %+v", r.Valid)
	}
	if r.Valid[0] != "active.com" || r.Valid[1] != "disabled.com" {
		t.Fatalf("valid: %+v", r.Valid)
	}
}

func TestParseInputPastedList(t *testing.T) {
	input := `4pda.to
codebeautify.org
cursor-cdn.com
// kick.com
xdaforums.com`
	r := ParseInput(input)
	if r.Mixed || len(r.Invalid) > 0 || r.Type != TypeDomain {
		t.Fatalf("unexpected: %+v", r)
	}
	if len(r.Valid) != 5 {
		t.Fatalf("want 5 entries, got %d: %v", len(r.Valid), r.Valid)
	}
	if r.Valid[3] != "kick.com" {
		t.Fatalf("kick.com should be normalized: %+v", r.Valid)
	}
}

func TestParseInputInlineComment(t *testing.T) {
	r := ParseInput("a.com, // b.com")
	if len(r.Valid) != 2 || r.Valid[0] != "a.com" || r.Valid[1] != "b.com" {
		t.Fatalf("unexpected: %+v", r)
	}
}

func TestSortDomainLines(t *testing.T) {
	lines := []string{
		"www.zebra.com",
		"zebra.com",
		"// apple.com",
		"api.beta.com",
		"alpha.org",
	}
	got := sortDomainLines(lines)
	// Grouped by base domain (eTLD+1) first, alphabetically: alpha.org,
	// then api.beta.com (base domain "beta.com"), then the zebra.com group
	// where "www.zebra.com" sorts before "zebra.com" itself.
	want := []string{
		"alpha.org",
		"// apple.com",
		"api.beta.com",
		"www.zebra.com",
		"zebra.com",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestBaseDomain(t *testing.T) {
	cases := map[string]string{
		"kick.com":           "kick.com",
		"cdn.kick.com":       "kick.com",
		"api.github.com":     "github.com",
		"www.api.example.org": "example.org",
	}
	for host, want := range cases {
		if got := baseDomain(host); got != want {
			t.Fatalf("baseDomain(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestAddDisableDelete(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/list.lst"

	if err := AddNew(path, []string{"a.com", "b.com"}, TypeDomain); err != nil {
		t.Fatal(err)
	}
	if err := DisableExistingOnly(path, []string{"a.com"}, TypeDomain); err != nil {
		t.Fatal(err)
	}

	entries, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || !entries[0].Disabled || entries[1].Disabled {
		t.Fatalf("unexpected entries: %+v", entries)
	}

	classified, err := ClassifyValues(path, []string{"a.com", "c.com"})
	if err != nil {
		t.Fatal(err)
	}
	newV, active, disabled := GroupByStatus(classified)
	if len(disabled) != 1 || len(newV) != 1 || len(active) != 0 {
		t.Fatalf("classified wrong: new=%v active=%v dis=%v", newV, active, disabled)
	}

	if err := AddAll(path, []string{"a.com", "c.com"}, TypeDomain); err != nil {
		t.Fatal(err)
	}
	entries, _ = ReadFile(path)
	if len(entries) != 3 {
		t.Fatalf("want 3 entries, got %d", len(entries))
	}
	if entries[0].Disabled || entries[0].Value != "a.com" {
		t.Fatalf("a.com should be enabled: %+v", entries[0])
	}

	if err := Delete(path, []string{"b.com"}, TypeDomain); err != nil {
		t.Fatal(err)
	}
	entries, _ = ReadFile(path)
	if len(entries) != 2 {
		t.Fatalf("want 2 after delete, got %d", len(entries))
	}
}
