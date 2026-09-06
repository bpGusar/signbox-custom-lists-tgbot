// Package podkop reads the podkop UCI config: which routing sections exist and
// which list files each of them is fed from.
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
	// ConnectionType is podkop's own label for what the section does
	// ("proxy", "exclusion", …); empty when the option is unset.
	ConnectionType string
	// DomainLists and SubnetLists are the paths from local_domain_lists and
	// local_subnet_lists. Both are UCI lists, so a section can be fed from
	// several files at once.
	DomainLists []string
	SubnetLists []string
	// ProxyConfigType is how the section gets its outbound: url, selector,
	// urltest or outbound.
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

// OptionName is the UCI option a list of the given type lives in.
func OptionName(t lists.EntryType) string {
	if t == lists.TypeDomain {
		return "local_domain_lists"
	}
	return "local_subnet_lists"
}

var (
	// sectionDecl matches the lines of "uci show podkop" that declare a
	// routing section.
	sectionDecl = regexp.MustCompile(`^podkop\.([A-Za-z0-9_]+)=section$`)
	// optionLine matches "podkop.<section>.<option>=<value>".
	optionLine = regexp.MustCompile(`^podkop\.([A-Za-z0-9_]+)\.([A-Za-z0-9_]+)=(.*)$`)
	// quoted picks the individual values out of a UCI list, which uci prints
	// as quoted words on one line.
	quoted = regexp.MustCompile(`'([^']*)'`)
	// sectionNameRe guards the names before they are pasted into a uci
	// argument.
	sectionNameRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
)

// Available reports whether this router has a podkop config to read at all.
func Available(ctx context.Context) bool {
	_, err := run(ctx, "show", "podkop")
	return err == nil
}

// Sections lists podkop's routing sections in config order, each with the list
// files bound to it. It reads the whole config in one call, because spawning a
// uci process per option is noticeable on the routers this runs on.
func Sections(ctx context.Context) ([]Section, error) {
	out, err := run(ctx, "show", "podkop")
	if err != nil {
		return nil, fmt.Errorf("uci show podkop: %w", err)
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
			index[m[1]] = len(sections)
			sections = append(sections, Section{Name: m[1]})
			continue
		}

		m := optionLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		i, ok := index[m[1]]
		if !ok {
			// An option of the global "settings" section, or of one declared
			// after its own options, which uci never does.
			continue
		}
		switch m[2] {
		case "connection_type":
			sections[i].ConnectionType = firstValue(parseValues(m[3]))
		case OptionName(lists.TypeDomain):
			sections[i].DomainLists = parseValues(m[3])
		case OptionName(lists.TypeIP):
			sections[i].SubnetLists = parseValues(m[3])
		case "proxy_config_type":
			sections[i].ProxyConfigType = firstValue(parseValues(m[3]))
		case "proxy_string":
			sections[i].ProxyString = firstValue(parseValues(m[3]))
		case urltestLinksOption, selectorLinksOption:
			if linksByOption[i] == nil {
				linksByOption[i] = make(map[string][]string, 2)
			}
			linksByOption[i][m[2]] = parseValues(m[3])
		}
	}

	for i := range sections {
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

// Bind adds path to the section's list option and commits, so podkop picks the
// file up on its next restart.
func Bind(ctx context.Context, section string, t lists.EntryType, path string) error {
	if !sectionNameRe.MatchString(section) {
		return fmt.Errorf("некорректное имя секции: %q", section)
	}
	if err := ValidatePath(path); err != nil {
		return err
	}
	if _, err := run(ctx, "add_list", "podkop."+section+"."+OptionName(t)+"="+path); err != nil {
		return fmt.Errorf("uci add_list: %w", err)
	}
	if _, err := run(ctx, "commit", "podkop"); err != nil {
		return fmt.Errorf("uci commit podkop: %w", err)
	}
	return nil
}

// pathRe is what a list path may look like. The path reaches this code from a
// chat message and ends up both in podkop's config and as a file the bot
// writes to, so the charset is kept to what a list file plausibly needs.
var pathRe = regexp.MustCompile(`^(/[A-Za-z0-9_.-]+)+$`)

const maxPathLen = 200

// ValidatePath rejects anything that should never reach the podkop config.
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
