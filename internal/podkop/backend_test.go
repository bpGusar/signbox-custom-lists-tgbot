package podkop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"lst-signbox-lists-tgbot/internal/lists"
)

func TestMain(m *testing.M) {
	detect = func() (Backend, error) { return Podkop, nil }
	os.Exit(m.Run())
}

func useBackend(t *testing.T, b Backend) {
	t.Helper()
	prev := detect
	t.Cleanup(func() { detect = prev })
	detect = func() (Backend, error) { return b, nil }
}

func TestDetect(t *testing.T) {
	prev := installed
	t.Cleanup(func() { installed = prev })

	cases := []struct {
		podkop, forkop bool
		want           Backend
		err            error
	}{
		{podkop: true, want: Podkop},
		{forkop: true, want: Forkop},
		{podkop: true, forkop: true, err: ErrBothInstalled},
		{err: ErrNotInstalled},
	}
	for _, c := range cases {
		installed = func(b Backend) bool {
			return (b == Podkop && c.podkop) || (b == Forkop && c.forkop)
		}
		got, err := Detect()
		if !errors.Is(err, c.err) || got != c.want {
			t.Errorf("Detect(podkop=%t forkop=%t) = %+v, %v; want %+v, %v", c.podkop, c.forkop, got, err, c.want, c.err)
		}
	}
}

func TestSectionsBackendMissing(t *testing.T) {
	prev := detect
	t.Cleanup(func() { detect = prev })
	detect = func() (Backend, error) { return Backend{}, ErrBothInstalled }

	f := &fakeUCI{show: showFixture}
	f.install(t)
	if _, err := Sections(context.Background()); !errors.Is(err, ErrBothInstalled) {
		t.Fatalf("Sections error = %v, want ErrBothInstalled", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("uci must not be called, got %v", f.calls)
	}
}

// Shaped like "uci show forkop" on OpenWrt 25.12: child sections of other types
// that point back at a section, the action that replaced connection_type, the
// legacy action names forkop still accepts, one list option for both kinds of
// files, and podkop's list options that forkop does not read at all.
const forkopShowFixture = `forkop.settings=settings
forkop.settings.dns_type='udp'
forkop.main=section
forkop.main.label='Main'
forkop.main.enabled='1'
forkop.main.action='connection'
forkop.main.selector_proxy_links='vless://a@1.2.3.4:8443#one' 'vless://b@1.2.3.4:8443#two'
forkop.main.domain_ip_lists='/etc/lst/domain_list.lst' '/etc/lst/ip_list.lst' 'https://example.com/list.lst'
forkop.ut_main=urltest
forkop.ut_main.section='main'
forkop.ut_main.name='Fastest'
forkop.@subscription_url[0]=subscription_url
forkop.@subscription_url[0].section='main'
forkop.legacy=section
forkop.legacy.action='proxy'
forkop.blocked=section
forkop.blocked.action='block'
forkop.blocked.domain_ip_lists='/etc/lst/block.lst'
forkop.blocked.local_subnet_lists='/etc/lst/ignored.lst'
forkop.srv=server
forkop.srv.protocol='vless'
podkop.stray=section
podkop.stray.local_domain_lists='/etc/passwd'
`

func useListFileType(t *testing.T) {
	t.Helper()
	prev := listFileType
	t.Cleanup(func() { listFileType = prev })
	listFileType = func(path string) lists.EntryType {
		if nameSaysIP(path) {
			return lists.TypeIP
		}
		return lists.TypeDomain
	}
}

func TestSectionsForkop(t *testing.T) {
	useBackend(t, Forkop)
	useListFileType(t)
	f := &fakeUCI{show: forkopShowFixture}
	f.install(t)

	got, err := Sections(context.Background())
	if err != nil {
		t.Fatalf("Sections: %v", err)
	}
	want := []Section{
		{
			Name:            "main",
			ConnectionType:  "connection",
			DomainLists:     []string{"/etc/lst/domain_list.lst"},
			SubnetLists:     []string{"/etc/lst/ip_list.lst"},
			ProxyConfigType: ProxyTypeSelector,
			ProxyLinks:      []string{"vless://a@1.2.3.4:8443#one", "vless://b@1.2.3.4:8443#two"},
		},
		{Name: "legacy", ConnectionType: "connection", ProxyConfigType: ProxyTypeSelector},
		{Name: "blocked", ConnectionType: "block", DomainLists: []string{"/etc/lst/block.lst"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sections mismatch:\n got %+v\nwant %+v", got, want)
	}
	if !reflect.DeepEqual(f.calls, [][]string{{"show", "forkop"}}) {
		t.Fatalf("uci calls: %v", f.calls)
	}
	if !got[0].AcceptsProxyLinks() || !got[1].AcceptsProxyLinks() || got[2].AcceptsProxyLinks() {
		t.Fatalf("AcceptsProxyLinks mismatch: %+v", got)
	}
	if got[0].ConvertibleToURLTest() {
		t.Fatal("a forkop section is never switched to urltest")
	}
}

func TestForkopWrites(t *testing.T) {
	useBackend(t, Forkop)
	f := &fakeUCI{show: forkopShowFixture}
	f.install(t)
	ctx := context.Background()

	if err := Bind(ctx, "blocked", lists.TypeIP, "/etc/lst/block_ip.lst"); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	for _, bad := range []struct {
		t    lists.EntryType
		path string
	}{
		{lists.TypeIP, "/etc/lst/block_ip.txt"},
		{lists.TypeIP, "/etc/lst/block.lst"},
		{lists.TypeDomain, "/etc/lst/block_ip.lst"},
	} {
		if err := Bind(ctx, "blocked", bad.t, bad.path); err == nil {
			t.Errorf("Bind(%v, %q) must be refused on forkop", bad.t, bad.path)
		}
	}
	if _, _, err := AddProxyLinks(ctx, "main", []string{"vless://c@5.6.7.8:443#three"}); err != nil {
		t.Fatalf("AddProxyLinks: %v", err)
	}
	if err := ReplaceProxyLinks(ctx, "legacy", []string{"vless://d@5.6.7.8:443#four"}); err != nil {
		t.Fatalf("ReplaceProxyLinks: %v", err)
	}
	if err := ConvertToURLTest(ctx, "main"); err == nil {
		t.Fatal("ConvertToURLTest must refuse a forkop section")
	}
	if _, _, err := AddProxyLinks(ctx, "blocked", []string{"vless://c@5.6.7.8:443#three"}); err == nil {
		t.Fatal("a block section must not accept links")
	}

	var writes []string
	for _, c := range f.calls {
		if c[0] != "show" {
			writes = append(writes, strings.Join(c, " "))
		}
	}
	want := []string{
		"add_list forkop.blocked.domain_ip_lists=/etc/lst/block_ip.lst",
		"commit forkop",
		"add_list forkop.main.selector_proxy_links=vless://c@5.6.7.8:443#three",
		"commit forkop",
		"delete forkop.legacy.selector_proxy_links",
		"add_list forkop.legacy.selector_proxy_links=vless://d@5.6.7.8:443#four",
		"commit forkop",
	}
	if !reflect.DeepEqual(writes, want) {
		t.Fatalf("uci writes mismatch:\n got %q\nwant %q", writes, want)
	}
}

func TestGroupLatencyForkop(t *testing.T) {
	useBackend(t, Forkop)
	prev := cliRun
	t.Cleanup(func() { cliRun = prev })

	cliRun = func(_ context.Context, name string, args ...string) (string, error) {
		if name != "forkop" || strings.Join(args, " ") != "clash_api get_group_latency main-out" {
			t.Errorf("unexpected call: %s %v", name, args)
		}
		return `{"main-1-out": 120}`, nil
	}
	got, err := GroupLatency(context.Background(), "main")
	if err != nil {
		t.Fatalf("GroupLatency: %v", err)
	}
	if !reflect.DeepEqual(got, map[string]int{"main-1-out": 120}) {
		t.Fatalf("latency: %v", got)
	}
}

func TestListFileType(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	cases := []struct {
		path string
		want lists.EntryType
	}{
		{write("a.lst", "// #cat: x\n10.0.0.0/8\n// 1.2.3.4\n"), lists.TypeIP},
		{write("b_ip_list.lst", "example.com\n10.0.0.0/8\n"), lists.TypeDomain},
		{write("main_ip_list.lst", ""), lists.TypeIP},
		{write("main_domain_list.lst", "// #uncat\n"), lists.TypeDomain},
		{filepath.Join(dir, "missing_subnets.lst"), lists.TypeIP},
		{filepath.Join(dir, "zip_codes.lst"), lists.TypeDomain},
	}
	for _, c := range cases {
		if got := listFileType(c.path); got != c.want {
			t.Errorf("listFileType(%s) = %v, want %v", filepath.Base(c.path), got, c.want)
		}
	}
}
