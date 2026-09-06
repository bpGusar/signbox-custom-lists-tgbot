// Package singbox measures proxy links the way podkop will actually use them:
// it asks podkop's own converter to turn the links into outbounds, runs a
// throwaway sing-box over them and times a request through each one.
//
// Everything here leans on podkop's internals, which are not a public API, so
// every entry point checks first and the caller is expected to fall back to a
// plain TCP probe when Available says no.
package singbox

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"lst-signbox-lists-tgbot/internal/proxylink"
)

const (
	// facadePath is podkop's link-to-outbound converter.
	facadePath = "/usr/lib/podkop/sing_box_config_facade.sh"
	// minVersion is what podkop itself requires.
	minVersion = "1.12.4"
	// minFreeKB keeps a second sing-box off a router that has no room for
	// it. podkop's own instance is already running.
	minFreeKB = 24 * 1024
	// MaxBatch caps how many outbounds one throwaway instance carries.
	MaxBatch = 30
	// delayURL is the same target podkop's urltest uses.
	delayURL = "https://www.gstatic.com/generate_204"
)

// Result is one link's latency through the tunnel.
type Result struct {
	Latency time.Duration
	OK      bool
	Err     error
}

type Options struct {
	// Timeout caps one delay request.
	Timeout time.Duration
	// Startup is how long sing-box gets to come up.
	Startup time.Duration
	// Gap is the pause between two delay requests, which are made one at a
	// time on purpose: the group handle would fire them all at once.
	Gap time.Duration
}

func (o Options) withDefaults() Options {
	if o.Timeout <= 0 {
		o.Timeout = 3 * time.Second
	}
	if o.Startup <= 0 {
		o.Startup = 15 * time.Second
	}
	if o.Gap < 0 {
		o.Gap = 0
	}
	return o
}

// DefaultOptions are the ones the bot uses for a given threshold.
func DefaultOptions(maxPing time.Duration) Options {
	timeout := 3 * time.Second
	if d := maxPing * 3; d > timeout {
		timeout = d
	}
	return Options{Timeout: timeout, Startup: 15 * time.Second, Gap: 100 * time.Millisecond}
}

// runner executes a command and returns its combined output. It is a variable
// so tests can answer for a router.
var runner = func(ctx context.Context, name string, args []string, stdin string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// fileExists is a variable for the same reason.
var fileExists = func(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// Available reports whether this router can measure through a tunnel at all,
// with a reason to show when it cannot.
func Available(ctx context.Context) (bool, string) {
	if !fileExists(facadePath) {
		return false, "конвертер podkop не найден (" + facadePath + ")"
	}
	out, err := runner(ctx, "sing-box", []string{"version"}, "")
	if err != nil {
		return false, "sing-box не запускается: " + err.Error()
	}
	v := versionRe.FindString(out)
	if v == "" {
		return false, "не удалось прочитать версию sing-box"
	}
	if compareVersions(v, minVersion) < 0 {
		return false, fmt.Sprintf("нужен sing-box ≥ %s, установлен %s", minVersion, v)
	}
	if free, err := freeMemoryKB(); err == nil && free < minFreeKB {
		return false, fmt.Sprintf("мало свободной памяти (%d МБ)", free/1024)
	}
	return true, "sing-box " + v
}

// Measure times every link through a throwaway sing-box, keyed by DedupKey.
func Measure(ctx context.Context, links []proxylink.Link, o Options, onProgress func(done, total int)) (map[string]Result, error) {
	o = o.withDefaults()

	if len(links) == 0 {
		return map[string]Result{}, nil
	}
	if len(links) > MaxBatch {
		return nil, fmt.Errorf("за раз можно проверить не больше %d ссылок", MaxBatch)
	}
	if ok, reason := Available(ctx); !ok {
		return nil, fmt.Errorf("%s", reason)
	}

	outbounds, tags, err := buildOutbounds(ctx, links)
	if err != nil {
		return nil, err
	}

	port, err := freePort()
	if err != nil {
		return nil, fmt.Errorf("не удалось занять порт для Clash API: %w", err)
	}

	// The config carries live keys, so it is written for this process only
	// and removed whichever way this function ends.
	path, err := writeConfig(outbounds, port)
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(path) }()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "sing-box", "run", "-c", path)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("не удалось запустить sing-box: %w", err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}()

	base := "http://127.0.0.1:" + strconv.Itoa(port)
	if err := waitReady(runCtx, base, o.Startup); err != nil {
		return nil, err
	}

	results := make(map[string]Result, len(links))
	for i, l := range links {
		if runCtx.Err() != nil {
			break
		}
		if i > 0 && o.Gap > 0 {
			select {
			case <-runCtx.Done():
			case <-time.After(o.Gap):
			}
		}
		results[l.DedupKey()] = queryDelay(runCtx, base, tags[i], o.Timeout)
		if onProgress != nil {
			onProgress(i+1, len(links))
		}
	}
	return results, nil
}

// buildScript is the shell podkop's converter is called from. It is a constant:
// the links it works on arrive on stdin, never as part of the script.
const buildScript = `. ` + facadePath + `
log() { :; }
config='{"outbounds":[]}'
i=0
while IFS= read -r link; do
  [ -n "$link" ] || continue
  i=$((i+1))
  config=$(sing_box_cf_add_proxy_outbound "$config" "probe$i" "$link" "0") || exit 1
done
printf '%s' "$config"`

// buildOutbounds hands the links to podkop's own converter, so what is measured
// is what podkop would generate. The tags come back from the produced JSON
// rather than being guessed from the naming scheme.
func buildOutbounds(ctx context.Context, links []proxylink.Link) ([]json.RawMessage, []string, error) {
	var stdin strings.Builder
	for _, l := range links {
		stdin.WriteString(l.ForUCI())
		stdin.WriteString("\n")
	}

	out, err := runner(ctx, "sh", []string{"-c", buildScript}, stdin.String())
	if err != nil {
		return nil, nil, fmt.Errorf("конвертер podkop не отработал: %w", err)
	}

	var cfg struct {
		Outbounds []json.RawMessage `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &cfg); err != nil {
		return nil, nil, fmt.Errorf("конвертер podkop вернул не JSON: %w", err)
	}
	if len(cfg.Outbounds) != len(links) {
		return nil, nil, fmt.Errorf("конвертер podkop вернул %d outbound'ов вместо %d", len(cfg.Outbounds), len(links))
	}

	tags := make([]string, len(cfg.Outbounds))
	for i, raw := range cfg.Outbounds {
		var ob struct {
			Tag string `json:"tag"`
		}
		if err := json.Unmarshal(raw, &ob); err != nil || ob.Tag == "" {
			return nil, nil, fmt.Errorf("outbound %d без тега", i+1)
		}
		tags[i] = ob.Tag
	}
	return cfg.Outbounds, tags, nil
}

func writeConfig(outbounds []json.RawMessage, port int) (string, error) {
	cfg := map[string]any{
		"log": map[string]any{"level": "error", "timestamp": false},
		"experimental": map[string]any{
			"clash_api": map[string]any{
				"external_controller": "127.0.0.1:" + strconv.Itoa(port),
			},
		},
		"outbounds": append(append([]json.RawMessage{}, outbounds...),
			json.RawMessage(`{"type":"direct","tag":"direct"}`)),
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}

	f, err := os.CreateTemp("", "lst-probe-*.json")
	if err != nil {
		return "", fmt.Errorf("не удалось создать временный конфиг: %w", err)
	}
	path := f.Name()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

var httpClient = &http.Client{}

func waitReady(ctx context.Context, base string, within time.Duration) error {
	deadline := time.Now().Add(within)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/version", nil)
		if err != nil {
			return err
		}
		resp, err := httpClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("sing-box не поднялся за %s", within)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func queryDelay(ctx context.Context, base, tag string, timeout time.Duration) Result {
	// One proxy at a time: the group handle fires every connection at once,
	// which is exactly what "не мешать интернету" rules out.
	endpoint := fmt.Sprintf("%s/proxies/%s/delay?url=%s&timeout=%d",
		base, url.PathEscape(tag), url.QueryEscape(delayURL), timeout.Milliseconds())

	reqCtx, cancel := context.WithTimeout(ctx, timeout+2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Result{Err: err}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return Result{Err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	var body struct {
		Delay   int    `json:"delay"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Result{Err: fmt.Errorf("ответ Clash API не разобран: %w", err)}
	}
	if resp.StatusCode != http.StatusOK || body.Delay <= 0 {
		msg := body.Message
		if msg == "" {
			msg = "прокси не ответил"
		}
		return Result{Err: fmt.Errorf("%s", msg)}
	}
	return Result{Latency: time.Duration(body.Delay) * time.Millisecond, OK: true}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// freeMemoryKB reads MemAvailable, which is what actually decides whether a
// second sing-box will start.
func freeMemoryKB() (int, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		name, value, ok := strings.Cut(sc.Text(), ":")
		if !ok || name != "MemAvailable" {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			continue
		}
		return strconv.Atoi(fields[0])
	}
	return 0, fmt.Errorf("MemAvailable не найден")
}

func compareVersions(a, b string) int {
	pa, pb := versionParts(a), versionParts(b)
	for i := 0; i < 3; i++ {
		switch {
		case pa[i] < pb[i]:
			return -1
		case pa[i] > pb[i]:
			return 1
		}
	}
	return 0
}

func versionParts(v string) [3]int {
	var out [3]int
	m := versionRe.FindStringSubmatch(v)
	if m == nil {
		return out
	}
	for i := 0; i < 3; i++ {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out
}
