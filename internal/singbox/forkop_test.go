package singbox

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"lst-signbox-lists-tgbot/internal/podkop"
)

func useForkop(t *testing.T, version string) {
	t.Helper()
	prevRun, prevStat, prevDetect := runner, fileExists, detectBackend
	t.Cleanup(func() { runner, fileExists, detectBackend = prevRun, prevStat, prevDetect })

	detectBackend = func() (podkop.Backend, error) { return podkop.Forkop, nil }
	fileExists = func(string) bool { return false }
	runner = func(_ context.Context, name string, _ []string, _ string) (string, error) {
		if name == "sing-box" {
			return "sing-box version " + version + "\n", nil
		}
		return "", errors.New("unexpected command " + name)
	}
}

func TestAvailableForkop(t *testing.T) {
	useForkop(t, "1.14.4-r1")
	if ok, reason := Available(context.Background()); !ok {
		t.Fatalf("Available = false (%s)", reason)
	}

	useForkop(t, "1.13.18-extended-2.6.5")
	if ok, reason := Available(context.Background()); ok || !strings.Contains(reason, "1.14.4") {
		t.Fatalf("a core older than sing-box-forkop must be refused, got %t %q", ok, reason)
	}
}

func TestBuildForkopOutboundsUsesParser(t *testing.T) {
	useForkop(t, "1.14.4-r1")

	links := parseLinks(t,
		"vless://uuid@1.2.3.4:8443#⚡ Первый узел",
		"hysteria2://pass@5.6.7.8:30443#⚡ Второй",
	)
	tc, _, err := check(context.Background())
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	outbounds, tags, err := buildOutbounds(context.Background(), tc, links)
	if err != nil {
		t.Fatalf("buildOutbounds: %v", err)
	}
	if len(outbounds) != 2 || tags[0] != "probe1" || tags[1] != "probe2" {
		t.Fatalf("tags: %v", tags)
	}
	for i, raw := range outbounds {
		var ob map[string]string
		if err := json.Unmarshal(raw, &ob); err != nil {
			t.Fatalf("outbound %d: %v", i, err)
		}
		want := map[string]string{"type": "parser", "tag": tags[i], "link": links[i].ForUCI()}
		if len(ob) != len(want) || ob["type"] != want["type"] || ob["tag"] != want["tag"] || ob["link"] != want["link"] {
			t.Fatalf("outbound %d = %v, want %v", i, ob, want)
		}
	}
}
