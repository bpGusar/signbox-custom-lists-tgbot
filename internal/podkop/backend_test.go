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

// Shaped like "uci show forkop" of Forkop 2.0: links as anonymous
// connection_link children printed out of order, other children whose names
// a new link must not take, list files and URLs in both rule set options, and
// podkop's list options that forkop does not read at all.
const forkopShowFixture = `forkop.settings=settings
forkop.settings.config_version='2.0.0'
forkop.main=section
forkop.main.label='Main'
forkop.main.enabled='1'
forkop.main.action='connection'
forkop.main.rule_set='/etc/lst/domain_list.lst' 'https://example.com/list.txt'
forkop.main.rule_set_with_subnets='/etc/lst/ip_list.lst' '/etc/lst/mixed.lst'
forkop.@connection_link[0]=connection_link
forkop.@connection_link[0].section='main'
forkop.@connection_link[0].uri='vless://b@1.2.3.4:8443#two'
forkop.@connection_link[0].name='two'
forkop.@connection_link[0].order='1'
forkop.@connection_link[1]=connection_link
forkop.@connection_link[1].section='main'
forkop.@connection_link[1].uri='vless://a@1.2.3.4:8443#one'
forkop.@connection_link[1].name='one'
forkop.@connection_link[1].order='0'
forkop.ut_main=urltest
forkop.ut_main.section='main'
forkop.ut_main.name='Fastest'
forkop.@subscription_url[0]=subscription_url
forkop.@subscription_url[0].section='main'
forkop.empty=section
forkop.empty.action='connection'
forkop.@connection_link[2]=connection_link
forkop.@connection_link[2].section='empty'
forkop.@connection_link[2].uri='vless://old@9.9.9.9:443#old'
forkop.@connection_link[2].name='old'
forkop.@connection_link[3]=connection_link
forkop.@connection_link[3].section='empty'
forkop.@connection_link[3].uri='vless://older@9.9.9.9:443'
forkop.@connection_link[3].name='Node #1'
forkop.@connection_link[3].order='1'
forkop.blocked=section
forkop.blocked.action='block'
forkop.blocked.enabled='0'
forkop.blocked.rule_set='/etc/lst/block.lst'
forkop.blocked.local_subnet_lists='/etc/lst/ignored.lst'
forkop.resolver=section
forkop.resolver.action='dns'
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
			Label:           "Main",
			ConnectionType:  "connection",
			DomainLists:     []string{"/etc/lst/domain_list.lst", "/etc/lst/mixed.lst"},
			SubnetLists:     []string{"/etc/lst/ip_list.lst"},
			ProxyConfigType: ProxyTypeSelector,
			ProxyLinks:      []string{"vless://a@1.2.3.4:8443#one", "vless://b@1.2.3.4:8443#two"},
		},
		{
			Name:            "empty",
			ConnectionType:  "connection",
			ProxyConfigType: ProxyTypeSelector,
			ProxyLinks:      []string{"vless://old@9.9.9.9:443#old", "vless://older@9.9.9.9:443"},
		},
		{Name: "blocked", Disabled: true, ConnectionType: "block", DomainLists: []string{"/etc/lst/block.lst"}},
		{Name: "resolver", ConnectionType: "dns"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sections mismatch:\n got %+v\nwant %+v", got, want)
	}
	if !reflect.DeepEqual(f.calls, [][]string{{"show", "forkop"}}) {
		t.Fatalf("uci calls: %v", f.calls)
	}
	if !got[0].AcceptsProxyLinks() || got[2].AcceptsProxyLinks() {
		t.Fatalf("AcceptsProxyLinks mismatch: %+v", got)
	}
	if got[0].ConvertibleToURLTest() {
		t.Fatal("a forkop section is never switched to urltest")
	}
}

func TestForkopWrites(t *testing.T) {
	useBackend(t, Forkop)
	useListFileType(t)
	f := &fakeUCI{show: forkopShowFixture}
	f.install(t)
	ctx := context.Background()

	if err := Bind(ctx, "blocked", lists.TypeIP, "/etc/lst/block_ip.txt"); err != nil {
		t.Fatalf("Bind IP: %v", err)
	}
	if err := Bind(ctx, "blocked", lists.TypeDomain, "/etc/lst/block_ip.lst"); err != nil {
		t.Fatalf("Bind domain: %v", err)
	}
	if err := Bind(ctx, "blocked", lists.TypeIP, "/etc/lst/block.lst"); err == nil {
		t.Error("an IP list named without ip must be refused on forkop")
	}
	if err := Bind(ctx, "resolver", lists.TypeIP, "/etc/lst/dns_ip.lst"); err == nil {
		t.Error("an IP list must be refused for a DNS section")
	}
	if _, _, err := AddProxyLinks(ctx, "main", []string{
		"vless://a@1.2.3.4:8443#again",
		"vless://c@5.6.7.8:443#Fastest",
		"vless://d@5.6.7.8:443#a%2Fb",
	}); err != nil {
		t.Fatalf("AddProxyLinks: %v", err)
	}
	if err := ReplaceProxyLinks(ctx, "empty", []string{"vless://e@5.6.7.8:443#old", "vless://f@5.6.7.8:443"}); err != nil {
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
		"add_list forkop.blocked.rule_set_with_subnets=/etc/lst/block_ip.txt",
		"commit forkop",
		"add_list forkop.blocked.rule_set=/etc/lst/block_ip.lst",
		"commit forkop",
		"add forkop connection_link",
		"set forkop.cfg1.section=main",
		"set forkop.cfg1.uri=vless://c@5.6.7.8:443#Fastest",
		"set forkop.cfg1.name=Fastest #1",
		"set forkop.cfg1.order=2",
		"add forkop connection_link",
		"set forkop.cfg2.section=main",
		"set forkop.cfg2.uri=vless://d@5.6.7.8:443#a%2Fb",
		"set forkop.cfg2.name=a／b",
		"set forkop.cfg2.order=3",
		"commit forkop",
		"delete forkop.@connection_link[3]",
		"delete forkop.@connection_link[2]",
		"add forkop connection_link",
		"set forkop.cfg3.section=empty",
		"set forkop.cfg3.uri=vless://e@5.6.7.8:443#old",
		"set forkop.cfg3.name=old",
		"set forkop.cfg3.order=0",
		"add forkop connection_link",
		"set forkop.cfg4.section=empty",
		"set forkop.cfg4.uri=vless://f@5.6.7.8:443",
		"set forkop.cfg4.name=Node #1",
		"set forkop.cfg4.order=1",
		"commit forkop",
	}
	if !reflect.DeepEqual(writes, want) {
		t.Fatalf("uci writes mismatch:\n got %q\nwant %q", writes, want)
	}
}

func TestGroupLatencyForkop(t *testing.T) {
	useBackend(t, Forkop)
	useListFileType(t)
	f := &fakeUCI{show: forkopShowFixture}
	f.install(t)
	prev := cliRun
	t.Cleanup(func() { cliRun = prev })

	cliRun = func(_ context.Context, name string, args ...string) (string, error) {
		want := "clash_api GET /group/Main/delay?url=https%3A%2F%2Fwww.gstatic.com%2Fgenerate_204&timeout=5000"
		if name != "forkop" || strings.Join(args, " ") != want {
			t.Errorf("unexpected call: %s %v", name, args)
		}
		return `{"status":200,"body":"{\"Main/one\":120,\"Main/two\":0}"}`, nil
	}
	got, err := GroupLatency(context.Background(), "main")
	if err != nil {
		t.Fatalf("GroupLatency: %v", err)
	}
	if !reflect.DeepEqual(got, map[string]int{"one": 120}) {
		t.Fatalf("latency: %v", got)
	}

	cliRun = func(context.Context, string, ...string) (string, error) {
		return `{"status":404,"body":"{\"message\":\"Resource not found\"}"}`, nil
	}
	if _, err := GroupLatency(context.Background(), "main"); err == nil {
		t.Fatal("a 404 from the Clash API must be an error")
	}
}

func TestEmptyListFiles(t *testing.T) {
	useBackend(t, Forkop)
	useListFileType(t)
	files := map[string]string{
		"/etc/lst/full.lst":     "// #cat: x\nexample.com\n",
		"/etc/lst/comments.lst": "// #cat: x\n// example.com\n# note\n\n",
		"/etc/lst/off.lst":      "",
	}
	prev := readListFile
	t.Cleanup(func() { readListFile = prev })
	readListFile = func(path string) ([]byte, error) {
		body, ok := files[path]
		if !ok {
			return nil, os.ErrNotExist
		}
		return []byte(body), nil
	}
	full, comments, missing, off := "/etc/lst/full.lst", "/etc/lst/comments.lst", "/etc/lst/missing_ip.lst", "/etc/lst/off.lst"

	show := "forkop.a=section\n" +
		"forkop.a.action='connection'\n" +
		"forkop.a.rule_set='" + full + "' '" + comments + "' 'https://example.com/x.txt'\n" +
		"forkop.a.rule_set_with_subnets='" + missing + "'\n" +
		"forkop.b=section\n" +
		"forkop.b.enabled='0'\n" +
		"forkop.b.rule_set='" + off + "'\n"
	f := &fakeUCI{show: show}
	f.install(t)

	got, err := EmptyListFiles(context.Background())
	if err != nil {
		t.Fatalf("EmptyListFiles: %v", err)
	}
	if want := []string{comments, missing}; !reflect.DeepEqual(got, want) {
		t.Fatalf("EmptyListFiles = %v, want %v", got, want)
	}

	useBackend(t, Podkop)
	if got, err := EmptyListFiles(context.Background()); err != nil || got != nil {
		t.Fatalf("podkop: %v, %v", got, err)
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
