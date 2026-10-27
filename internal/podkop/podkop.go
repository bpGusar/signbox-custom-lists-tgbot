// Package podkop reads the podkop or forkop UCI config: which routing sections
// exist and which list files each of them is fed from.
package podkop

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"lst-signbox-lists-tgbot/internal/lists"
)

// Section is one podkop routing section — "config section 'main'" in
// /etc/config/podkop. The global "settings" section is not one of these.
type Section struct {
	Name string
	// ConnectionType is the backend's own label for what the section does:
	// podkop's connection_type ("proxy", "exclusion", …) or forkop's action
	// ("connection", "block", …); empty when the option is unset.
	ConnectionType string
	// DomainLists and SubnetLists are the paths from local_domain_lists and
	// local_subnet_lists. Both are UCI lists, so a section can be fed from
	// several files at once.
	DomainLists []string
	SubnetLists []string
	// ProxyConfigType is how the section gets its outbound: url, selector,
	// urltest or outbound. A forkop connection section always keeps a
	// selector list.
	ProxyConfigType string
	// ProxyLinks is the option matching ProxyConfigType — urltest_proxy_links
	// or selector_proxy_links. Both are UCI lists.
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

// OptionName is the podkop UCI option a list of the given type lives in.
func OptionName(t lists.EntryType) string {
	if t == lists.TypeDomain {
		return "local_domain_lists"
	}
	return "local_subnet_lists"
}

var (
	// sectionDecl matches the lines of "uci show <config>" that declare a
	// routing section.
	sectionDecl = regexp.MustCompile(`^([a-z]+)\.([A-Za-z0-9_]+)=section$`)
	// optionLine matches "<config>.<section>.<option>=<value>".
	optionLine = regexp.MustCompile(`^([a-z]+)\.([A-Za-z0-9_]+)\.([A-Za-z0-9_]+)=(.*)$`)
	// quoted picks the individual values out of a UCI list, which uci prints
	// as quoted words on one line.
	quoted = regexp.MustCompile(`'([^']*)'`)
	// sectionNameRe guards the names before they are pasted into a uci
	// argument.
	sectionNameRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
)

// forkopConnection is forkop's action for a section that routes through
// proxies; the older names it still accepts are folded into it.
const forkopConnection = "connection"

func normalizeForkopAction(action string) string {
	switch action {
	case "proxy", "outbound", "vpn":
		return forkopConnection
	}
	return action
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
	out, err := run(ctx, "show", b.Name)
	if err != nil {
		return nil, fmt.Errorf("uci show %s: %w", b.Name, err)
	}

	var sections []Section
	index := make(map[string]int)
	// Both link options are collected as they come: proxy_config_type may be
	// printed after them, and only it says which one is in use.
	linksByOption := make(map[int]map[string][]string)

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
		if m := sectionDecl.FindStringSubmatch(line); m != nil {
			if m[1] != b.Name {
				continue
			}
			index[m[2]] = len(sections)
			sections = append(sections, Section{Name: m[2]})
			continue
		}

		m := optionLine.FindStringSubmatch(line)
		if m == nil || m[1] != b.Name {
			continue
		}
		i, ok := index[m[2]]
		if !ok {
			// An option of the global "settings" section, or of one declared
			// after its own options, which uci never does.
			continue
		}
		option, value := m[3], m[4]
		switch {
		case option == "connection_type" && !b.IsForkop():
			sections[i].ConnectionType = firstValue(parseValues(value))
		case option == "action" && b.IsForkop():
			sections[i].ConnectionType = normalizeForkopAction(firstValue(parseValues(value)))
		case option == OptionName(lists.TypeDomain) && !b.IsForkop():
			sections[i].DomainLists = parseValues(value)
		case option == OptionName(lists.TypeIP) && !b.IsForkop():
			sections[i].SubnetLists = parseValues(value)
		case option == forkopListsOption && b.IsForkop():
			for _, path := range parseValues(value) {
				// URLs are downloaded by forkop itself; only files are the bot's.
				if !strings.HasPrefix(path, "/") {
					continue
				}
				if listFileType(path) == lists.TypeIP {
					sections[i].SubnetLists = append(sections[i].SubnetLists, path)
				} else {
					sections[i].DomainLists = append(sections[i].DomainLists, path)
				}
			}
		case option == "proxy_config_type" && !b.IsForkop():
			sections[i].ProxyConfigType = firstValue(parseValues(value))
		case option == "proxy_string" && !b.IsForkop():
			sections[i].ProxyString = firstValue(parseValues(value))
		case option == urltestLinksOption, option == selectorLinksOption:
			if linksByOption[i] == nil {
				linksByOption[i] = make(map[string][]string, 2)
			}
			linksByOption[i][option] = parseValues(value)
		}
	}

	for i := range sections {
		if b.IsForkop() && sections[i].ConnectionType == forkopConnection {
			sections[i].ProxyConfigType = ProxyTypeSelector
		}
		sections[i].ProxyLinks = linksByOption[i][ProxyLinksOption(sections[i].ProxyConfigType)]
	}
	return sections, nil
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
