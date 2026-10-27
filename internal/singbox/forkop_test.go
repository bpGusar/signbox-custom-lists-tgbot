package singbox

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lst-signbox-lists-tgbot/internal/podkop"
)

// fakeForkop stands in for forkop's generator: it reads the fixture it was
// given and writes the config and section cache the way the generator does.
type fakeForkop struct {
	args    []string
	fixture map[string]any
	drop    int
	fail    bool
}

func (f *fakeForkop) install(t *testing.T) {
	t.Helper()
	prevRun, prevStat, prevDetect := runner, fileExists, detectBackend
	t.Cleanup(func() { runner, fileExists, detectBackend = prevRun, prevStat, prevDetect })

	detectBackend = func() (podkop.Backend, error) { return podkop.Forkop, nil }
	fileExists = func(path string) bool { return path == forkopGenerator }
	runner = func(_ context.Context, name string, args []string, _ string) (string, error) {
		if name == "sing-box" {
			return "sing-box version 1.12.10-extended-1.4.1\n", nil
		}
		if name != "env" {
			return "", errors.New("unexpected command " + name)
		}
		f.args = args
		if f.fail {
			return "manual proxy link scheme is not supported by sing-box config generation yet\n", errors.New("exit status 2")
		}
		n := len(args)
		fixturePath, configPath := args[n-5], args[n-4]
		data, err := os.ReadFile(fixturePath)
		if err != nil {
			t.Fatalf("read fixture: %v", err)
		}
		if err := json.Unmarshal(data, &f.fixture); err != nil {
			t.Fatalf("fixture is not JSON: %v", err)
		}
		section := f.fixture["section"].([]any)[0].(map[string]any)
		links := section["selector_proxy_links"].([]any)

		outbounds := []map[string]any{{"type": "direct", "tag": "direct-out"}}
		cacheLinks := map[string]string{}
		for i := len(links) - 1; i >= f.drop; i-- {
			tag := forkopProbeSection + "-" + string(rune('1'+i)) + "-out"
			outbounds = append(outbounds, map[string]any{"type": "vless", "tag": tag})
			cacheLinks[tag] = links[i].(string)
		}
		writeJSON(t, configPath, map[string]any{"outbounds": outbounds})
		writeJSON(t, filepath.Join(configPath+".section-cache", forkopProbeSection+".json"), map[string]any{"links": cacheLinks})
		return "", nil
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(v)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAvailableForkop(t *testing.T) {
	f := &fakeForkop{}
	f.install(t)
	if ok, reason := Available(context.Background()); !ok {
		t.Fatalf("Available = false (%s)", reason)
	}

	fileExists = func(string) bool { return false }
	ok, reason := Available(context.Background())
	if ok || !strings.Contains(reason, forkopGenerator) {
		t.Fatalf("missing generator must be reported, got %t %q", ok, reason)
	}
}

func TestBuildForkopOutboundsMapsTagsByLink(t *testing.T) {
	f := &fakeForkop{}
	f.install(t)

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
	if len(outbounds) != 2 || tags[0] != "lstprobe-1-out" || tags[1] != "lstprobe-2-out" {
		t.Fatalf("tags: %v", tags)
	}
	if !strings.Contains(string(outbounds[1]), `"lstprobe-2-out"`) {
		t.Fatalf("outbound does not match its tag: %s", outbounds[1])
	}

	if got := f.args[len(f.args)-1]; got != "1" {
		t.Fatalf("an extended sing-box must enable XHTTP, got %q", got)
	}
	for _, a := range f.args[:2] {
		if !strings.HasPrefix(a, "TMP_SUBSCRIPTION_FOLDER=") && !strings.HasPrefix(a, "FORKOP_RUNTIME_STATE_DIR=") {
			t.Fatalf("forkop's live state must be kept out of reach: %v", f.args)
		}
	}
	section := f.fixture["section"].([]any)[0].(map[string]any)
	if section["action"] != "connection" {
		t.Fatalf("fixture section: %v", section)
	}
	if _, err := os.Stat(filepath.Dir(f.args[len(f.args)-5])); !os.IsNotExist(err) {
		t.Fatal("the temp dir with live keys must be removed")
	}
}

func TestBuildForkopOutboundsErrors(t *testing.T) {
	links := parseLinks(t, "vless://uuid@1.2.3.4:8443#⚡ a", "vless://uuid@5.6.7.8:8443#⚡ b")

	f := &fakeForkop{fail: true}
	f.install(t)
	_, _, err := buildOutbounds(context.Background(), toolchainFor(podkop.Forkop), links)
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("the generator's reason must reach the user, got %v", err)
	}

	f2 := &fakeForkop{drop: 1}
	f2.install(t)
	if _, _, err := buildOutbounds(context.Background(), toolchainFor(podkop.Forkop), links); err == nil {
		t.Fatal("a link without an outbound must be an error")
	}
}
