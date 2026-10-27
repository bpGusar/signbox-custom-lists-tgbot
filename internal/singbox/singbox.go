// Package singbox measures proxy links the way podkop or forkop will actually
// use them: it asks the backend's own converter to turn the links into
// outbounds, runs a throwaway sing-box over them and times a request through
// each one.
//
// Everything here leans on the backends' internals, which are not a public
// API, so every entry point checks first and the caller is expected to fall
// back to a plain TCP probe when Available says no.
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

	"lst-signbox-lists-tgbot/internal/podkop"
	"lst-signbox-lists-tgbot/internal/proxylink"
)

const (
	// facadePath is podkop's link-to-outbound converter.
	facadePath = "/usr/lib/podkop/sing_box_config_facade.sh"
	// forkopLib holds forkop's generator, which builds a whole sing-box
	// config from a JSON fixture instead of the live UCI config.
	forkopLib       = "/usr/lib/forkop"
	forkopGenerator = forkopLib + "/singbox/generator.uc"
	// forkopProbeSection is the section the links are put into for forkop.
	forkopProbeSection = "lstprobe"
	// minFreeKB keeps a second sing-box off a router that has no room for
	// it. The backend's own instance is already running.
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

// detectBackend is a variable so tests can pick the backend.
var detectBackend = podkop.Detect

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// toolchain is what one backend needs to turn links into outbounds.
type toolchain struct {
	backend   podkop.Backend
	converter string
	// minVersion is the sing-box the backend itself requires.
	minVersion string
	// extended is set for a sing-box-extended build, which forkop needs to
	// know to accept XHTTP links.
	extended bool
}

func toolchainFor(b podkop.Backend) toolchain {
	if b.IsForkop() {
		return toolchain{backend: b, converter: forkopGenerator, minVersion: "1.12.0"}
	}
	return toolchain{backend: b, converter: facadePath, minVersion: "1.12.4"}
}

// check finds the backend's converter and a sing-box fit to run its output.
func check(ctx context.Context) (toolchain, string, error) {
	b, err := detectBackend()
	if err != nil {
		return toolchain{}, "", err
	}
	tc := toolchainFor(b)
	if !fileExists(tc.converter) {
		return toolchain{}, "", fmt.Errorf("конвертер %s не найден (%s)", b.Name, tc.converter)
	}
	out, err := runner(ctx, "sing-box", []string{"version"}, "")
	if err != nil {
		return toolchain{}, "", fmt.Errorf("sing-box не запускается: %w", err)
	}
	v := versionRe.FindString(out)
	if v == "" {
		return toolchain{}, "", fmt.Errorf("не удалось прочитать версию sing-box")
	}
	if compareVersions(v, tc.minVersion) < 0 {
		return toolchain{}, "", fmt.Errorf("нужен sing-box ≥ %s, установлен %s", tc.minVersion, v)
	}
	if free, err := freeMemoryKB(); err == nil && free < minFreeKB {
		return toolchain{}, "", fmt.Errorf("мало свободной памяти (%d МБ)", free/1024)
	}
	tc.extended = strings.Contains(out, "extended")
	return tc, v, nil
}

// Available reports whether this router can measure through a tunnel at all,
// with a reason to show when it cannot.
func Available(ctx context.Context) (bool, string) {
	_, v, err := check(ctx)
	if err != nil {
		return false, err.Error()
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
	tc, _, err := check(ctx)
	if err != nil {
		return nil, err
	}

	outbounds, tags, err := buildOutbounds(ctx, tc, links)
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

// buildOutbounds hands the links to the backend's own converter, so what is
// measured is what the backend would generate. The tags come back from the
// produced JSON rather than being guessed from the naming scheme.
func buildOutbounds(ctx context.Context, tc toolchain, links []proxylink.Link) ([]json.RawMessage, []string, error) {
	if tc.backend.IsForkop() {
		return buildForkopOutbounds(ctx, links, tc.extended)
	}
	return buildPodkopOutbounds(ctx, links)
}

func buildPodkopOutbounds(ctx context.Context, links []proxylink.Link) ([]json.RawMessage, []string, error) {
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

// buildForkopOutbounds runs forkop's generator over a fixture holding one
// connection section with the links, the way forkop's own tests drive it, and
// keeps the outbounds made from the links. Which tag belongs to which link is
// read from the section cache the generator writes next to the config.
func buildForkopOutbounds(ctx context.Context, links []proxylink.Link, extended bool) ([]json.RawMessage, []string, error) {
	dir, err := os.MkdirTemp("", "lst-forkop-probe-*")
	if err != nil {
		return nil, nil, fmt.Errorf("не удалось создать временный каталог: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	raw := make([]string, len(links))
	for i, l := range links {
		raw[i] = l.ForUCI()
	}
	fixture := map[string]any{
		"settings": map[string]any{".name": "settings", ".type": "settings", "log_level": "error"},
		"section": []map[string]any{{
			".name":                forkopProbeSection,
			".type":                "section",
			"enabled":              "1",
			"action":               "connection",
			"selector_proxy_links": raw,
			"domain_suffix":        []string{"lst-probe.invalid"},
		}},
	}
	data, err := json.Marshal(fixture)
	if err != nil {
		return nil, nil, err
	}
	fixturePath := dir + "/fixture.json"
	if err := os.WriteFile(fixturePath, data, 0o600); err != nil {
		return nil, nil, fmt.Errorf("не удалось записать fixture: %w", err)
	}

	xhttp := "0"
	if extended {
		xhttp = "1"
	}
	configPath := dir + "/config.json"
	out, err := runner(ctx, "env", []string{
		"TMP_SUBSCRIPTION_FOLDER=" + dir + "/subscriptions",
		"FORKOP_RUNTIME_STATE_DIR=" + dir + "/state",
		"ucode", "-L", forkopLib, forkopGenerator, "generate-config-fixture",
		fixturePath, configPath, "127.0.0.1", "0", xhttp,
	}, "")
	if err != nil {
		if line := firstLine(out); line != "" {
			return nil, nil, fmt.Errorf("конвертер forkop не отработал: %w: %s", err, line)
		}
		return nil, nil, fmt.Errorf("конвертер forkop не отработал: %w", err)
	}

	var cfg struct {
		Outbounds []json.RawMessage `json:"outbounds"`
	}
	if err := readJSON(configPath, &cfg); err != nil {
		return nil, nil, fmt.Errorf("конвертер forkop вернул не JSON: %w", err)
	}
	var cache struct {
		Links map[string]string `json:"links"`
	}
	if err := readJSON(configPath+".section-cache/"+forkopProbeSection+".json", &cache); err != nil {
		return nil, nil, fmt.Errorf("кеш секции forkop не прочитан: %w", err)
	}

	byTag := make(map[string]json.RawMessage, len(cfg.Outbounds))
	for _, ob := range cfg.Outbounds {
		var head struct {
			Tag string `json:"tag"`
		}
		if json.Unmarshal(ob, &head) == nil && head.Tag != "" {
			byTag[head.Tag] = ob
		}
	}
	tagByLink := make(map[string]string, len(cache.Links))
	for tag, link := range cache.Links {
		tagByLink[link] = tag
	}

	outbounds := make([]json.RawMessage, len(links))
	tags := make([]string, len(links))
	for i, link := range raw {
		tag, ok := tagByLink[link]
		if !ok {
			return nil, nil, fmt.Errorf("конвертер forkop не вернул outbound для ссылки %d", i+1)
		}
		ob, ok := byTag[tag]
		if !ok {
			return nil, nil, fmt.Errorf("в конфиге forkop нет outbound %q", tag)
		}
		outbounds[i], tags[i] = ob, tag
	}
	return outbounds, tags, nil
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
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
