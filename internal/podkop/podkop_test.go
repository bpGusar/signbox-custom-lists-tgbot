package podkop

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"lst-signbox-lists-tgbot/internal/lists"
)

// fakeUCI replaces the uci binary with a canned config, recording every call.
type fakeUCI struct {
	show   string
	calls  [][]string
	failOn string
}

func (f *fakeUCI) install(t *testing.T) {
	t.Helper()
	prev := run
	t.Cleanup(func() { run = prev })
	run = func(_ context.Context, args ...string) (string, error) {
		f.calls = append(f.calls, args)
		if f.failOn != "" && strings.Join(args, " ") == f.failOn {
			return "", errors.New("uci failed")
		}
		if len(args) == 2 && args[0] == "show" {
			return f.show, nil
		}
		return "", nil
	}
}

// Trimmed from a real router, keeping the shapes that break naive parsers: a
// global section that is not a routing one, a UCI list with several values, a
// multi-line value, and a line inside that value that mimics a declaration.
const showFixture = `podkop.settings=settings
podkop.settings.dns_type='udp'
podkop.settings.download_lists_via_proxy_section='main'
podkop.main=section
podkop.main.connection_type='proxy'
podkop.main.proxy_config_type='urltest'
podkop.main.urltest_proxy_links='vless://a@1.2.3.4:8443#%E2%9A%A1%20one' 'vless://b@1.2.3.4:8443#%E2%9A%A1%20two'
podkop.main.selector_proxy_links='vless://stale@9.9.9.9:443#old'
podkop.main.user_domain_list_type='disabled'
podkop.main.local_domain_lists='/etc/lst/domain_list.lst' '/etc/lst/extra.lst'
podkop.main.local_subnet_lists='/etc/lst/ip_list.lst'
podkop.main.community_lists='telegram' 'discord'
podkop.Exclude=section
podkop.Exclude.connection_type='exclusion'
podkop.Exclude.user_domain_list_type='text'
podkop.Exclude.user_domains_text='spotify.com
podkop.fake=section
podkop.fake.local_domain_lists='/etc/passwd'
test.com'
podkop.youtube=section
podkop.youtube.connection_type='proxy'
podkop.youtube.proxy_config_type='url'
podkop.youtube.proxy_string='vless://single@7.7.7.7:443#%E2%9A%A1%20single'
podkop.youtube.local_subnet_lists='/etc/lst/yt_ip.lst'
`

func TestSections(t *testing.T) {
	f := &fakeUCI{show: showFixture}
	f.install(t)

	got, err := Sections(context.Background())
	if err != nil {
		t.Fatalf("Sections: %v", err)
	}

	want := []Section{
		{
			Name:            "main",
			ConnectionType:  "proxy",
			DomainLists:     []string{"/etc/lst/domain_list.lst", "/etc/lst/extra.lst"},
			SubnetLists:     []string{"/etc/lst/ip_list.lst"},
			ProxyConfigType: "urltest",
			// selector_proxy_links is in the config too, and must be
			// ignored: only the option matching the type is in use.
			ProxyLinks: []string{
				"vless://a@1.2.3.4:8443#%E2%9A%A1%20one",
				"vless://b@1.2.3.4:8443#%E2%9A%A1%20two",
			},
		},
		{Name: "Exclude", ConnectionType: "exclusion"},
		{
			Name:            "youtube",
			ConnectionType:  "proxy",
			SubnetLists:     []string{"/etc/lst/yt_ip.lst"},
			ProxyConfigType: "url",
			ProxyString:     "vless://single@7.7.7.7:443#%E2%9A%A1%20single",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sections mismatch:\n got %+v\nwant %+v", got, want)
	}

	// The whole config is read in one call: a uci process per option is
	// noticeable on the routers this runs on.
	if len(f.calls) != 1 {
		t.Fatalf("expected a single uci call, got %v", f.calls)
	}
}

// Lines inside a multi-line value must not be read as config: a domain list a
// user pasted into podkop could otherwise declare sections and bind files.
func TestSectionsIgnoresValueLines(t *testing.T) {
	f := &fakeUCI{show: showFixture}
	f.install(t)

	got, err := Sections(context.Background())
	if err != nil {
		t.Fatalf("Sections: %v", err)
	}
	for _, s := range got {
		if s.Name == "fake" {
			t.Fatalf("a line inside user_domains_text was read as a section: %+v", got)
		}
		for _, p := range append(append([]string{}, s.DomainLists...), s.SubnetLists...) {
			if p == "/etc/passwd" {
				t.Fatalf("a line inside user_domains_text bound a file: %+v", got)
			}
		}
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 sections, got %d: %+v", len(got), got)
	}
}

func TestSectionsUCIMissing(t *testing.T) {
	f := &fakeUCI{failOn: "show podkop"}
	f.install(t)

	if _, err := Sections(context.Background()); err == nil {
		t.Fatal("expected an error when uci show fails")
	}
	if Available(context.Background()) {
		t.Fatal("Available must be false when uci show fails")
	}
}

func TestSectionLists(t *testing.T) {
	s := Section{DomainLists: []string{"/d.lst"}, SubnetLists: []string{"/i.lst"}}
	if got := s.Lists(lists.TypeDomain); !reflect.DeepEqual(got, []string{"/d.lst"}) {
		t.Fatalf("domain lists: %v", got)
	}
	if got := s.Lists(lists.TypeIP); !reflect.DeepEqual(got, []string{"/i.lst"}) {
		t.Fatalf("subnet lists: %v", got)
	}
}

func TestParseValues(t *testing.T) {
	cases := []struct {
		raw  string
		want []string
	}{
		{"'/a.lst'", []string{"/a.lst"}},
		{"'/a.lst' '/b.lst'", []string{"/a.lst", "/b.lst"}},
		{"proxy", []string{"proxy"}},
		{"", nil},
		{"''", nil},
	}
	for _, c := range cases {
		if got := parseValues(c.raw); !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseValues(%q) = %v, want %v", c.raw, got, c.want)
		}
	}
}

func TestBind(t *testing.T) {
	f := &fakeUCI{}
	f.install(t)

	if err := Bind(context.Background(), "youtube", lists.TypeDomain, "/etc/lst/yt.lst"); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	want := [][]string{
		{"add_list", "podkop.youtube.local_domain_lists=/etc/lst/yt.lst"},
		{"commit", "podkop"},
	}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("uci calls mismatch:\n got %v\nwant %v", f.calls, want)
	}
}

func TestBindRejectsBadInput(t *testing.T) {
	f := &fakeUCI{}
	f.install(t)

	if err := Bind(context.Background(), "main; reboot", lists.TypeDomain, "/etc/lst/a.lst"); err == nil {
		t.Fatal("expected a bad section name to be rejected")
	}
	if err := Bind(context.Background(), "main", lists.TypeDomain, "/etc/../root/.ssh/authorized_keys"); err == nil {
		t.Fatal("expected a traversing path to be rejected")
	}
	if len(f.calls) != 0 {
		t.Fatalf("nothing should have reached uci, got %v", f.calls)
	}
}

func TestValidatePath(t *testing.T) {
	ok := []string{"/etc/lst-signbox-lists-tgbot/domain_list.lst", "/tmp/a.lst", "/etc/podkop/main_domains.lst"}
	for _, p := range ok {
		if err := ValidatePath(p); err != nil {
			t.Errorf("ValidatePath(%q) = %v, want nil", p, err)
		}
	}
	bad := []string{
		"",
		"etc/domain.lst",
		"/etc/lists/",
		"/etc/../etc/passwd",
		"/etc/domain list.lst",
		"/etc/$(reboot).lst",
		"/etc/'x'.lst",
		"/etc/списки.lst",
		"/etc/a\nb.lst",
		"/" + strings.Repeat("a", maxPathLen),
	}
	for _, p := range bad {
		if err := ValidatePath(p); err == nil {
			t.Errorf("ValidatePath(%q) = nil, want an error", p)
		}
	}
}
