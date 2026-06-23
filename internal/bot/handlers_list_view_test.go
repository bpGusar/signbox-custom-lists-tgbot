package bot

import (
	"os"
	"path/filepath"
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

func TestListViewText_emptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.lst")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	app := &App{}
	got, err := app.listViewText(path, "тест")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Файл пуст.") {
		t.Fatalf("expected empty file message, got %q", got)
	}
}

func TestListViewText_withContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "list.lst")
	if err := os.WriteFile(path, []byte("example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	app := &App{}
	got, err := app.listViewText(path, "список доменов")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "example.com") {
		t.Fatalf("expected content in text, got %q", got)
	}
}
