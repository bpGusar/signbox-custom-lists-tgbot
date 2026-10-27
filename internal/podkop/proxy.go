package podkop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
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
	return (s.ConnectionType == "proxy" || s.ConnectionType == forkopConnection) &&
		ProxyLinksOption(s.ProxyConfigType) != ""
}

// ConvertibleToURLTest reports whether the section holds a single link today
// and could be switched to a list without losing it.
func (s Section) ConvertibleToURLTest() bool {
	return s.ConnectionType == "proxy" && s.ProxyConfigType == ProxyTypeURL
}

// ValidateProxyLink rejects anything that must not reach the backend's config.
// podkop expands the list unquoted, so a value with whitespace in it would
// break config generation on the next restart — this is the last place that
// can be caught.
func ValidateProxyLink(raw string) error {
	return proxylink.ValidateRaw(raw)
}

// AddProxyLinks appends the links the section does not have yet and commits
// once at the end. Links equal but for their label count as present.
func AddProxyLinks(ctx context.Context, section string, links []string) (added, skipped int, err error) {
	b, cfg, s, option, err := proxyTarget(ctx, section)
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

	if b.IsForkop() {
		order := 0
		for _, l := range cfg.links[section] {
			if l.order >= order {
				order = l.order + 1
			}
		}
		taken := copyNames(cfg.childNames[section])
		for _, l := range pending {
			if err := addForkopLink(ctx, section, l, nextLinkName(taken, l), order); err != nil {
				return added, skipped, err
			}
			order++
			added++
		}
		return added, skipped, commit(ctx, b)
	}

	for _, l := range pending {
		if _, err := run(ctx, "add_list", b.Name+"."+section+"."+option+"="+l); err != nil {
			return added, skipped, fmt.Errorf("uci add_list: %w", err)
		}
		added++
	}
	if err := commit(ctx, b); err != nil {
		return added, skipped, err
	}
	return added, skipped, nil
}

// ReplaceProxyLinks drops the section's list and writes the given links in its
// place.
func ReplaceProxyLinks(ctx context.Context, section string, links []string) error {
	b, cfg, _, option, err := proxyTarget(ctx, section)
	if err != nil {
		return err
	}
	for _, l := range links {
		if err := ValidateProxyLink(l); err != nil {
			return fmt.Errorf("ссылка не прошла проверку: %w", err)
		}
	}

	if b.IsForkop() {
		return replaceForkopLinks(ctx, b, cfg, section, links)
	}

	// A section that has no list yet has nothing to delete, and uci says so
	// with an error — the add_list calls below create the option either way.
	_, _ = run(ctx, "delete", b.Name+"."+section+"."+option)

	for _, l := range links {
		if _, err := run(ctx, "add_list", b.Name+"."+section+"."+option+"="+l); err != nil {
			return fmt.Errorf("uci add_list: %w", err)
		}
	}
	return commit(ctx, b)
}

func replaceForkopLinks(ctx context.Context, b Backend, cfg config, section string, links []string) error {
	old := cfg.links[section]
	ids := make([]string, 0, len(old))
	// The names of the deleted links are free again, the other children's
	// are not.
	taken := copyNames(cfg.childNames[section])
	for _, l := range old {
		ids = append(ids, l.id)
		delete(taken, l.name)
	}
	// Deleting an anonymous section renumbers the ones after it, so they go
	// from the last one back.
	sort.SliceStable(ids, func(i, j int) bool { return anonIndex(ids[i]) > anonIndex(ids[j]) })
	for _, id := range ids {
		if _, err := run(ctx, "delete", b.Name+"."+id); err != nil {
			return fmt.Errorf("uci delete: %w", err)
		}
	}

	for i, l := range links {
		if err := addForkopLink(ctx, section, l, nextLinkName(taken, l), i); err != nil {
			return err
		}
	}
	return commit(ctx, b)
}

// ConvertToURLTest switches a single-link section over to a list, carrying the
// link it already had into it. Nothing here happens without the user saying so:
// it changes how podkop picks its outbound.
func ConvertToURLTest(ctx context.Context, section string) error {
	if !sectionNameRe.MatchString(section) {
		return fmt.Errorf("некорректное имя секции: %q", section)
	}
	b, s, err := findSection(ctx, section)
	if err != nil {
		return err
	}
	if !s.ConvertibleToURLTest() {
		return fmt.Errorf("секция «%s» не типа URL — переключать нечего", section)
	}

	if _, err := run(ctx, "set", b.Name+"."+section+".proxy_config_type="+ProxyTypeURLTest); err != nil {
		return fmt.Errorf("uci set proxy_config_type: %w", err)
	}
	if link := strings.TrimSpace(s.ProxyString); link != "" {
		if err := ValidateProxyLink(link); err == nil {
			if _, err := run(ctx, "add_list", b.Name+"."+section+"."+urltestLinksOption+"="+link); err != nil {
				return fmt.Errorf("uci add_list: %w", err)
			}
		}
	}
	return commit(ctx, b)
}

// GroupLatency asks the backend what the links in a section actually measure
// now that they are live, keyed by the name it gave each outbound.
func GroupLatency(ctx context.Context, section string) (map[string]int, error) {
	if !sectionNameRe.MatchString(section) {
		return nil, fmt.Errorf("некорректное имя секции: %q", section)
	}
	b, err := detect()
	if err != nil {
		return nil, err
	}
	group := section
	var out string
	if b.IsForkop() {
		_, s, findErr := findSection(ctx, section)
		if findErr != nil {
			return nil, findErr
		}
		group = s.groupTag(b)
		out, err = forkopGroupDelay(ctx, group)
	} else {
		out, err = cliRun(ctx, b.Name, "clash_api", "get_group_latency", group)
	}
	if err != nil {
		return nil, fmt.Errorf("%s clash_api: %w", b.Name, err)
	}
	latency := parseLatency(out)
	if len(latency) == 0 {
		return nil, fmt.Errorf("%s не вернул задержки", b.Name)
	}
	if b.IsForkop() {
		latency = trimGroupPrefix(latency, group)
	}
	return latency, nil
}

const (
	forkopDelayURL     = "https://www.gstatic.com/generate_204"
	forkopDelayTimeout = 5000
)

// forkopGroupDelay goes through forkop's allowlisted Clash API transport,
// which answers with {"status": …, "body": "…"}, and returns the body.
func forkopGroupDelay(ctx context.Context, group string) (string, error) {
	path := "/group/" + url.PathEscape(group) + "/delay?url=" + url.QueryEscape(forkopDelayURL) +
		"&timeout=" + strconv.Itoa(forkopDelayTimeout)
	out, err := cliRun(ctx, Forkop.Name, "clash_api", "GET", path)
	if err != nil {
		return "", err
	}
	var reply struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &reply); err != nil {
		return "", fmt.Errorf("ответ не разобран: %w", err)
	}
	if reply.Status < 200 || reply.Status > 299 {
		return "", fmt.Errorf("Clash API ответил %d: %s", reply.Status, strings.TrimSpace(reply.Body))
	}
	return reply.Body, nil
}

// trimGroupPrefix drops the "<section>/" forkop puts before each link's name.
func trimGroupPrefix(latency map[string]int, group string) map[string]int {
	out := make(map[string]int, len(latency))
	for tag, ms := range latency {
		out[strings.TrimPrefix(tag, group+"/")] = ms
	}
	return out
}

// cliRun executes the backend's CLI. Separate from run so tests can answer for
// one without answering for uci.
var cliRun = func(ctx context.Context, name string, args ...string) (string, error) {
	return runCmd(ctx, name, args...)
}

// latencyLine matches the "<tag> ... <number>" shape podkop prints when its
// output is not JSON.
var latencyLine = regexp.MustCompile(`^\s*([^\s:]+)\s*[:=]?\s*(\d+)\s*(?:ms|мс)?\s*$`)

// parseLatency reads the backend's answer either way it comes: the Clash API's JSON
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
func proxyTarget(ctx context.Context, section string) (Backend, config, Section, string, error) {
	if !sectionNameRe.MatchString(section) {
		return Backend{}, config{}, Section{}, "", fmt.Errorf("некорректное имя секции: %q", section)
	}
	b, err := detect()
	if err != nil {
		return Backend{}, config{}, Section{}, "", err
	}
	cfg, err := readConfig(ctx, b)
	if err != nil {
		return Backend{}, config{}, Section{}, "", err
	}
	s, err := pickSection(b, cfg.sections, section)
	if err != nil {
		return Backend{}, config{}, Section{}, "", err
	}
	option := ProxyLinksOption(s.ProxyConfigType)
	if option == "" {
		return Backend{}, config{}, Section{}, "", fmt.Errorf("секция «%s» не принимает список ссылок (тип %q)", section, s.ProxyConfigType)
	}
	return b, cfg, s, option, nil
}

func findSection(ctx context.Context, name string) (Backend, Section, error) {
	b, err := detect()
	if err != nil {
		return Backend{}, Section{}, err
	}
	secs, err := sectionsOf(ctx, b)
	if err != nil {
		return Backend{}, Section{}, err
	}
	s, err := pickSection(b, secs, name)
	return b, s, err
}

func pickSection(b Backend, secs []Section, name string) (Section, error) {
	for _, s := range secs {
		if s.Name == name {
			return s, nil
		}
	}
	return Section{}, fmt.Errorf("секции «%s» нет в конфиге %s", name, b.Name)
}

// addForkopLink writes one connection_link child of the section.
func addForkopLink(ctx context.Context, section, uri, name string, order int) error {
	out, err := run(ctx, "add", Forkop.Name, "connection_link")
	if err != nil {
		return fmt.Errorf("uci add: %w", err)
	}
	id := strings.TrimSpace(out)
	if !sectionNameRe.MatchString(id) {
		return fmt.Errorf("uci add вернул неожиданное имя секции: %q", id)
	}
	for _, kv := range [][2]string{
		{"section", section},
		{"uri", uri},
		{"name", name},
		{"order", strconv.Itoa(order)},
	} {
		if _, err := run(ctx, "set", Forkop.Name+"."+id+"."+kv[0]+"="+kv[1]); err != nil {
			return fmt.Errorf("uci set %s: %w", kv[0], err)
		}
	}
	return nil
}

// nextLinkName names a link the way forkop's page does: after its label with
// "／" for "/", or "Node"; a name the section already holds gets " #N" with
// the next free number. The name is taken in the set.
func nextLinkName(taken map[string]bool, link string) string {
	own := strings.ReplaceAll(strings.TrimSpace(linkLabel(link)), "/", "／")
	name := own
	if own == "" || taken[own] {
		base := own
		if base == "" {
			base = "Node"
		}
		marker := base + " #"
		highest := 0
		for t := range taken {
			if n, err := strconv.Atoi(strings.TrimPrefix(t, marker)); err == nil && strings.HasPrefix(t, marker) && n > highest {
				highest = n
			}
		}
		name = marker + strconv.Itoa(highest+1)
	}
	taken[name] = true
	return name
}

func linkLabel(link string) string {
	_, fragment, ok := strings.Cut(link, "#")
	if !ok {
		return ""
	}
	if decoded, err := url.PathUnescape(fragment); err == nil {
		return decoded
	}
	return fragment
}

func copyNames(names map[string]bool) map[string]bool {
	out := make(map[string]bool, len(names))
	for n := range names {
		out[n] = true
	}
	return out
}

func anonIndex(id string) int {
	m := anonIndexRe.FindStringSubmatch(id)
	if m == nil {
		return -1
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// linkKey is what makes two stored links the same one: everything but the
// label, the way the parser deduplicates a subscription file.
func linkKey(raw string) string {
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		return raw[:i]
	}
	return raw
}
