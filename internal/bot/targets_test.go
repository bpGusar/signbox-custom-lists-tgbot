package bot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"lst-signbox-lists-tgbot/internal/config"
	"lst-signbox-lists-tgbot/internal/lists"
	"lst-signbox-lists-tgbot/internal/podkop"
)

// testApp stands in for a router: the sections are whatever the test says
// they are, and the config keeps the fallback pair meaningful.
func testApp(secs []podkop.Section, err error) *App {
	return &App{
		cfg: &config.Config{
			DomainList: "/etc/lst-signbox-lists-tgbot/domain_list.lst",
			IPList:     "/etc/lst-signbox-lists-tgbot/ip_list.lst",
		},
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

// Without a podkop config to read, the bot must still reach the file pair from
// its own settings — otherwise a plain sing-box install has no way in.
func TestSectionsFallsBackToConfig(t *testing.T) {
	a := testApp(nil, errors.New("uci missing"))

	secs := a.sections(context.Background())
	if len(secs) != 1 || secs[0].Name != "" {
		t.Fatalf("expected one synthetic section, got %+v", secs)
	}
	if got := secs[0].Lists(lists.TypeDomain); !reflect.DeepEqual(got, []string{a.cfg.DomainList}) {
		t.Fatalf("fallback domain list = %v", got)
	}
	if got := secs[0].Lists(lists.TypeIP); !reflect.DeepEqual(got, []string{a.cfg.IPList}) {
		t.Fatalf("fallback subnet list = %v", got)
	}
	if sectionToken("") != fallbackSectionToken {
		t.Fatalf("fallback token = %q", sectionToken(""))
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

func TestSectionsWithSkipsUnboundSections(t *testing.T) {
	a := testApp(sampleSections, nil)

	var names []string
	for _, s := range a.sectionsWith(context.Background(), lists.TypeDomain) {
		names = append(names, s.Name)
	}
	if !reflect.DeepEqual(names, []string{"main", "youtube"}) {
		t.Fatalf("sections with a domain list = %v", names)
	}

	names = nil
	for _, s := range a.sectionsWith(context.Background(), lists.TypeIP) {
		names = append(names, s.Name)
	}
	if !reflect.DeepEqual(names, []string{"main"}) {
		t.Fatalf("sections with a subnet list = %v", names)
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
	if got := sectionButtonLabel(podkop.Section{SubnetLists: []string{"/x.lst"}}); got != fallbackSectionName+" · IP" {
		t.Errorf("fallback label = %q", got)
	}
}

// A section with only one of the two lists offers to bind the other, and the
// synthetic fallback section offers nothing: it is not in podkop's config.
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

	if rows := a.sectionActionRows(a.fallbackSection(), lists.TypeDomain, fallbackSectionToken); len(rows) != 1 {
		t.Fatalf("the fallback pair is bound, so it gets buttons: %+v", rows)
	}
	empty := podkop.Section{}
	if rows := a.sectionActionRows(empty, lists.TypeIP, fallbackSectionToken); rows != nil {
		t.Fatalf("nothing to bind outside podkop, got %+v", rows)
	}
}

func TestSuggestBindPath(t *testing.T) {
	a := testApp(sampleSections, nil)

	if got, want := a.suggestBindPath("youtube", lists.TypeDomain), "/etc/lst-signbox-lists-tgbot/youtube_domain_list.lst"; got != want {
		t.Errorf("suggested path = %q, want %q", got, want)
	}
	if got, want := a.suggestBindPath("main", lists.TypeIP), "/etc/lst-signbox-lists-tgbot/main_ip_list.lst"; got != want {
		t.Errorf("suggested path = %q, want %q", got, want)
	}
	// Whatever is suggested must pass the check the binding itself applies.
	if err := podkop.ValidatePath(a.suggestBindPath("youtube", lists.TypeDomain)); err != nil {
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
