package service

import (
	"context"
	"os/exec"
	"strings"
)

type PodkopBindings struct {
	DomainBound bool
	IPBound     bool
}

func CheckPodkopBindings(ctx context.Context, domainList, ipList string) (PodkopBindings, error) {
	var st PodkopBindings

	out, err := exec.CommandContext(ctx, "uci", "show", "podkop").Output()
	if err != nil {
		return st, err
	}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := normalizeUCIValue(parts[1])

		if domainList != "" && strings.HasSuffix(key, ".local_domain_lists") && val == domainList {
			st.DomainBound = true
		}
		if ipList != "" && strings.HasSuffix(key, ".local_subnet_lists") && val == ipList {
			st.IPBound = true
		}
		if st.DomainBound && st.IPBound {
			break
		}
	}

	return st, nil
}

func normalizeUCIValue(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
