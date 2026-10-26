package podkop

import (
	"fmt"
	pathpkg "path"
	"regexp"
	"strings"

	"lst-signbox-lists-tgbot/internal/lists"
)

// forkopListsOption is where forkop takes plain list files from. It has no
// separate domain and subnet options: every file feeds the section with both
// the domains and the IP/CIDR in it, and only .lst files are accepted.
const forkopListsOption = "domain_ip_lists"

const forkopListExt = ".lst"

// ipNameRe is how an IP list is told apart by name while it has no entries
// yet: "ip_list.lst", "main_ip_list.lst", "subnets.lst".
var ipNameRe = regexp.MustCompile(`(?i)(^|[_.-])(ip|ips|subnet|subnets|cidr)([_.-]|$)`)

func nameSaysIP(path string) bool {
	return ipNameRe.MatchString(pathpkg.Base(path))
}

// listFileType decides which of the section's two lists a forkop file is: an
// IP list holds IP/CIDR only, anything else is a domain list. A file without
// entries goes by its name. It is a variable so tests need no files.
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
	if !strings.HasSuffix(path, forkopListExt) {
		return fmt.Errorf("forkop принимает только файлы %s", forkopListExt)
	}
	// The name is what tells an empty file's type apart, so it has to agree.
	if t == lists.TypeIP && !nameSaysIP(path) {
		return fmt.Errorf("в forkop имя файла со списком IP должно содержать ip, например ip_list%s", forkopListExt)
	}
	if t == lists.TypeDomain && nameSaysIP(path) {
		return fmt.Errorf("в forkop имя файла со списком доменов не должно содержать ip, subnet или cidr")
	}
	return nil
}

func listOption(b Backend, t lists.EntryType) string {
	if b.IsForkop() {
		return forkopListsOption
	}
	return OptionName(t)
}
