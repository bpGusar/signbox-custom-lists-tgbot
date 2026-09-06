// Package probe measures how far away a proxy server is without going through
// it: a TCP handshake to host:port, or an ICMP ping for the protocols that have
// no TCP to time.
package probe

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Method names what a number was measured with, because the two are not
// comparable and a report must say which one it shows.
type Method string

const (
	MethodTCP  Method = "tcp"
	MethodICMP Method = "icmp"
)

// Label is how a method reads in a chat message.
func (m Method) Label() string {
	switch m {
	case MethodICMP:
		return "ICMP ping до сервера"
	case MethodTCP:
		return "TCP-хендшейк до сервера"
	default:
		return string(m)
	}
}

// Target is one host:port to measure, however many links share it.
type Target struct {
	Host string
	Port int
	// UDP marks a target whose protocol has no TCP handshake, so it is
	// measured with ICMP instead.
	UDP bool
}

func (t Target) Endpoint() string {
	return t.Host + ":" + strconv.Itoa(t.Port)
}

type Result struct {
	Latency time.Duration
	OK      bool
	Method  Method
	Err     error
}

type Options struct {
	// Attempts is how many times one target is measured; the best result
	// wins, because a single sample on a busy router is noise.
	Attempts int
	// Timeout caps one attempt.
	Timeout time.Duration
	// Concurrency is how many targets are measured at once. It is small on
	// purpose: this runs on the router the user is browsing through.
	Concurrency int
	// Gap is the pause between two attempts on the same target.
	Gap time.Duration
	// MaxPing is the threshold the results will be compared against. It is
	// only used to stop early on a target that is clearly fast enough.
	MaxPing time.Duration
}

// DefaultOptions are the ones the bot uses for a given threshold.
func DefaultOptions(maxPing time.Duration) Options {
	timeout := 1500 * time.Millisecond
	if d := maxPing * 2; d > timeout {
		timeout = d
	}
	return Options{
		Attempts:    3,
		Timeout:     timeout,
		Concurrency: 4,
		Gap:         250 * time.Millisecond,
		MaxPing:     maxPing,
	}
}

func (o Options) withDefaults() Options {
	if o.Attempts <= 0 {
		o.Attempts = 3
	}
	if o.Timeout <= 0 {
		o.Timeout = 1500 * time.Millisecond
	}
	if o.Concurrency <= 0 {
		o.Concurrency = 4
	}
	return o
}

// Run measures every target and returns the results keyed by endpoint. It never
// fails as a whole: an unreachable target is a Result with OK false.
func Run(ctx context.Context, targets []Target, o Options, onProgress func(done, total int)) map[string]Result {
	o = o.withDefaults()

	results := make(map[string]Result, len(targets))
	if len(targets) == 0 {
		return results
	}

	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		done int
	)
	queue := make(chan Target)

	workers := o.Concurrency
	if workers > len(targets) {
		workers = len(targets)
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for tgt := range queue {
				res := measure(ctx, tgt, o)
				mu.Lock()
				results[tgt.Endpoint()] = res
				done++
				n := done
				mu.Unlock()
				if onProgress != nil {
					onProgress(n, len(targets))
				}
			}
		}()
	}

	for _, tgt := range targets {
		select {
		case <-ctx.Done():
			close(queue)
			wg.Wait()
			return results
		case queue <- tgt:
		}
	}
	close(queue)
	wg.Wait()
	return results
}

func measure(ctx context.Context, tgt Target, o Options) Result {
	if tgt.UDP {
		return pingICMP(ctx, tgt, o)
	}
	return dialTCP(ctx, tgt, o)
}

// dialer is a variable so a test can point the probes somewhere harmless.
var dialer = func(ctx context.Context, address string, timeout time.Duration) (net.Conn, error) {
	d := net.Dialer{Timeout: timeout}
	return d.DialContext(ctx, "tcp", address)
}

func dialTCP(ctx context.Context, tgt Target, o Options) Result {
	res := Result{Method: MethodTCP}
	address := tgt.Endpoint()

	for i := 0; i < o.Attempts; i++ {
		if ctx.Err() != nil {
			break
		}
		if i > 0 && o.Gap > 0 {
			if !sleep(ctx, o.Gap) {
				break
			}
		}

		start := time.Now()
		conn, err := dialer(ctx, address, o.Timeout)
		elapsed := time.Since(start)
		if err != nil {
			res.Err = err
			continue
		}
		_ = conn.Close()

		if !res.OK || elapsed < res.Latency {
			res.Latency = elapsed
		}
		res.OK = true
		res.Err = nil

		// Two fast samples in a row say enough: the point of a third is to
		// rescue a borderline node, not to re-time a clearly good one.
		if i >= 1 && o.MaxPing > 0 && res.Latency*5 < o.MaxPing*4 {
			break
		}
	}
	if !res.OK && res.Err == nil {
		res.Err = ctx.Err()
	}
	return res
}

// pingCmd is a variable so a test can stand in for busybox ping.
var pingCmd = func(ctx context.Context, host string, count int, timeoutSec int) (string, error) {
	out, err := exec.CommandContext(ctx, "ping",
		"-c", strconv.Itoa(count),
		"-W", strconv.Itoa(timeoutSec),
		"-q", host).CombinedOutput()
	return string(out), err
}

// pingStats matches the summary line busybox and iputils both print.
var pingStats = regexp.MustCompile(`=\s*([0-9.]+)/([0-9.]+)/([0-9.]+)`)

func pingICMP(ctx context.Context, tgt Target, o Options) Result {
	res := Result{Method: MethodICMP}

	timeoutSec := int(o.Timeout / time.Second)
	if timeoutSec < 1 {
		timeoutSec = 1
	}
	out, err := pingCmd(ctx, tgt.Host, o.Attempts, timeoutSec)
	m := pingStats.FindStringSubmatch(out)
	if m == nil {
		res.Err = err
		if res.Err == nil {
			res.Err = fmt.Errorf("ping не вернул статистику")
		}
		return res
	}
	ms, convErr := strconv.ParseFloat(m[1], 64)
	if convErr != nil {
		res.Err = convErr
		return res
	}
	res.Latency = time.Duration(ms * float64(time.Millisecond))
	res.OK = true
	return res
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// FormatLatency renders a duration the way the reports do.
func FormatLatency(d time.Duration) string {
	return strconv.Itoa(int(d.Round(time.Millisecond)/time.Millisecond)) + " мс"
}

// ParseMaxPing reads a threshold typed by hand: "400", "400ms", "400 мс",
// "0.4s" all mean the same thing.
func ParseMaxPing(text string) (time.Duration, error) {
	s := strings.ToLower(strings.TrimSpace(text))
	s = strings.ReplaceAll(s, ",", ".")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.NewReplacer("мсек", "ms", "сек", "s", "мс", "ms", "с", "s").Replace(s)
	if s == "" {
		return 0, fmt.Errorf("пустое значение")
	}

	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return checkMaxPing(time.Duration(v * float64(time.Millisecond)))
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("не похоже на порог: пришлите число в миллисекундах, например 400")
	}
	return checkMaxPing(d)
}

const (
	// MinMaxPing and MaxMaxPing bound what a threshold may be: below the
	// first nothing would ever pass, above the second nothing would be
	// filtered.
	MinMaxPing = 10 * time.Millisecond
	MaxMaxPing = 10 * time.Second
)

func checkMaxPing(d time.Duration) (time.Duration, error) {
	if d < MinMaxPing || d > MaxMaxPing {
		return 0, fmt.Errorf("порог должен быть от %s до %s", FormatLatency(MinMaxPing), FormatLatency(MaxMaxPing))
	}
	return d, nil
}
