package version

import (
	"strconv"
	"strings"
)

// Set at link time via -ldflags.
var (
	Version = "dev"
	Release = "0"
)

const repoLatestURL = "https://api.github.com/repos/bpGusar/signbox-custom-lists-tgbot/releases/latest"

func Display() string {
	if Version == "" || Version == "dev" {
		return "dev"
	}
	return Normalize(Version)
}

func IsDev() bool {
	return Version == "" || Version == "dev"
}

func Normalize(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 0 && (s[0] == 'v' || s[0] == 'V') {
		s = s[1:]
	}
	if i := strings.Index(s, "-r"); i >= 0 {
		s = s[:i]
	}
	return s
}

func Compare(a, b string) int {
	pa := strings.Split(Normalize(a), ".")
	pb := strings.Split(Normalize(b), ".")
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		va, vb := 0, 0
		if i < len(pa) {
			va, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			vb, _ = strconv.Atoi(pb[i])
		}
		if va < vb {
			return -1
		}
		if va > vb {
			return 1
		}
	}
	return 0
}
