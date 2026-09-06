package singbox

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"lst-signbox-lists-tgbot/internal/proxylink"
)

type fakeShell struct {
	version   string
	versionEr error
	build     string
	buildErr  error
	stdin     string
}

func (f *fakeShell) install(t *testing.T, facade bool) {
	t.Helper()
	prevRun, prevStat := runner, fileExists
	t.Cleanup(func() { runner, fileExists = prevRun, prevStat })

	fileExists = func(string) bool { return facade }
	runner = func(_ context.Context, name string, args []string, stdin string) (string, error) {
		switch {
		case name == "sing-box":
			return f.version, f.versionEr
		case name == "sh":
			f.stdin = stdin
			if len(args) != 2 || args[0] != "-c" || args[1] != buildScript {
				t.Errorf("unexpected shell invocation: %v", args)
			}
			return f.build, f.buildErr
		}
		return "", errors.New("unexpected command " + name)
	}
}

func TestAvailable(t *testing.T) {
	f := &fakeShell{version: "sing-box version 1.12.4\n"}
	f.install(t, true)

	ok, reason := Available(context.Background())
	if !ok {
		t.Fatalf("Available = false (%s)", reason)
	}
	if !strings.Contains(reason, "1.12.4") {
		t.Fatalf("reason should name the version: %q", reason)
	}
}

func TestAvailableRejects(t *testing.T) {
	cases := []struct {
		name   string
		facade bool
		shell  fakeShell
	}{
		{"конвертер отсутствует", false, fakeShell{version: "sing-box version 1.12.4"}},
		{"sing-box не запускается", true, fakeShell{versionEr: errors.New("not found")}},
		{"версия не читается", true, fakeShell{version: "какой-то мусор"}},
		{"версия старая", true, fakeShell{version: "sing-box version 1.11.9"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sh := c.shell
			sh.install(t, c.facade)
			if ok, _ := Available(context.Background()); ok {
				t.Fatal("Available must be false")
			}
		})
	}
}

func TestBuildOutboundsTakesTagsFromJSON(t *testing.T) {
	f := &fakeShell{build: `{"outbounds":[
		{"type":"vless","tag":"probe1-out"},
		{"type":"hysteria2","tag":"probe2-out"}
	]}`}
	f.install(t, true)

	links := parseLinks(t,
		"vless://uuid@1.2.3.4:8443#⚡ Первый узел",
		"hysteria2://pass@5.6.7.8:30443#⚡ Второй",
	)
	outbounds, tags, err := buildOutbounds(context.Background(), links)
	if err != nil {
		t.Fatalf("buildOutbounds: %v", err)
	}
	if len(outbounds) != 2 {
		t.Fatalf("got %d outbounds", len(outbounds))
	}
	if tags[0] != "probe1-out" || tags[1] != "probe2-out" {
		t.Fatalf("tags: %v", tags)
	}

	// The links go in on stdin, one per line, already safe for a shell loop.
	lines := strings.Split(strings.TrimSpace(f.stdin), "\n")
	if len(lines) != 2 {
		t.Fatalf("stdin lines: %q", f.stdin)
	}
	for _, line := range lines {
		if strings.ContainsAny(line, " \t") {
			t.Fatalf("a link reached the shell with whitespace in it: %q", line)
		}
	}
}

func TestBuildOutboundsRejectsBadOutput(t *testing.T) {
	links := parseLinks(t, "vless://uuid@1.2.3.4:8443#⚡")

	cases := map[string]fakeShell{
		"не JSON":           {build: "fatal: unsupported protocol"},
		"конвертер упал":    {build: "", buildErr: errors.New("exit 1")},
		"не столько ссылок": {build: `{"outbounds":[]}`},
		"outbound без тега": {build: `{"outbounds":[{"type":"vless"}]}`},
	}
	for name, sh := range cases {
		t.Run(name, func(t *testing.T) {
			f := sh
			f.install(t, true)
			if _, _, err := buildOutbounds(context.Background(), links); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestMeasureRefusesOversizedBatch(t *testing.T) {
	f := &fakeShell{version: "sing-box version 1.12.4"}
	f.install(t, true)

	links := make([]proxylink.Link, MaxBatch+1)
	if _, err := Measure(context.Background(), links, Options{}, nil); err == nil {
		t.Fatal("a batch over the cap must be refused before anything starts")
	}
}

func TestMeasureFallsBackWhenUnavailable(t *testing.T) {
	f := &fakeShell{version: "sing-box version 1.12.4"}
	f.install(t, false)

	links := parseLinks(t, "vless://uuid@1.2.3.4:8443#⚡")
	if _, err := Measure(context.Background(), links, Options{}, nil); err == nil {
		t.Fatal("expected an error when podkop's converter is missing")
	}
}

func TestWriteConfigIsPrivateAndComplete(t *testing.T) {
	path, err := writeConfig([]json.RawMessage{json.RawMessage(`{"type":"vless","tag":"probe1-out"}`)}, 19090)
	if err != nil {
		t.Fatalf("writeConfig: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg struct {
		Experimental struct {
			ClashAPI struct {
				ExternalController string `json:"external_controller"`
			} `json:"clash_api"`
		} `json:"experimental"`
		Outbounds []struct {
			Tag  string `json:"tag"`
			Type string `json:"type"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("config is not JSON: %v", err)
	}
	if cfg.Experimental.ClashAPI.ExternalController != "127.0.0.1:19090" {
		t.Fatalf("clash api: %q", cfg.Experimental.ClashAPI.ExternalController)
	}
	if len(cfg.Outbounds) != 2 || cfg.Outbounds[0].Tag != "probe1-out" || cfg.Outbounds[1].Type != "direct" {
		t.Fatalf("outbounds: %+v", cfg.Outbounds)
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.12.4", "1.12.4", 0},
		{"1.12.5", "1.12.4", 1},
		{"1.11.9", "1.12.4", -1},
		{"2.0.0", "1.12.4", 1},
		{"sing-box version 1.12.10", "1.12.4", 1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func parseLinks(t *testing.T, raw ...string) []proxylink.Link {
	t.Helper()
	out := make([]proxylink.Link, 0, len(raw))
	for _, r := range raw {
		l, err := proxylink.Parse(r)
		if err != nil {
			t.Fatalf("Parse(%q): %v", r, err)
		}
		out = append(out, l)
	}
	return out
}
