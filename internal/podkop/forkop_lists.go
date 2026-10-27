package podkop

import (
	"context"
	"fmt"
	"os"
	pathpkg "path"
	"regexp"
	"strings"

	"lst-signbox-lists-tgbot/internal/lists"
)

// forkop takes plain list files as rule sets: from rule_set it uses only the
// domains, from rule_set_with_subnets the IP/CIDR as well.
const (
	forkopDomainListsOption = "rule_set"
	forkopSubnetListsOption = "rule_set_with_subnets"
)

// ipNameRe is how an IP list is told apart by name while it has no entries
// yet: "ip_list.lst", "main_ip_list.lst", "subnets.lst".
var ipNameRe = regexp.MustCompile(`(?i)(^|[_.-])(ip|ips|subnet|subnets|cidr)([_.-]|$)`)

func nameSaysIP(path string) bool {
	return ipNameRe.MatchString(pathpkg.Base(path))
}

// listFileType decides which of the section's two lists a file from
// rule_set_with_subnets is: an IP list holds IP/CIDR only, anything else is a
// domain list. A file without entries goes by its name. It is a variable so
// tests need no files.
var listFileType = func(path string) lists.EntryType {
	if entries, err := lists.ReadFile(path); err == nil {
		hasIP := false
		for _, e := range entries {
			switch lists.ClassifyToken(e.Value) {
			case lists.TypeDomain:
				return lists.TypeDomain
			case lists.TypeIP:
				hasIP = true
			}
		}
		if hasIP {
			return lists.TypeIP
		}
	}
	if nameSaysIP(path) {
		return lists.TypeIP
	}
	return lists.TypeDomain
}

// ValidateBindPath is ValidatePath plus what the installed backend needs of a
// list file of this type.
func ValidateBindPath(t lists.EntryType, path string) error {
	if err := ValidatePath(path); err != nil {
		return err
	}
	b, err := detect()
	if err != nil {
		return err
	}
	return validateForBackend(b, t, path)
}

func validateForBackend(b Backend, t lists.EntryType, path string) error {
	if !b.IsForkop() {
		return nil
	}
	// The name is what tells an empty file's type apart, so it has to agree.
	if t == lists.TypeIP && !nameSaysIP(path) {
		return fmt.Errorf("в forkop имя файла со списком IP должно содержать ip, например ip_list.lst")
	}
	return nil
}

func listOption(b Backend, t lists.EntryType) string {
	if !b.IsForkop() {
		return OptionName(t)
	}
	if t == lists.TypeIP {
		return forkopSubnetListsOption
	}
	return forkopDomainListsOption
}

// EmptyListFiles names the list files of enabled forkop sections that are
// missing or hold no entry: forkop's core refuses to start with one. podkop
// skips such files, so for it there are none.
func EmptyListFiles(ctx context.Context) ([]string, error) {
	b, err := detect()
	if err != nil {
		return nil, err
	}
	if !b.IsForkop() {
		return nil, nil
	}
	secs, err := sectionsOf(ctx, b)
	if err != nil {
		return nil, err
	}
	var empty []string
	seen := make(map[string]bool)
	for _, s := range secs {
		if s.Disabled {
			continue
		}
		for _, path := range append(append([]string{}, s.DomainLists...), s.SubnetLists...) {
			if seen[path] {
				continue
			}
			seen[path] = true
			if !fileHoldsEntries(path) {
				empty = append(empty, path)
			}
		}
	}
	return empty, nil
}

var readListFile = os.ReadFile

// fileHoldsEntries reads a list file the way forkop does: comments after "#"
// or "//" and blank lines are no entries.
func fileHoldsEntries(path string) bool {
	data, err := readListFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line, _, _ = strings.Cut(line, "#")
		line, _, _ = strings.Cut(line, "//")
		if strings.TrimSpace(line) != "" {
			return true
		}
	}
	return false
}
