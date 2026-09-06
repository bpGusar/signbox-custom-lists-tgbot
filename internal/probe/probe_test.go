package probe

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

// fakeDialer stands in for the network: each endpoint gets a fixed latency, or
// an error when it is missing from the map.
func fakeDialer(t *testing.T, latency map[string]time.Duration) *int {
	t.Helper()
	prev := dialer
	t.Cleanup(func() { dialer = prev })

	var mu sync.Mutex
	calls := 0
	dialer = func(ctx context.Context, address string, _ time.Duration) (net.Conn, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		d, ok := latency[address]
		if !ok {
			return nil, errors.New("connection refused")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(d):
		}
		c1, c2 := net.Pipe()
		_ = c2.Close()
		return c1, nil
	}
	return &calls
}

func TestRunTCP(t *testing.T) {
	fakeDialer(t, map[string]time.Duration{
		"1.1.1.1:443": 5 * time.Millisecond,
	})

	targets := []Target{
		{Host: "1.1.1.1", Port: 443},
		{Host: "2.2.2.2", Port: 443},
	}
	var progress int
	res := Run(context.Background(), targets, Options{
		Attempts: 2, Timeout: time.Second, Concurrency: 2, MaxPing: 400 * time.Millisecond,
	}, func(done, total int) { progress = done })

	if len(res) != 2 {
		t.Fatalf("expected a result per target, got %v", res)
	}
	good := res["1.1.1.1:443"]
	if !good.OK || good.Method != MethodTCP {
		t.Fatalf("good target: %+v", good)
	}
	if good.Latency < 5*time.Millisecond {
		t.Fatalf("latency below the simulated one: %v", good.Latency)
	}
	if bad := res["2.2.2.2:443"]; bad.OK || bad.Err == nil {
		t.Fatalf("unreachable target reported as OK: %+v", bad)
	}
	if progress != 2 {
		t.Fatalf("progress reported %d of 2", progress)
	}
}

// A target that is clearly under the threshold does not deserve a third dial:
// this runs on the router the user is browsing through.
func TestRunStopsEarlyOnFastTarget(t *testing.T) {
	calls := fakeDialer(t, map[string]time.Duration{"1.1.1.1:443": time.Millisecond})

	Run(context.Background(), []Target{{Host: "1.1.1.1", Port: 443}}, Options{
		Attempts: 3, Timeout: time.Second, Concurrency: 1, MaxPing: 400 * time.Millisecond,
	}, nil)

	if *calls != 2 {
		t.Fatalf("expected 2 attempts on a fast target, got %d", *calls)
	}
}

func TestRunCancelled(t *testing.T) {
	fakeDialer(t, map[string]time.Duration{"1.1.1.1:443": 50 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res := Run(ctx, []Target{{Host: "1.1.1.1", Port: 443}}, Options{
		Attempts: 3, Timeout: time.Second, Concurrency: 1,
	}, nil)
	if r, ok := res["1.1.1.1:443"]; ok && r.OK {
		t.Fatalf("a cancelled run must not report success: %+v", r)
	}
}

func TestPingICMP(t *testing.T) {
	prev := pingCmd
	t.Cleanup(func() { pingCmd = prev })
	pingCmd = func(_ context.Context, host string, _ int, _ int) (string, error) {
		return "--- " + host + " ping statistics ---\n" +
			"3 packets transmitted, 3 packets received, 0% packet loss\n" +
			"round-trip min/avg/max = 41.2/48.9/55.1 ms\n", nil
	}

	res := Run(context.Background(), []Target{{Host: "9.9.9.9", Port: 30443, UDP: true}},
		Options{Attempts: 3, Timeout: time.Second, Concurrency: 1}, nil)

	got := res["9.9.9.9:30443"]
	if !got.OK || got.Method != MethodICMP {
		t.Fatalf("icmp result: %+v", got)
	}
	if want := 41200 * time.Microsecond; got.Latency != want {
		t.Fatalf("latency = %v, want %v", got.Latency, want)
	}
}

func TestPingICMPNoStats(t *testing.T) {
	prev := pingCmd
	t.Cleanup(func() { pingCmd = prev })
	pingCmd = func(context.Context, string, int, int) (string, error) {
		return "ping: bad address", errors.New("exit 1")
	}

	res := Run(context.Background(), []Target{{Host: "nope", Port: 1, UDP: true}},
		Options{Attempts: 1, Timeout: time.Second, Concurrency: 1}, nil)
	if got := res["nope:1"]; got.OK || got.Err == nil {
		t.Fatalf("expected a failure, got %+v", got)
	}
}

func TestParseMaxPing(t *testing.T) {
	cases := map[string]time.Duration{
		"400":      400 * time.Millisecond,
		"400ms":    400 * time.Millisecond,
		"400 мс":   400 * time.Millisecond,
		"0.4s":     400 * time.Millisecond,
		"0,4 с":    400 * time.Millisecond,
		"1500":     1500 * time.Millisecond,
		" 250 ":    250 * time.Millisecond,
		"1s":       time.Second,
		"2сек":     2 * time.Second,
		"120 мсек": 120 * time.Millisecond,
	}
	for in, want := range cases {
		got, err := ParseMaxPing(in)
		if err != nil {
			t.Errorf("ParseMaxPing(%q) = %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseMaxPing(%q) = %v, want %v", in, got, want)
		}
	}

	bad := []string{"", "быстро", "0", "-100", "999999", "20s"}
	for _, in := range bad {
		if _, err := ParseMaxPing(in); err == nil {
			t.Errorf("ParseMaxPing(%q) = nil error, want one", in)
		}
	}
}

func TestDefaultOptionsTimeoutFollowsThreshold(t *testing.T) {
	if got := DefaultOptions(200 * time.Millisecond).Timeout; got != 1500*time.Millisecond {
		t.Fatalf("timeout floor: %v", got)
	}
	if got := DefaultOptions(2 * time.Second).Timeout; got != 4*time.Second {
		t.Fatalf("timeout for a high threshold: %v", got)
	}
}
