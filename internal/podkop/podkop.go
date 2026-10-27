// Package podkop reads the podkop or forkop UCI config: which routing sections
// exist and which list files each of them is fed from.
package podkop

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"lst-signbox-lists-tgbot/internal/lists"
)

// Section is one podkop routing section — "config section 'main'" in
// /etc/config/podkop. The global "settings" section is not one of these.
type Section struct {
	Name string
	// Label is forkop's display name of the section, which is also the tag
	// of its outbound group; empty when unset and for podkop.
	Label string
	// Disabled is set for a forkop section with enabled='0'.
	Disabled bool
	// ConnectionType is the backend's own label for what the section does:
	// podkop's connection_type ("proxy", "exclusion", …) or forkop's action
	// ("connection", "block", …); empty when the option is unset.
	ConnectionType string
	// DomainLists and SubnetLists are the list files feeding the section.
	// Both are UCI lists, so a section can be fed from several files at once.
	DomainLists []string
	SubnetLists []string
	// ProxyConfigType is how the section gets its outbound: url, selector,
	// urltest or outbound. A forkop connection section always counts as a
	// selector.
	ProxyConfigType string
	// ProxyLinks are the section's links: podkop's urltest_proxy_links or
	// selector_proxy_links, forkop's connection_link children.
	ProxyLinks []string
	// ProxyString is the single link a proxy_config_type=url section carries.
	ProxyString string
}

// Lists returns the paths feeding the section for one kind of entry.
func (s Section) Lists(t lists.EntryType) []string {
	if t == lists.TypeDomain {
		return s.DomainLists
	}
	return s.SubnetLists
}

func (s Section) groupTag(b Backend) string {
	if b.IsForkop() && s.Label != "" {
		return s.Label
	}
	return s.Name
}

// OptionName is the podkop UCI option a list of the given type lives in.
func OptionName(t lists.EntryType) string {
	if t == lists.TypeDomain {
		return "local_domain_lists"
	}
	return "local_subnet_lists"
}

var (
	// sectionID is how "uci show" names a section: by name, or as
	// "@type[index]" when it is anonymous.
	sectionID = `@?[A-Za-z0-9_]+(?:\[-?[0-9]+\])?`
	// declLine matches "<config>.<section>=<type>".
	declLine = regexp.MustCompile(`^([a-z]+)\.(` + sectionID + `)=([A-Za-z0-9_]+)$`)
	// optionLine matches "<config>.<section>.<option>=<value>".
	optionLine = regexp.MustCompile(`^([a-z]+)\.(` + sectionID + `)\.([A-Za-z0-9_]+)=(.*)$`)
	// quoted picks the individual values out of a UCI list, which uci prints
	// as quoted words on one line.
	quoted = regexp.MustCompile(`'([^']*)'`)
	// sectionNameRe guards the names before they are pasted into a uci
	// argument.
	sectionNameRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
	// anonIndexRe reads the index out of an anonymous section's name.
	anonIndexRe = regexp.MustCompile(`^@[A-Za-z0-9_]+\[([0-9]+)\]$`)
)

// forkopConnection is forkop's action for a section that routes through
// proxies.
const forkopConnection = "connection"

// uciSection is one section of "uci show" output with its options as printed.
type uciSection struct {
	id      string
	typ     string
	options map[string][]string
}

func (s *uciSection) value(option string) string {
	return firstValue(s.options[option])
}

// Available reports whether this router has a backend config to read at all.
func Available(ctx context.Context) bool {
	b, err := detect()
	if err != nil {
		return false
	}
	_, err = run(ctx, "show", b.Name)
	return err == nil
}

// Sections lists the backend's routing sections in config order, each with
// the list files bound to it. It reads the whole config in one call, because
// spawning a uci process per option is noticeable on the routers this runs on.
func Sections(ctx context.Context) ([]Section, error) {
	b, err := detect()
	if err != nil {
		return nil, err
	}
	return sectionsOf(ctx, b)
}

func sectionsOf(ctx context.Context, b Backend) ([]Section, error) {
	cfg, err := readConfig(ctx, b)
	if err != nil {
		return nil, err
	}
	return cfg.sections, nil
}

// config is the backend's config as the bot needs it.
type config struct {
	sections []Section
	// links are forkop's connection_link children per section, in order.
	links map[string][]forkopLink
	// childNames are the names forkop's children already hold per section;
	// a new link must not take one of them.
	childNames map[string]map[string]bool
}

type forkopLink struct {
	id    string
	uri   string
	name  string
	order int
}

func readConfig(ctx context.Context, b Backend) (config, error) {
	out, err := run(ctx, "show", b.Name)
	if err != nil {
		return config{}, fmt.Errorf("uci show %s: %w", b.Name, err)
	}
	all := parseShow(out, b.Name)
	if b.IsForkop() {
		return forkopConfig(all), nil
	}
	return config{sections: podkopSections(all)}, nil
}

// parseShow reads "uci show <config>" into its sections in config order.
func parseShow(out, configName string) []*uciSection {
	var sections []*uciSection
	byID := make(map[string]*uciSection)

	// uci prints values verbatim, and a value may span lines —
	// user_domains_text holds a whole list, any line of which can look exactly
	// like a config line. So lines are only read while the parser is not
	// inside a quoted value.
	inValue := false
	for _, line := range strings.Split(out, "\n") {
		toggles := quoteCount(line)%2 == 1
		if inValue {
			inValue = !toggles
			continue
		}
		if inValue = toggles; inValue {
			// A value opens here and continues on the next line. Nothing the
			// bot needs is written that way.
			continue
		}

		line = strings.TrimSpace(line)
		if m := declLine.FindStringSubmatch(line); m != nil {
			if m[1] != configName {
				continue
			}
			s := &uciSection{id: m[2], typ: m[3], options: make(map[string][]string)}
			byID[m[2]] = s
			sections = append(sections, s)
			continue
		}

		m := optionLine.FindStringSubmatch(line)
		if m == nil || m[1] != configName {
			continue
		}
		if s, ok := byID[m[2]]; ok {
			s.options[m[3]] = parseValues(m[4])
		}
	}
	return sections
}

func podkopSections(all []*uciSection) []Section {
	var sections []Section
	for _, u := range all {
		if u.typ != "section" || !sectionNameRe.MatchString(u.id) {
			continue
		}
		s := Section{
			Name:            u.id,
			ConnectionType:  u.value("connection_type"),
			DomainLists:     u.options[OptionName(lists.TypeDomain)],
			SubnetLists:     u.options[OptionName(lists.TypeIP)],
			ProxyConfigType: u.value("proxy_config_type"),
			ProxyString:     u.value("proxy_string"),
		}
		if option := ProxyLinksOption(s.ProxyConfigType); option != "" {
			s.ProxyLinks = u.options[option]
		}
		sections = append(sections, s)
	}
	return sections
}

// forkopChildNameOptions are the options forkop's children of a section are
// named by. The names run as "<section>/<name>", so they are unique within
// the section across all kinds of children.
var forkopChildNameOptions = map[string][]string{
	"connection_link":   {"name"},
	"tunnel":            {"name"},
	"urltest":           {"name"},
	"fallback_group":    {"name"},
	"section_interface": {"display_name", "name"},
}

func forkopConfig(all []*uciSection) config {
	cfg := config{
		links:      make(map[string][]forkopLink),
		childNames: make(map[string]map[string]bool),
	}
	for _, u := range all {
		owner := u.value("section")
		if owner == "" {
			continue
		}
		if u.typ == "connection_link" {
			order, _ := strconv.Atoi(u.value("order"))
			cfg.links[owner] = append(cfg.links[owner], forkopLink{
				id: u.id, uri: u.value("uri"), name: strings.TrimSpace(u.value("name")), order: order,
			})
		}
		for _, option := range forkopChildNameOptions[u.typ] {
			if name := strings.TrimSpace(u.value(option)); name != "" {
				if cfg.childNames[owner] == nil {
					cfg.childNames[owner] = make(map[string]bool)
				}
				cfg.childNames[owner][name] = true
				break
			}
		}
	}
	for _, links := range cfg.links {
		sort.SliceStable(links, func(i, j int) bool { return links[i].order < links[j].order })
	}

	for _, u := range all {
		if u.typ != "section" || !sectionNameRe.MatchString(u.id) {
			continue
		}
		s := Section{
			Name:           u.id,
			Label:          strings.TrimSpace(u.value("label")),
			Disabled:       u.value("enabled") == "0",
			ConnectionType: u.value("action"),
		}
		// URLs are downloaded by forkop itself; only files are the bot's.
		for _, path := range u.options[forkopDomainListsOption] {
			if strings.HasPrefix(path, "/") {
				s.DomainLists = append(s.DomainLists, path)
			}
		}
		for _, path := range u.options[forkopSubnetListsOption] {
			if !strings.HasPrefix(path, "/") {
				continue
			}
			if listFileType(path) == lists.TypeIP {
				s.SubnetLists = append(s.SubnetLists, path)
			} else {
				s.DomainLists = append(s.DomainLists, path)
			}
		}
		if s.ConnectionType == forkopConnection {
			s.ProxyConfigType = ProxyTypeSelector
			for _, l := range cfg.links[s.Name] {
				s.ProxyLinks = append(s.ProxyLinks, l.uri)
			}
		}
		cfg.sections = append(cfg.sections, s)
	}
	return cfg
}

// quoteCount counts the quotes that open or close a value, ignoring the
// escape sequence uci prints for a quote inside one.
func quoteCount(line string) int {
	return strings.Count(strings.ReplaceAll(line, `'\''`, ""), "'")
}

// parseValues splits the right-hand side of a uci line. A list comes back as
// several quoted words, a plain option as one.
func parseValues(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if !strings.Contains(raw, "'") {
		return []string{raw}
	}
	var values []string
	for _, m := range quoted.FindAllStringSubmatch(raw, -1) {
		if v := strings.TrimSpace(m[1]); v != "" {
			values = append(values, v)
		}
	}
	return values
}

func firstValue(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

// Bind adds path to the section's list option and commits, so the backend
// picks the file up on its next restart.
func Bind(ctx context.Context, section string, t lists.EntryType, path string) error {
	if !sectionNameRe.MatchString(section) {
		return fmt.Errorf("некорректное имя секции: %q", section)
	}
	if err := ValidatePath(path); err != nil {
		return err
	}
	b, err := detect()
	if err != nil {
		return err
	}
	if err := validateForBackend(b, t, path); err != nil {
		return err
	}
	if b.IsForkop() && t == lists.TypeIP {
		_, s, err := findSection(ctx, section)
		if err != nil {
			return err
		}
		if s.ConnectionType == "dns" {
			return fmt.Errorf("DNS-секция forkop берёт из списков только домены — список IP к ней не привязать")
		}
	}
	if _, err := run(ctx, "add_list", b.Name+"."+section+"."+listOption(b, t)+"="+path); err != nil {
		return fmt.Errorf("uci add_list: %w", err)
	}
	return commit(ctx, b)
}

func commit(ctx context.Context, b Backend) error {
	if _, err := run(ctx, "commit", b.Name); err != nil {
		return fmt.Errorf("uci commit %s: %w", b.Name, err)
	}
	return nil
}

// pathRe is what a list path may look like. The path reaches this code from a
// chat message and ends up both in the backend's config and as a file the bot
// writes to, so the charset is kept to what a list file plausibly needs.
var pathRe = regexp.MustCompile(`^(/[A-Za-z0-9_.-]+)+$`)

const maxPathLen = 200

// ValidatePath rejects anything that should never reach the backend's config.
func ValidatePath(path string) error {
	switch {
	case path == "":
		return fmt.Errorf("пустой путь")
	case !strings.HasPrefix(path, "/"):
		return fmt.Errorf("путь должен быть абсолютным и начинаться с /")
	case len(path) > maxPathLen:
		return fmt.Errorf("путь длиннее %d символов", maxPathLen)
	case strings.HasSuffix(path, "/"):
		return fmt.Errorf("путь должен указывать на файл, а не на каталог")
	case !pathRe.MatchString(path):
		return fmt.Errorf("в пути допустимы только латиница, цифры и символы . _ - /")
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".." {
			return fmt.Errorf("путь не должен содержать ..")
		}
	}
	return nil
}
