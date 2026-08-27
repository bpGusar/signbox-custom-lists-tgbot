package bot

import (
	"strings"
	"testing"
)

func TestTruncateForMessage_shortUnchanged(t *testing.T) {
	text := "hello"
	if got := truncateForMessage(text, 100); got != text {
		t.Fatalf("expected unchanged text, got %q", got)
	}
}

func TestTruncateForMessage_longAddsSuffix(t *testing.T) {
	text := strings.Repeat("line\n", 2000)
	got := truncateForMessage(text, 100)
	if len(got) > 100 {
		t.Fatalf("expected length <= 100, got %d", len(got))
	}
	if !strings.Contains(got, "список обрезан") {
		t.Fatalf("expected truncation suffix, got %q", got)
	}
}
