package bot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"lst-signbox-lists-tgbot/internal/config"
	"lst-signbox-lists-tgbot/internal/lists"
	"lst-signbox-lists-tgbot/internal/podkop"
)

// testApp stands in for a router: the sections are whatever the test says
// they are.
func testApp(secs []podkop.Section, err error) *App {
	return &App{
		cfg:          &config.Config{},
		readSections: func(context.Context) ([]podkop.Section, error) { return secs, err },
	}
}

var sampleSections = []podkop.Section{
	{
		Name:           "main",
		ConnectionType: "proxy",
		DomainLists:    []string{"/etc/lst/domain_list.lst", "/etc/lst/extra.lst"},
		SubnetLists:    []string{"/etc/lst/ip_list.lst"},
	},
	{Name: "Exclude", ConnectionType: "exclusion"},
	{Name: "youtube", ConnectionType: "proxy", DomainLists: []string{"/etc/lst/domain_list.lst"}},
}

func TestSectionsWithoutConfig(t *testing.T) {
	a := testApp(nil, errors.New("uci missing"))
	if secs := a.sections(context.Background()); len(secs) != 0 {
		t.Fatalf("expected no sections, got %+v", secs)
	}
}

func TestResolveTarget(t *testing.T) {
	a := testApp(sampleSections, nil)
	ctx := context.Background()

	// A file picked out of a section that holds several.
	tgt, err := a.resolveTarget(ctx, sectionToken("main"), pathToken("/etc/lst/extra.lst"), lists.TypeDomain)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if tgt.Section != "main" || tgt.Path != "/etc/lst/extra.lst" {
		t.Fatalf("resolved to %+v", tgt)
	}

	// No file token means "the only one", which is unambiguous here.
	tgt, err = a.resolveTarget(ctx, sectionToken("main"), "", lists.TypeIP)
	if err != nil || tgt.Path != "/etc/lst/ip_list.lst" {
		t.Fatalf("single-file resolve: %+v err=%v", tgt, err)
	}

	// …but ambiguous here, and guessing would write to the wrong file.
	if _, err := a.resolveTarget(ctx, sectionToken("main"), "", lists.TypeDomain); !errors.Is(err, errFileGone) {
		t.Fatalf("ambiguous resolve err = %v, want errFileGone", err)
	}

	if _, err := a.resolveTarget(ctx, sectionToken("Exclude"), "", lists.TypeDomain); !errors.Is(err, errNotBound) {
		t.Fatalf("unbound resolve err = %v, want errNotBound", err)
	}
	if _, err := a.resolveTarget(ctx, sectionToken("gone"), "", lists.TypeDomain); !errors.Is(err, errSectionGone) {
		t.Fatalf("missing section err = %v, want errSectionGone", err)
	}
	if _, err := a.resolveTarget(ctx, sectionToken("main"), "deadbeef", lists.TypeDomain); !errors.Is(err, errFileGone) {
		t.Fatalf("missing file err = %v, want errFileGone", err)
	}
}

// One file feeding two sections means an edit reaches further than the screen
// suggests, so the other sections have to be named.
func TestSharedWith(t *testing.T) {
	a := testApp(sampleSections, nil)

	shared := a.sharedWith(context.Background(), listTarget{
		Section: "main", Type: lists.TypeDomain, Path: "/etc/lst/domain_list.lst",
	})
	if !reflect.DeepEqual(shared, []string{"youtube"}) {
		t.Fatalf("shared sections = %v", shared)
	}

	shared = a.sharedWith(context.Background(), listTarget{
		Section: "main", Type: lists.TypeDomain, Path: "/etc/lst/extra.lst",
	})
	if len(shared) != 0 {
		t.Fatalf("expected no sharing, got %v", shared)
	}
}

func TestSectionButtonLabel(t *testing.T) {
	cases := map[string]string{
		"main":    "main · proxy · домены+IP",
		"Exclude": "Exclude · exclusion · нет списков",
		"youtube": "youtube · proxy · домены",
	}
	for _, s := range sampleSections {
		if got := sectionButtonLabel(s); got != cases[s.Name] {
			t.Errorf("label for %q = %q, want %q", s.Name, got, cases[s.Name])
		}
	}
}

// A section with only one of the two lists offers to bind the other.
func TestSectionActionRows(t *testing.T) {
	a := testApp(sampleSections, nil)
	secTok := sectionToken("youtube")
	yt := sampleSections[2]

	rows := a.sectionActionRows(yt, lists.TypeDomain, secTok)
	if len(rows) != 1 || len(rows[0]) != 2 {
		t.Fatalf("expected show+download for a bound list, got %+v", rows)
	}

	rows = a.sectionActionRows(yt, lists.TypeIP, secTok)
	if len(rows) != 1 || len(rows[0]) != 1 {
		t.Fatalf("expected a single bind button, got %+v", rows)
	}
	if want := targetCallback(verbBind, lists.TypeIP, secTok, ""); rows[0][0].CallbackData != want {
		t.Fatalf("bind callback = %q, want %q", rows[0][0].CallbackData, want)
	}
}

func TestSuggestBindPath(t *testing.T) {
	if got, want := suggestBindPath("youtube", lists.TypeDomain), "/etc/lst-signbox-lists-tgbot/youtube_domain_list.lst"; got != want {
		t.Errorf("suggested path = %q, want %q", got, want)
	}
	if got, want := suggestBindPath("main", lists.TypeIP), "/etc/lst-signbox-lists-tgbot/main_ip_list.lst"; got != want {
		t.Errorf("suggested path = %q, want %q", got, want)
	}
	// Whatever is suggested must pass the check the binding itself applies.
	if err := podkop.ValidatePath(suggestBindPath("youtube", lists.TypeDomain)); err != nil {
		t.Errorf("suggested path rejected by ValidatePath: %v", err)
	}
}

// The startup check now covers every file podkop reads, not just the pair from
// the bot's own settings.
func TestMissingFilesCoversEverySection(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "domain_list.lst")
	if err := os.WriteFile(present, []byte("example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	absent := filepath.Join(dir, "ip_list.lst")

	a := testApp([]podkop.Section{
		{Name: "main", DomainLists: []string{present}, SubnetLists: []string{absent}},
		{Name: "youtube", DomainLists: []string{present}},
	}, nil)

	got := a.missingFiles(context.Background())
	if !reflect.DeepEqual(got, []string{absent}) {
		t.Fatalf("missing files = %v, want %v", got, []string{absent})
	}
}

func TestFileLabel(t *testing.T) {
	if got := fileLabel("/etc/lst/domain_list.lst"); got != "domain_list.lst" {
		t.Fatalf("file label = %q", got)
	}
}

// The "куда добавить?" screen always lists every podkop section, including the
// ones with no file for this list yet — that is where a new file gets bound.
func TestAddTargetRows(t *testing.T) {
	rows := addTargetRows(sampleSections, lists.TypeDomain, "op1")
	got := make([]string, 0, len(rows))
	for _, row := range rows {
		got = append(got, row[0].Text)
	}
	want := []string{"🗂 main · 2 файла", "🔗 Exclude · нет файла", "🗂 youtube"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("domain rows = %v, want %v", got, want)
	}

	rows = addTargetRows(sampleSections, lists.TypeIP, "op1")
	got = got[:0]
	for _, row := range rows {
		got = append(got, row[0].Text)
	}
	want = []string{"🗂 main", "🔗 Exclude · нет файла", "🔗 youtube · нет файла"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ip rows = %v, want %v", got, want)
	}

	// Every row carries the operation, so a tap in an older message still
	// applies to that message's entries.
	for _, row := range rows {
		if !strings.HasPrefix(row[0].CallbackData, cbPrefix+"op1:"+cbSectionPrefix) {
			t.Fatalf("unexpected callback %q", row[0].CallbackData)
		}
	}
}
