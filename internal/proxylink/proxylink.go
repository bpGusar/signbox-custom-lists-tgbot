// Package proxylink parses a subscription file of proxy links and picks the
// ones worth measuring: marked with ⚡, not LTE, deduplicated.
package proxylink

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Link is one proxy link from the subscription file.
type Link struct {
	// Raw is the source line, unchanged.
	Raw string
	// Scheme is the part before "://", lowercased.
	Scheme string
	Host   string
	Port   int
	// Label is the fragment after "#", percent-decoded.
	Label string
	// UDP marks the protocols that carry no TCP handshake to time, so a
	// TCP probe would measure nothing.
	UDP bool
}

// Stats is what the file turned out to hold, for the screen that reports it
// back before anything is measured.
type Stats struct {
	// Lines is the non-empty lines the parser looked at.
	Lines int
	// Parsed is the lines that were proxy links of a supported scheme.
	Parsed int
	// Skipped is the lines that were not: another scheme, or not a link.
	Skipped int
	// Bolt is the parsed links marked with ⚡.
	Bolt int
	// LTE is the ⚡ links dropped for being LTE.
	LTE int
	// Collapsed is the duplicates dropped, links equal but for the label.
	Collapsed int
	// Kept is what survived all of the above — the length of the returned
	// slice.
	Kept int
	// Targets is the unique host:port among the kept links.
	Targets int
	// Truncated says a limit cut the file short.
	Truncated bool
}

// Limits guard against a file that is not a subscription at all.
type Limits struct {
	MaxBytes int64
	MaxLines int
	MaxLinks int
}

// DefaultLimits are the ones the bot uses.
func DefaultLimits() Limits {
	return Limits{MaxBytes: 1 << 20, MaxLines: 5000, MaxLinks: 300}
}

const (
	// MaxLinkLen is the longest link that may reach podkop's config.
	MaxLinkLen = 2048
	// MaxLabelLen caps the fragment, in runes: it is a human label, and it
	// travels through a shell loop in podkop.
	MaxLabelLen = 96
)

// schemes is podkop's own whitelist (validateProxyUrl). udp marks the ones a
// TCP probe cannot time.
var schemes = map[string]bool{
	"vless":     false,
	"ss":        false,
	"trojan":    false,
	"socks4":    false,
	"socks4a":   false,
	"socks5":    false,
	"hysteria2": true,
	"hy2":       true,
}

// SupportedScheme reports whether podkop would accept a link of this scheme.
func SupportedScheme(scheme string) bool {
	_, ok := schemes[strings.ToLower(scheme)]
	return ok
}

// boltRune is U+26A1. The file mixes it with and without the U+FE0F variation
// selector, which a rune search ignores on its own.
const boltRune = '⚡'

var lteRe = regexp.MustCompile(`(?i)(^|[^a-z0-9])lte([^a-z0-9]|$)`)

// HasBolt reports whether the label carries the ⚡ mark, with or without the
// variation selector the file mixes in.
func HasBolt(label string) bool {
	return strings.ContainsRune(label, boltRune)
}

// IsLTE reports whether the label calls the node an LTE one.
func IsLTE(label string) bool {
	return lteRe.MatchString(label)
}

// Parse reads one line as a proxy link. The fragment is split off by hand:
// url.Parse rejects a stray "%" in it, and subscription labels are free text.
func Parse(line string) (Link, error) {
	raw := strings.TrimSpace(line)
	if raw == "" {
		return Link{}, fmt.Errorf("пустая строка")
	}
	if strings.ContainsAny(raw, "\n\r") {
		return Link{}, fmt.Errorf("перенос строки внутри ссылки")
	}
	if len(raw) > MaxLinkLen {
		return Link{}, fmt.Errorf("ссылка длиннее %d символов", MaxLinkLen)
	}

	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return Link{}, fmt.Errorf("не похоже на ссылку")
	}
	scheme = strings.ToLower(scheme)
	udp, known := schemes[scheme]
	if !known {
		return Link{}, fmt.Errorf("схема %q не поддерживается podkop", scheme)
	}

	base, frag, hasFrag := strings.Cut(rest, "#")
	u, err := url.Parse(scheme + "://" + base)
	if err != nil {
		return Link{}, fmt.Errorf("неразбираемая ссылка: %w", err)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return Link{}, fmt.Errorf("не указан хост")
	}
	portStr := u.Port()
	if portStr == "" {
		return Link{}, fmt.Errorf("не указан порт")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return Link{}, fmt.Errorf("некорректный порт %q", portStr)
	}

	l := Link{Raw: raw, Scheme: scheme, Host: host, Port: port, UDP: udp}
	if hasFrag {
		l.Label = decodePercent(frag)
	}
	return l, nil
}

// ParseAll runs the whole selection over a subscription file: parse, keep the
// ⚡ ones, drop LTE, collapse links that differ only by label.
func ParseAll(r io.Reader, lim Limits) ([]Link, Stats, error) {
	if lim.MaxBytes > 0 {
		r = io.LimitReader(r, lim.MaxBytes+1)
	}

	var (
		st    Stats
		kept  []Link
		seen  = make(map[string]struct{})
		hosts = make(map[string]struct{})
		read  int64
	)

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 256*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		read += int64(len(sc.Bytes())) + 1
		if lim.MaxBytes > 0 && read > lim.MaxBytes {
			st.Truncated = true
			break
		}
		if line == "" {
			continue
		}
		st.Lines++
		if lim.MaxLines > 0 && st.Lines > lim.MaxLines {
			st.Lines--
			st.Truncated = true
			break
		}

		l, err := Parse(line)
		if err != nil {
			st.Skipped++
			continue
		}
		st.Parsed++

		if !HasBolt(l.Label) {
			continue
		}
		st.Bolt++
		if IsLTE(l.Label) {
			st.LTE++
			continue
		}
		key := l.DedupKey()
		if _, dup := seen[key]; dup {
			st.Collapsed++
			continue
		}
		seen[key] = struct{}{}
		kept = append(kept, l)
		hosts[l.Endpoint()] = struct{}{}

		if lim.MaxLinks > 0 && len(kept) >= lim.MaxLinks {
			st.Truncated = true
			break
		}
	}
	if err := sc.Err(); err != nil {
		return nil, st, fmt.Errorf("чтение файла: %w", err)
	}

	st.Kept = len(kept)
	st.Targets = len(hosts)
	return kept, st, nil
}

// Endpoint is the host:port a probe connects to — several links can share one.
func (l Link) Endpoint() string {
	return l.Host + ":" + strconv.Itoa(l.Port)
}

// DedupKey is the link without its label: two lines that differ only by the
// caption are the same node, but the same host:port with a different uuid is
// a different account and must survive.
func (l Link) DedupKey() string {
	if i := strings.IndexByte(l.Raw, '#'); i >= 0 {
		return l.Raw[:i]
	}
	return l.Raw
}

// ForUCI is the link as it may be written into podkop's config. podkop expands
// the list unquoted ("for link in $urltest_proxy_links"), so a space anywhere
// in the value would split one link into several and break config generation
// on the next restart.
func (l Link) ForUCI() string {
	base := l.DedupKey()
	label := trimRunes(l.Label, MaxLabelLen)
	if label == "" {
		return base
	}
	return base + "#" + encodeFragment(label)
}

// Validate rejects a link that must never reach podkop's config.
func (l Link) Validate() error {
	return ValidateRaw(l.ForUCI())
}

// ValidateRaw is Validate for a value read back from the config, where there is
// no parsed link to ask.
func ValidateRaw(raw string) error {
	switch {
	case raw == "":
		return fmt.Errorf("пустая ссылка")
	case len(raw) > MaxLinkLen:
		return fmt.Errorf("ссылка длиннее %d символов", MaxLinkLen)
	case strings.ContainsAny(raw, "\n\r"):
		return fmt.Errorf("перенос строки внутри ссылки")
	}
	scheme, _, ok := strings.Cut(raw, "://")
	if !ok || !SupportedScheme(scheme) {
		return fmt.Errorf("схема не поддерживается podkop")
	}
	for _, r := range raw {
		if unicode.IsSpace(r) || r == '\'' || r == '\\' || r < 0x20 || r == 0x7f {
			return fmt.Errorf("в ссылке есть пробел или служебный символ — podkop разберёт её неверно")
		}
	}
	return nil
}

// Masked hides the credentials, so a report can name a link without leaking
// the key it carries.
func (l Link) Masked() string {
	return l.Scheme + "://***@" + l.Endpoint()
}

// Title is how a link is named in a report: its label, or its endpoint when
// the label is empty.
func (l Link) Title() string {
	label := strings.TrimSpace(l.Label)
	if label == "" {
		return l.Endpoint()
	}
	return label
}

// Endpoints is the deduplicated host:port set of a batch, in first-seen order.
func Endpoints(links []Link) []Link {
	seen := make(map[string]struct{}, len(links))
	out := make([]Link, 0, len(links))
	for _, l := range links {
		if _, ok := seen[l.Endpoint()]; ok {
			continue
		}
		seen[l.Endpoint()] = struct{}{}
		out = append(out, l)
	}
	return out
}

// decodePercent decodes %XX the way a subscription label expects, leaving a
// stray "%" alone instead of failing the whole line over it.
func decodePercent(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == '%' && i+2 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
				sb.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		sb.WriteByte(s[i])
		i++
	}
	return sb.String()
}

// encodeFragment percent-encodes everything that would confuse podkop's shell
// loop or LuCI's decodeURIComponent. Emoji are left as they are — they survive
// both, and the label is what the user recognises the node by.
func encodeFragment(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		if needsEscape(r) {
			for _, b := range []byte(string(r)) {
				sb.WriteString(fmt.Sprintf("%%%02X", b))
			}
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

func needsEscape(r rune) bool {
	switch r {
	case '\'', '\\', '"', '%', '#', '`', '$':
		return true
	}
	return unicode.IsSpace(r) || r < 0x20 || r == 0x7f
}

func trimRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	n := 0
	for i := range s {
		if n == max {
			return s[:i]
		}
		n++
	}
	return s
}
