package podkop

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"lst-signbox-lists-tgbot/internal/proxylink"
)

// The two UCI options that hold a list of proxy links. They are the same list
// as far as the bot is concerned — only the name differs with the section's
// proxy_config_type.
const (
	urltestLinksOption  = "urltest_proxy_links"
	selectorLinksOption = "selector_proxy_links"
)

// Proxy config types, as offered by luci-app-podkop.
const (
	ProxyTypeURL      = "url"
	ProxyTypeSelector = "selector"
	ProxyTypeURLTest  = "urltest"
	ProxyTypeOutbound = "outbound"
)

// ProxyLinksOption is the option a section of this type keeps its links in,
// empty for the types that have no list at all.
func ProxyLinksOption(t string) string {
	switch t {
	case ProxyTypeURLTest:
		return urltestLinksOption
	case ProxyTypeSelector:
		return selectorLinksOption
	}
	return ""
}

// AcceptsProxyLinks reports whether links can be written into the section as
// it is configured right now.
func (s Section) AcceptsProxyLinks() bool {
	return s.ConnectionType == "proxy" && ProxyLinksOption(s.ProxyConfigType) != ""
}

// ConvertibleToURLTest reports whether the section holds a single link today
// and could be switched to a list without losing it.
func (s Section) ConvertibleToURLTest() bool {
	return s.ConnectionType == "proxy" && s.ProxyConfigType == ProxyTypeURL
}

// ValidateProxyLink rejects anything that must not reach podkop's config.
// podkop expands the list unquoted, so a value with whitespace in it would
// break config generation on the next restart — this is the last place that
// can be caught.
func ValidateProxyLink(raw string) error {
	return proxylink.ValidateRaw(raw)
}

// AddProxyLinks appends the links the section does not have yet and commits
// once at the end. Links equal but for their label count as present.
func AddProxyLinks(ctx context.Context, section string, links []string) (added, skipped int, err error) {
	s, option, err := proxyTarget(ctx, section)
	if err != nil {
		return 0, 0, err
	}

	have := make(map[string]struct{}, len(s.ProxyLinks))
	for _, l := range s.ProxyLinks {
		have[linkKey(l)] = struct{}{}
	}

	var pending []string
	for _, l := range links {
		if err := ValidateProxyLink(l); err != nil {
			return 0, 0, fmt.Errorf("ссылка не прошла проверку: %w", err)
		}
		key := linkKey(l)
		if _, ok := have[key]; ok {
			skipped++
			continue
		}
		have[key] = struct{}{}
		pending = append(pending, l)
	}
	if len(pending) == 0 {
		return 0, skipped, nil
	}

	for _, l := range pending {
		if _, err := run(ctx, "add_list", "podkop."+section+"."+option+"="+l); err != nil {
			return added, skipped, fmt.Errorf("uci add_list: %w", err)
		}
		added++
	}
	if _, err := run(ctx, "commit", "podkop"); err != nil {
		return added, skipped, fmt.Errorf("uci commit podkop: %w", err)
	}
	return added, skipped, nil
}

// ReplaceProxyLinks drops the section's list and writes the given links in its
// place.
func ReplaceProxyLinks(ctx context.Context, section string, links []string) error {
	_, option, err := proxyTarget(ctx, section)
	if err != nil {
		return err
	}
	for _, l := range links {
		if err := ValidateProxyLink(l); err != nil {
			return fmt.Errorf("ссылка не прошла проверку: %w", err)
		}
	}

	// A section that has no list yet has nothing to delete, and uci says so
	// with an error — the add_list calls below create the option either way.
	_, _ = run(ctx, "delete", "podkop."+section+"."+option)

	for _, l := range links {
		if _, err := run(ctx, "add_list", "podkop."+section+"."+option+"="+l); err != nil {
			return fmt.Errorf("uci add_list: %w", err)
		}
	}
	if _, err := run(ctx, "commit", "podkop"); err != nil {
		return fmt.Errorf("uci commit podkop: %w", err)
	}
	return nil
}

// ConvertToURLTest switches a single-link section over to a list, carrying the
// link it already had into it. Nothing here happens without the user saying so:
// it changes how podkop picks its outbound.
func ConvertToURLTest(ctx context.Context, section string) error {
	if !sectionNameRe.MatchString(section) {
		return fmt.Errorf("некорректное имя секции: %q", section)
	}
	s, err := findSection(ctx, section)
	if err != nil {
		return err
	}
	if !s.ConvertibleToURLTest() {
		return fmt.Errorf("секция «%s» не типа URL — переключать нечего", section)
	}

	if _, err := run(ctx, "set", "podkop."+section+".proxy_config_type="+ProxyTypeURLTest); err != nil {
		return fmt.Errorf("uci set proxy_config_type: %w", err)
	}
	if link := strings.TrimSpace(s.ProxyString); link != "" {
		if err := ValidateProxyLink(link); err == nil {
			if _, err := run(ctx, "add_list", "podkop."+section+"."+urltestLinksOption+"="+link); err != nil {
				return fmt.Errorf("uci add_list: %w", err)
			}
		}
	}
	if _, err := run(ctx, "commit", "podkop"); err != nil {
		return fmt.Errorf("uci commit podkop: %w", err)
	}
	return nil
}

// GroupLatency asks podkop what the links in a section actually measure now
// that they are live, keyed by the tag podkop gave each outbound.
func GroupLatency(ctx context.Context, section string) (map[string]int, error) {
	if !sectionNameRe.MatchString(section) {
		return nil, fmt.Errorf("некорректное имя секции: %q", section)
	}
	out, err := podkopRun(ctx, "clash_api", "get_group_latency", section)
	if err != nil {
		return nil, fmt.Errorf("podkop clash_api: %w", err)
	}
	latency := parseLatency(out)
	if len(latency) == 0 {
		return nil, fmt.Errorf("podkop не вернул задержки")
	}
	return latency, nil
}

// podkopRun executes the podkop CLI. Separate from run so tests can answer for
// one without answering for uci.
var podkopRun = func(ctx context.Context, args ...string) (string, error) {
	return runCmd(ctx, "podkop", args...)
}

// latencyLine matches the "<tag> ... <number>" shape podkop prints when its
// output is not JSON.
var latencyLine = regexp.MustCompile(`^\s*([^\s:]+)\s*[:=]?\s*(\d+)\s*(?:ms|мс)?\s*$`)

// parseLatency reads podkop's answer either way it comes: the Clash API's JSON
// object, or a line per outbound.
func parseLatency(out string) map[string]int {
	trimmed := strings.TrimSpace(out)

	var asJSON map[string]json.Number
	if err := json.Unmarshal([]byte(trimmed), &asJSON); err == nil {
		latency := make(map[string]int, len(asJSON))
		for tag, v := range asJSON {
			if ms, err := v.Int64(); err == nil && ms > 0 {
				latency[tag] = int(ms)
			}
		}
		return latency
	}

	latency := make(map[string]int)
	for _, line := range strings.Split(trimmed, "\n") {
		m := latencyLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		ms, err := strconv.Atoi(m[2])
		if err != nil || ms <= 0 {
			continue
		}
		latency[m[1]] = ms
	}
	return latency
}

// proxyTarget resolves the section and the option its links live in, refusing
// anything the bot must not write to.
func proxyTarget(ctx context.Context, section string) (Section, string, error) {
	if !sectionNameRe.MatchString(section) {
		return Section{}, "", fmt.Errorf("некорректное имя секции: %q", section)
	}
	s, err := findSection(ctx, section)
	if err != nil {
		return Section{}, "", err
	}
	option := ProxyLinksOption(s.ProxyConfigType)
	if option == "" {
		return Section{}, "", fmt.Errorf("секция «%s» не принимает список ссылок (тип %q)", section, s.ProxyConfigType)
	}
	return s, option, nil
}

func findSection(ctx context.Context, name string) (Section, error) {
	secs, err := Sections(ctx)
	if err != nil {
		return Section{}, err
	}
	for _, s := range secs {
		if s.Name == name {
			return s, nil
		}
	}
	return Section{}, fmt.Errorf("секции «%s» нет в конфиге podkop", name)
}

// linkKey is what makes two stored links the same one: everything but the
// label, the way the parser deduplicates a subscription file.
func linkKey(raw string) string {
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		return raw[:i]
	}
	return raw
}
