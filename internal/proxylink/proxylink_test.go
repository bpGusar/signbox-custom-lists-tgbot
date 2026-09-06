package proxylink

import (
	"os"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	l, err := Parse("vless://uuid@1.2.3.4:8443?type=tcp#⚡️%20Нидерланды%20|%20Amsterdam")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if l.Scheme != "vless" || l.Host != "1.2.3.4" || l.Port != 8443 {
		t.Fatalf("unexpected link: %+v", l)
	}
	if l.Label != "⚡️ Нидерланды | Amsterdam" {
		t.Fatalf("label not percent-decoded: %q", l.Label)
	}
	if l.UDP {
		t.Fatal("vless is not a UDP-only protocol")
	}
	if l.Endpoint() != "1.2.3.4:8443" {
		t.Fatalf("endpoint: %q", l.Endpoint())
	}
}

func TestParseHysteria2IsUDP(t *testing.T) {
	l, err := Parse("hysteria2://pass@5.6.7.8:30443?alpn=h3#⚡ hy2")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !l.UDP {
		t.Fatal("hysteria2 must be marked UDP")
	}
}

func TestParseRejects(t *testing.T) {
	bad := []string{
		"",
		"просто текст",
		"vmess://eyJhZGQiOiIxIn0=#⚡",
		"vless://uuid@host-without-port#⚡",
		"vless://uuid@:8443#⚡",
		"vless://uuid@1.2.3.4:70000#⚡",
		"vless://uuid@1.2.3.4:8443\n#⚡",
	}
	for _, s := range bad {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) = nil error, want one", s)
		}
	}
}

// A "%" that is not an escape must not fail the line: subscription labels are
// free text written by humans.
func TestParseKeepsStrayPercent(t *testing.T) {
	l, err := Parse("vless://uuid@1.2.3.4:443#⚡ скидка 50% сегодня")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if l.Label != "⚡ скидка 50% сегодня" {
		t.Fatalf("label: %q", l.Label)
	}
}

func TestHasBolt(t *testing.T) {
	if !HasBolt("⚡️ Нидерланды") {
		t.Error("bolt with the variation selector must be found")
	}
	if !HasBolt("⚡ Германия") {
		t.Error("bare bolt must be found")
	}
	if HasBolt("Нидерланды") {
		t.Error("no bolt, no match")
	}
}

func TestIsLTE(t *testing.T) {
	yes := []string{"⚡ Москва LTE билайн", "lte-питер", "LTE", "нода (lte)"}
	no := []string{"Нидерланды", "filter", "altered", "lteX", "Xlte"}
	for _, s := range yes {
		if !IsLTE(s) {
			t.Errorf("IsLTE(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if IsLTE(s) {
			t.Errorf("IsLTE(%q) = true, want false", s)
		}
	}
}

// The cost of getting this wrong is a podkop that fails to generate its config
// after the next restart: podkop expands the list unquoted.
func TestForUCIHasNoWhitespace(t *testing.T) {
	l, err := Parse("vless://uuid@1.2.3.4:8443?type=tcp#⚡️ Нидерланды | Amsterdam 2")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := l.ForUCI()
	if strings.ContainsAny(got, " \t\n\r") {
		t.Fatalf("ForUCI kept whitespace: %q", got)
	}
	if !strings.Contains(got, "%20") {
		t.Fatalf("spaces must be percent-encoded: %q", got)
	}
	if !strings.Contains(got, "⚡") {
		t.Fatalf("the bolt must survive: %q", got)
	}
	if err := ValidateRaw(got); err != nil {
		t.Fatalf("ValidateRaw(%q) = %v", got, err)
	}
}

func TestForUCIEscapesShellSpecials(t *testing.T) {
	l, err := Parse("vless://uuid@1.2.3.4:8443#it's\\a `$test` 100%")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := l.ForUCI()
	frag := got[strings.Index(got, "#")+1:]
	for _, bad := range []string{"'", "\\", "`", "$", " "} {
		if strings.Contains(frag, bad) {
			t.Fatalf("%q survived into the fragment: %q", bad, got)
		}
	}
	if err := ValidateRaw(got); err != nil {
		t.Fatalf("ValidateRaw(%q) = %v", got, err)
	}
}

func TestForUCITrimsLongLabel(t *testing.T) {
	l, err := Parse("vless://uuid@1.2.3.4:8443#" + strings.Repeat("я", MaxLabelLen*2))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := l.ForUCI()
	frag := got[strings.Index(got, "#")+1:]
	// Every "я" costs two bytes, each escaped or not.
	if len([]rune(strings.ReplaceAll(frag, "я", ""))) != 0 {
		t.Fatalf("unexpected fragment content: %q", frag)
	}
	if n := len([]rune(frag)); n > MaxLabelLen {
		t.Fatalf("fragment kept %d runes, want at most %d", n, MaxLabelLen)
	}
}

func TestDedupKeyIgnoresLabelOnly(t *testing.T) {
	a, _ := Parse("vless://uuid@1.2.3.4:8443?type=tcp#первая подпись")
	b, _ := Parse("vless://uuid@1.2.3.4:8443?type=tcp#вторая подпись")
	c, _ := Parse("vless://other@1.2.3.4:8443?type=tcp#вторая подпись")
	if a.DedupKey() != b.DedupKey() {
		t.Fatal("links differing only by label must collapse")
	}
	if a.DedupKey() == c.DedupKey() {
		t.Fatal("a different uuid on the same host is a different account")
	}
}

func TestValidateRaw(t *testing.T) {
	if err := ValidateRaw("vless://uuid@1.2.3.4:8443#ok"); err != nil {
		t.Fatalf("ValidateRaw: %v", err)
	}
	bad := []string{
		"",
		"vmess://uuid@1.2.3.4:8443",
		"vless://uuid@1.2.3.4:8443#с пробелом",
		"vless://uuid@1.2.3.4:8443#c'кавычкой",
		"vless://uuid@1.2.3.4:8443\n",
		"vless://uuid@1.2.3.4:8443#" + strings.Repeat("a", MaxLinkLen),
	}
	for _, s := range bad {
		if err := ValidateRaw(s); err == nil {
			t.Errorf("ValidateRaw(%q) = nil, want an error", s)
		}
	}
}

func TestMasked(t *testing.T) {
	l, _ := Parse("vless://11111111-1111-1111-1111-111111111111@1.2.3.4:8443#⚡")
	got := l.Masked()
	if strings.Contains(got, "1111") {
		t.Fatalf("credentials leaked: %q", got)
	}
	if got != "vless://***@1.2.3.4:8443" {
		t.Fatalf("Masked: %q", got)
	}
}

func TestParseAllFixture(t *testing.T) {
	f, err := os.Open("testdata/proxy_links.txt")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = f.Close() }()

	links, st, err := ParseAll(f, DefaultLimits())
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}

	want := Stats{
		Lines:     15,
		Parsed:    12,
		Skipped:   3,
		Bolt:      10,
		LTE:       2,
		Collapsed: 1,
		Kept:      7,
		Targets:   6,
	}
	if st != want {
		t.Fatalf("stats mismatch:\n got %+v\nwant %+v", st, want)
	}
	if len(links) != want.Kept {
		t.Fatalf("got %d links, want %d", len(links), want.Kept)
	}
	for _, l := range links {
		if !HasBolt(l.Label) {
			t.Errorf("kept a link without the bolt: %q", l.Label)
		}
		if IsLTE(l.Label) {
			t.Errorf("kept an LTE link: %q", l.Label)
		}
		if err := l.Validate(); err != nil {
			t.Errorf("kept link fails validation: %v (%q)", err, l.ForUCI())
		}
	}
	if got := len(Endpoints(links)); got != want.Targets {
		t.Fatalf("Endpoints gave %d targets, want %d", got, want.Targets)
	}
}

func TestParseAllLimits(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 50; i++ {
		sb.WriteString("vless://uuid")
		sb.WriteString(string(rune('a' + i%26)))
		sb.WriteString("@1.2.3.4:8443#⚡\n")
	}
	links, st, err := ParseAll(strings.NewReader(sb.String()), Limits{MaxLinks: 5})
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	if len(links) != 5 || !st.Truncated {
		t.Fatalf("limit not applied: %d links, truncated=%t", len(links), st.Truncated)
	}
}
