package version

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

type Status int

const (
	StatusUnknown Status = iota
	StatusCurrent
	StatusOutdated
)

const defaultCacheTTL = 7 * 24 * time.Hour

type Checker struct {
	mu        sync.Mutex
	latest    string
	fetchedAt time.Time
	lastErr   error
	ttl       time.Duration
	client    *http.Client
}

func NewChecker() *Checker {
	return &Checker{
		ttl: defaultCacheTTL,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *Checker) Refresh(ctx context.Context) {
	c.refresh(ctx)
}

func (c *Checker) Check(ctx context.Context) Status {
	c.mu.Lock()
	stale := c.fetchedAt.IsZero() || time.Since(c.fetchedAt) > c.ttl
	c.mu.Unlock()

	if stale {
		c.refresh(ctx)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusLocked()
}

// CheckFresh always fetches the latest release version from GitHub.
func (c *Checker) CheckFresh(ctx context.Context) Status {
	c.refresh(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusLocked()
}

func (c *Checker) statusLocked() Status {
	if c.latest == "" {
		return StatusUnknown
	}
	if Compare(Display(), c.latest) >= 0 {
		return StatusCurrent
	}
	return StatusOutdated
}

func (c *Checker) Latest() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.latest
}

func (c *Checker) LastError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr
}

func (c *Checker) refresh(ctx context.Context) {
	latest, err := fetchLatest(ctx, c.client)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fetchedAt = time.Now()
	c.lastErr = err
	if err == nil {
		c.latest = latest
	}
}

type ghRelease struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
}

func fetchLatest(ctx context.Context, client *http.Client) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, repoLatestURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "lst-signbox-lists-tgbot")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github api: %s", resp.Status)
	}

	var rel ghRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return "", err
	}

	latest := Normalize(rel.TagName)
	if latest == "" {
		latest = Normalize(rel.Name)
	}
	if latest == "" {
		return "", fmt.Errorf("empty release version")
	}
	return latest, nil
}
