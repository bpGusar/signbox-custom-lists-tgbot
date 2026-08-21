package service

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestClaimOrCheckOwner_firstCallerClaims(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "state.json"))

	owner, isOwner, err := m.ClaimOrCheckOwner(100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isOwner || owner != 100 {
		t.Fatalf("expected first caller to be claimed as owner, got owner=%d isOwner=%t", owner, isOwner)
	}
}

func TestClaimOrCheckOwner_rejectsOtherChats(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "state.json"))

	if _, _, err := m.ClaimOrCheckOwner(100); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	owner, isOwner, err := m.ClaimOrCheckOwner(200)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isOwner || owner != 100 {
		t.Fatalf("expected chat 200 to be rejected in favor of owner 100, got owner=%d isOwner=%t", owner, isOwner)
	}
}

func TestClaimOrCheckOwner_ownerCanKeepUsingBot(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "state.json"))

	if _, _, err := m.ClaimOrCheckOwner(100); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	owner, isOwner, err := m.ClaimOrCheckOwner(100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isOwner || owner != 100 {
		t.Fatalf("expected owner to still be recognized, got owner=%d isOwner=%t", owner, isOwner)
	}
}

func TestClaimOrCheckOwner_persistsAcrossManagers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	m1 := NewManager(path)
	if _, _, err := m1.ClaimOrCheckOwner(100); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m2 := NewManager(path)
	owner, isOwner, err := m2.ClaimOrCheckOwner(200)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isOwner || owner != 100 {
		t.Fatalf("expected ownership to persist across Manager instances, got owner=%d isOwner=%t", owner, isOwner)
	}
}

func TestNotifiedVersion_persistsAcrossManagers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	m1 := NewManager(path)
	if got := m1.NotifiedVersion(); got != "" {
		t.Fatalf("expected empty notified version on fresh state, got %q", got)
	}
	if err := m1.MarkVersionNotified("0.20260727.30"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m2 := NewManager(path)
	if got := m2.NotifiedVersion(); got != "0.20260727.30" {
		t.Fatalf("expected notified version to persist, got %q", got)
	}
}

func TestMarkVersionNotified_keepsOwner(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "state.json"))

	if _, _, err := m.ClaimOrCheckOwner(100); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := m.MarkVersionNotified("0.20260727.30"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	owner, ok := m.Owner()
	if !ok || owner != 100 {
		t.Fatalf("expected owner 100 to survive version write, got owner=%d ok=%t", owner, ok)
	}
}

func TestClaimOrCheckOwner_concurrentClaimsHaveOneWinner(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "state.json"))

	const n = 20
	var wg sync.WaitGroup
	winners := make(chan int64, n)

	for i := int64(0); i < n; i++ {
		wg.Add(1)
		go func(chatID int64) {
			defer wg.Done()
			owner, isOwner, err := m.ClaimOrCheckOwner(chatID)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if isOwner {
				winners <- owner
			}
		}(i)
	}
	wg.Wait()
	close(winners)

	var count int
	var first int64 = -1
	for w := range winners {
		count++
		first = w
	}
	if count != 1 {
		t.Fatalf("expected exactly one chat to win ownership, got %d winners (last=%d)", count, first)
	}
}
