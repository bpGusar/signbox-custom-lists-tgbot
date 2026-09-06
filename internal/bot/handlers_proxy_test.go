package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	"lst-signbox-lists-tgbot/internal/podkop"
	"lst-signbox-lists-tgbot/internal/probe"
	"lst-signbox-lists-tgbot/internal/proxylink"
)

func testImport(t *testing.T, raw ...string) *ProxyImport {
	t.Helper()
	imp := &ProxyImport{
		ChatID:  1,
		MaxPing: 400 * time.Millisecond,
		Results: make(map[string]linkResult),
	}
	for _, r := range raw {
		l, err := proxylink.Parse(r)
		if err != nil {
			t.Fatalf("Parse(%q): %v", r, err)
		}
		imp.Links = append(imp.Links, l)
	}
	return imp
}

func TestPassedIsSortedByLatency(t *testing.T) {
	imp := testImport(t,
		"vless://a@1.1.1.1:443#⚡ медленный",
		"vless://b@2.2.2.2:443#⚡ быстрый",
		"vless://c@3.3.3.3:443#⚡ средний",
		"vless://d@4.4.4.4:443#⚡ мимо порога",
		"vless://e@5.5.5.5:443#⚡ молчит",
	)
	imp.Results[imp.Links[0].DedupKey()] = linkResult{Latency: 300 * time.Millisecond, OK: true}
	imp.Results[imp.Links[1].DedupKey()] = linkResult{Latency: 40 * time.Millisecond, OK: true}
	imp.Results[imp.Links[2].DedupKey()] = linkResult{Latency: 150 * time.Millisecond, OK: true}
	imp.Results[imp.Links[3].DedupKey()] = linkResult{Latency: 900 * time.Millisecond, OK: true}
	imp.Results[imp.Links[4].DedupKey()] = linkResult{Reason: "сервер не ответил"}

	passed := imp.Passed()
	var labels []string
	for _, l := range passed {
		labels = append(labels, l.Label)
	}
	want := []string{"⚡ быстрый", "⚡ средний", "⚡ медленный"}
	if strings.Join(labels, ",") != strings.Join(want, ",") {
		t.Fatalf("passed = %v, want %v", labels, want)
	}

	failed := imp.Failed()
	if len(failed) != 2 {
		t.Fatalf("failed = %d, want 2", len(failed))
	}
}

// A link nothing was measured for — the run was stopped before it — counts as
// failed, never as a silent pass.
func TestFailedIncludesUnmeasured(t *testing.T) {
	imp := testImport(t, "vless://a@1.1.1.1:443#⚡ один")
	if len(imp.Failed()) != 1 || len(imp.Passed()) != 0 {
		t.Fatalf("an unmeasured link must not pass: passed=%d failed=%d", len(imp.Passed()), len(imp.Failed()))
	}
}

func TestReportMasksCredentials(t *testing.T) {
	imp := testImport(t, "vless://11111111-2222-3333-4444-555555555555@1.1.1.1:443#⚡ узел")
	imp.Results[imp.Links[0].DedupKey()] = linkResult{Latency: 120 * time.Millisecond, OK: true}

	body := reportBody(imp, imp.Passed(), imp.Failed())
	if strings.Contains(body, "11111111") {
		t.Fatalf("the report leaked the credentials:\n%s", body)
	}
	if !strings.Contains(body, "vless://***@1.1.1.1:443") {
		t.Fatalf("the report should name the endpoint:\n%s", body)
	}
	if !strings.Contains(body, "120 мс") {
		t.Fatalf("the report should show the latency:\n%s", body)
	}
}

func TestMetricLabel(t *testing.T) {
	cases := []struct {
		imp  ProxyImport
		want string
	}{
		{ProxyImport{Tunnel: true, Method: probe.MethodTCP}, "через туннель (sing-box)"},
		{ProxyImport{Method: probe.MethodTCP}, "TCP-хендшейк до сервера"},
		{ProxyImport{Method: probe.MethodICMP}, "TCP-хендшейк до сервера, ICMP ping для hysteria2"},
	}
	for _, c := range cases {
		imp := c.imp
		if got := metricLabel(&imp); got != c.want {
			t.Errorf("metricLabel(%+v) = %q, want %q", c.imp, got, c.want)
		}
	}
}

// Only sections that can hold a list are offered; a single-link one is offered
// as a switch, and the rest are not shown at all.
func TestProxySectionRows(t *testing.T) {
	a := testApp([]podkop.Section{
		{Name: "main", ConnectionType: "proxy", ProxyConfigType: "urltest", ProxyLinks: []string{"vless://a@1.1.1.1:443"}},
		{Name: "picker", ConnectionType: "proxy", ProxyConfigType: "selector"},
		{Name: "single", ConnectionType: "proxy", ProxyConfigType: "url", ProxyString: "vless://b@2.2.2.2:443"},
		{Name: "raw", ConnectionType: "proxy", ProxyConfigType: "outbound"},
		{Name: "Exclude", ConnectionType: "exclusion"},
	}, nil)

	rows := a.proxySectionRows(context.Background(), "imp1")
	var labels []string
	for _, row := range rows {
		labels = append(labels, row[0].Text)
	}
	if len(labels) != 3 {
		t.Fatalf("rows = %v, want three of them", labels)
	}
	if !strings.HasPrefix(labels[0], "🗂 main · urltest · 1") {
		t.Errorf("urltest row: %q", labels[0])
	}
	if !strings.HasPrefix(labels[1], "🗂 picker · selector · 0") {
		t.Errorf("selector row: %q", labels[1])
	}
	if !strings.HasPrefix(labels[2], "🔁 single") {
		t.Errorf("convertible row: %q", labels[2])
	}
	for _, row := range rows {
		if !strings.HasPrefix(row[0].CallbackData, proxyCbPrefix+"imp1:") {
			t.Errorf("callback outside the import namespace: %q", row[0].CallbackData)
		}
	}
}

func TestForUCIIsSafeForPodkop(t *testing.T) {
	imp := testImport(t, "vless://a@1.1.1.1:443#⚡️ Нидерланды | Amsterdam")
	imp.Results[imp.Links[0].DedupKey()] = linkResult{Latency: 10 * time.Millisecond, OK: true}

	for _, raw := range forUCI(imp.Passed()) {
		if err := podkop.ValidateProxyLink(raw); err != nil {
			t.Fatalf("ValidateProxyLink(%q) = %v", raw, err)
		}
	}
}

func TestProxyLinkKey(t *testing.T) {
	if proxyLinkKey("vless://a@1.1.1.1:443#label") != "vless://a@1.1.1.1:443" {
		t.Fatal("the label must not be part of the key")
	}
	if proxyLinkKey("vless://a@1.1.1.1:443") != "vless://a@1.1.1.1:443" {
		t.Fatal("a link without a label is its own key")
	}
}

func TestImportStatsText(t *testing.T) {
	text := importStatsText("sub.txt", proxylink.Stats{
		Lines: 278, Parsed: 278, Skipped: 0, Bolt: 83, LTE: 64, Collapsed: 5, Kept: 30, Targets: 25,
	})
	for _, want := range []string{"sub.txt", "278", "83", "64", "30", "25"} {
		if !strings.Contains(text, want) {
			t.Errorf("stats text is missing %q:\n%s", want, text)
		}
	}
}

func TestNoLinksHint(t *testing.T) {
	cases := []struct {
		st   proxylink.Stats
		want string
	}{
		{proxylink.Stats{Lines: 5}, "поддерживаемых схем"},
		{proxylink.Stats{Lines: 5, Parsed: 5}, "не помечена ⚡"},
		{proxylink.Stats{Lines: 5, Parsed: 5, Bolt: 3, LTE: 3}, "LTE и дублям"},
	}
	for _, c := range cases {
		if got := noLinksHint(c.st); !strings.Contains(got, c.want) {
			t.Errorf("noLinksHint(%+v) = %q, want it to mention %q", c.st, got, c.want)
		}
	}
}

func TestPluralLinks(t *testing.T) {
	cases := map[int]string{1: "1 ссылка", 2: "2 ссылки", 5: "5 ссылок", 11: "11 ссылок", 21: "21 ссылка", 104: "104 ссылки"}
	for n, want := range cases {
		if got := pluralLinks(n); got != want {
			t.Errorf("pluralLinks(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestProxyUploadHintText(t *testing.T) {
	text := proxyUploadHintText()
	for _, want := range []string{"vless", "ss", "trojan", "hysteria2", "⚡", "LTE", "1024 КБ", "5000", "300"} {
		if !strings.Contains(text, want) {
			t.Errorf("upload hint is missing %q:\n%s", want, text)
		}
	}
}

// A document is taken only after the upload screen has been shown, and one
// screen arms exactly one import.
func TestProxyFileArming(t *testing.T) {
	s := newTestStore(time.Minute)

	if s.ProxyFileArmed(1) {
		t.Fatal("a chat that has seen nothing must not accept a file")
	}
	s.ArmProxyFile(1)
	if !s.ProxyFileArmed(1) {
		t.Fatal("the upload screen must arm the chat")
	}
	if s.ProxyFileArmed(2) {
		t.Fatal("arming is per chat")
	}

	// Typing something is a different channel and must not disarm the upload.
	s.Await(1, awaitNewCategory, "op1")
	s.TakeAwait(1)
	if !s.ProxyFileArmed(1) {
		t.Fatal("a text message must not disarm the file upload")
	}

	s.DisarmProxyFile(1)
	if s.ProxyFileArmed(1) {
		t.Fatal("leaving the screen must disarm the chat")
	}
}

func newTestStore(ttl time.Duration) *SessionStore {
	return &SessionStore{
		ops:     map[string]*PendingOp{},
		imports: map[string]*ProxyImport{},
		chats:   map[int64]*chatState{},
		ttl:     ttl,
	}
}

func TestImportStoreLifecycle(t *testing.T) {
	s := newTestStore(time.Minute)

	imp := s.CreateImport(ProxyImport{ChatID: 7})
	if _, ok := s.GetImport(imp.ID); !ok {
		t.Fatal("a fresh import must be readable")
	}

	s.UpdateImport(imp.ID, func(p *ProxyImport) { p.Section = "main" })
	got, _ := s.GetImport(imp.ID)
	if got.Section != "main" {
		t.Fatalf("UpdateImport did not apply: %+v", got)
	}

	cancelled := false
	s.UpdateImport(imp.ID, func(p *ProxyImport) { p.cancel = func() { cancelled = true } })
	s.DeleteImport(imp.ID)
	if !cancelled {
		t.Fatal("deleting a running import must cancel it")
	}
	if _, ok := s.GetImport(imp.ID); ok {
		t.Fatal("a deleted import must be gone")
	}
}

func TestImportStoreExpires(t *testing.T) {
	s := newTestStore(time.Millisecond)
	imp := s.CreateImport(ProxyImport{ChatID: 7})
	time.Sleep(5 * time.Millisecond)
	if _, ok := s.GetImport(imp.ID); ok {
		t.Fatal("a stale import must not be readable")
	}
}
