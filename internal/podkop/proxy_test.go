package podkop

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestAcceptsProxyLinks(t *testing.T) {
	cases := []struct {
		s    Section
		want bool
	}{
		{Section{ConnectionType: "proxy", ProxyConfigType: "urltest"}, true},
		{Section{ConnectionType: "proxy", ProxyConfigType: "selector"}, true},
		{Section{ConnectionType: "proxy", ProxyConfigType: "url"}, false},
		{Section{ConnectionType: "proxy", ProxyConfigType: "outbound"}, false},
		{Section{ConnectionType: "exclusion", ProxyConfigType: "urltest"}, false},
		{Section{ConnectionType: "proxy"}, false},
	}
	for _, c := range cases {
		if got := c.s.AcceptsProxyLinks(); got != c.want {
			t.Errorf("AcceptsProxyLinks(%+v) = %t, want %t", c.s, got, c.want)
		}
	}
}

func TestProxyLinksOption(t *testing.T) {
	if got := ProxyLinksOption(ProxyTypeURLTest); got != "urltest_proxy_links" {
		t.Errorf("urltest option: %q", got)
	}
	if got := ProxyLinksOption(ProxyTypeSelector); got != "selector_proxy_links" {
		t.Errorf("selector option: %q", got)
	}
	for _, t2 := range []string{ProxyTypeURL, ProxyTypeOutbound, ""} {
		if got := ProxyLinksOption(t2); got != "" {
			t.Errorf("ProxyLinksOption(%q) = %q, want empty", t2, got)
		}
	}
}

func TestAddProxyLinksSkipsExisting(t *testing.T) {
	f := &fakeUCI{show: showFixture}
	f.install(t)

	added, skipped, err := AddProxyLinks(context.Background(), "main", []string{
		// Already there, only the label differs.
		"vless://a@1.2.3.4:8443#%E2%9A%A1%20other%20label",
		"vless://new@5.6.7.8:443#%E2%9A%A1%20new",
	})
	if err != nil {
		t.Fatalf("AddProxyLinks: %v", err)
	}
	if added != 1 || skipped != 1 {
		t.Fatalf("added=%d skipped=%d, want 1 and 1", added, skipped)
	}

	want := [][]string{
		{"show", "podkop"},
		{"add_list", "podkop.main.urltest_proxy_links=vless://new@5.6.7.8:443#%E2%9A%A1%20new"},
		{"commit", "podkop"},
	}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("uci calls mismatch:\n got %v\nwant %v", f.calls, want)
	}
}

// Nothing is committed when there is nothing new: a stale banner for a no-op
// would send the user off to restart podkop for no reason.
func TestAddProxyLinksNoop(t *testing.T) {
	f := &fakeUCI{show: showFixture}
	f.install(t)

	added, skipped, err := AddProxyLinks(context.Background(), "main",
		[]string{"vless://a@1.2.3.4:8443#%E2%9A%A1%20one"})
	if err != nil {
		t.Fatalf("AddProxyLinks: %v", err)
	}
	if added != 0 || skipped != 1 {
		t.Fatalf("added=%d skipped=%d", added, skipped)
	}
	if len(f.calls) != 1 {
		t.Fatalf("nothing should have been written, got %v", f.calls)
	}
}

func TestAddProxyLinksRejectsBadInput(t *testing.T) {
	f := &fakeUCI{show: showFixture}
	f.install(t)

	// A space inside a link breaks podkop's own config generation, which
	// expands the list unquoted.
	if _, _, err := AddProxyLinks(context.Background(), "main",
		[]string{"vless://x@1.2.3.4:8443#с пробелом"}); err == nil {
		t.Fatal("expected a link with whitespace to be rejected")
	}
	if _, _, err := AddProxyLinks(context.Background(), "main; reboot", nil); err == nil {
		t.Fatal("expected a bad section name to be rejected")
	}
	// A section that keeps a single link has no list to append to.
	if _, _, err := AddProxyLinks(context.Background(), "youtube",
		[]string{"vless://x@1.2.3.4:8443#ok"}); err == nil {
		t.Fatal("expected a url-type section to be refused")
	}
	for _, c := range f.calls {
		if c[0] == "add_list" || c[0] == "commit" {
			t.Fatalf("nothing should have been written, got %v", f.calls)
		}
	}
}

func TestReplaceProxyLinks(t *testing.T) {
	f := &fakeUCI{show: showFixture}
	f.install(t)

	err := ReplaceProxyLinks(context.Background(), "main", []string{
		"vless://x@5.6.7.8:443#%E2%9A%A1%20x",
		"vless://y@5.6.7.9:443#%E2%9A%A1%20y",
	})
	if err != nil {
		t.Fatalf("ReplaceProxyLinks: %v", err)
	}

	want := [][]string{
		{"show", "podkop"},
		{"delete", "podkop.main.urltest_proxy_links"},
		{"add_list", "podkop.main.urltest_proxy_links=vless://x@5.6.7.8:443#%E2%9A%A1%20x"},
		{"add_list", "podkop.main.urltest_proxy_links=vless://y@5.6.7.9:443#%E2%9A%A1%20y"},
		{"commit", "podkop"},
	}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("uci calls mismatch:\n got %v\nwant %v", f.calls, want)
	}
}

// A section that never had the option is the normal case for a replace, and
// uci reports the missing option as an error.
func TestReplaceProxyLinksSurvivesMissingOption(t *testing.T) {
	f := &fakeUCI{show: showFixture, failOn: "delete podkop.main.urltest_proxy_links"}
	f.install(t)

	if err := ReplaceProxyLinks(context.Background(), "main", []string{"vless://x@5.6.7.8:443#x"}); err != nil {
		t.Fatalf("ReplaceProxyLinks: %v", err)
	}
}

func TestReplaceProxyLinksValidatesBeforeDeleting(t *testing.T) {
	f := &fakeUCI{show: showFixture}
	f.install(t)

	err := ReplaceProxyLinks(context.Background(), "main", []string{
		"vless://ok@5.6.7.8:443#ok",
		"vless://bad@5.6.7.9:443#с пробелом",
	})
	if err == nil {
		t.Fatal("expected the bad link to be caught")
	}
	for _, c := range f.calls {
		if c[0] == "delete" {
			t.Fatalf("the list was dropped before the input was checked: %v", f.calls)
		}
	}
}

func TestConvertToURLTest(t *testing.T) {
	f := &fakeUCI{show: showFixture}
	f.install(t)

	if err := ConvertToURLTest(context.Background(), "youtube"); err != nil {
		t.Fatalf("ConvertToURLTest: %v", err)
	}
	want := [][]string{
		{"show", "podkop"},
		{"set", "podkop.youtube.proxy_config_type=urltest"},
		{"add_list", "podkop.youtube.urltest_proxy_links=vless://single@7.7.7.7:443#%E2%9A%A1%20single"},
		{"commit", "podkop"},
	}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("uci calls mismatch:\n got %v\nwant %v", f.calls, want)
	}
}

func TestConvertToURLTestRefusesWrongType(t *testing.T) {
	f := &fakeUCI{show: showFixture}
	f.install(t)

	if err := ConvertToURLTest(context.Background(), "main"); err == nil {
		t.Fatal("a section that is already a list must not be converted")
	}
}

func TestValidateProxyLink(t *testing.T) {
	if err := ValidateProxyLink("vless://uuid@1.2.3.4:8443#%E2%9A%A1"); err != nil {
		t.Fatalf("ValidateProxyLink: %v", err)
	}
	bad := []string{"", "http://1.2.3.4", "vless://uuid@1.2.3.4:8443#a b", "vless://uuid@1.2.3.4:8443\n"}
	for _, s := range bad {
		if err := ValidateProxyLink(s); err == nil {
			t.Errorf("ValidateProxyLink(%q) = nil, want an error", s)
		}
	}
}

func TestGroupLatency(t *testing.T) {
	prev := podkopRun
	t.Cleanup(func() { podkopRun = prev })

	podkopRun = func(_ context.Context, args ...string) (string, error) {
		if strings.Join(args, " ") != "clash_api get_group_latency main" {
			t.Errorf("unexpected podkop args: %v", args)
		}
		return `{"main-1-out": 143, "main-2-out": 388, "main-3-out": 0}`, nil
	}
	got, err := GroupLatency(context.Background(), "main")
	if err != nil {
		t.Fatalf("GroupLatency: %v", err)
	}
	want := map[string]int{"main-1-out": 143, "main-2-out": 388}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("latency mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestGroupLatencyPlainOutput(t *testing.T) {
	prev := podkopRun
	t.Cleanup(func() { podkopRun = prev })

	podkopRun = func(context.Context, ...string) (string, error) {
		return "main-1-out: 143ms\nmain-2-out = 388 ms\nвсё\n", nil
	}
	got, err := GroupLatency(context.Background(), "main")
	if err != nil {
		t.Fatalf("GroupLatency: %v", err)
	}
	want := map[string]int{"main-1-out": 143, "main-2-out": 388}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("latency mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestGroupLatencyErrors(t *testing.T) {
	prev := podkopRun
	t.Cleanup(func() { podkopRun = prev })

	podkopRun = func(context.Context, ...string) (string, error) { return "", errors.New("no such command") }
	if _, err := GroupLatency(context.Background(), "main"); err == nil {
		t.Fatal("expected an error when podkop fails")
	}

	podkopRun = func(context.Context, ...string) (string, error) { return "ничего полезного", nil }
	if _, err := GroupLatency(context.Background(), "main"); err == nil {
		t.Fatal("expected an error when nothing could be parsed")
	}

	podkopRun = func(context.Context, ...string) (string, error) { return "{}", nil }
	if _, err := GroupLatency(context.Background(), "main; reboot"); err == nil {
		t.Fatal("expected a bad section name to be rejected")
	}
}
