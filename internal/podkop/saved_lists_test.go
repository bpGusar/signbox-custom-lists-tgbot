package podkop

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"lst-signbox-lists-tgbot/internal/lists"
)

func TestSavedListFiles(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"domains.lst":       "// #cat: Video\nExample.COM\n// other.org\n",
		"addresses.lst":     "1.1.1.1\n// 10.0.0.0/8\n",
		"empty_ip_list.lst": "",
		"empty.lst":         "// #cat: Empty\n",
		"bot.log":           "log text",
		"state.json":        "{}",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir.lst"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "domains.lst"), filepath.Join(dir, "link.lst")); err != nil {
		t.Fatal(err)
	}
	got, err := SavedListFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []SavedListFile{
		{Path: filepath.Join(dir, "addresses.lst"), Type: lists.TypeIP, Active: 1, Disabled: 1},
		{Path: filepath.Join(dir, "domains.lst"), Type: lists.TypeDomain, Active: 1, Disabled: 1},
		{Path: filepath.Join(dir, "empty.lst"), Type: lists.TypeDomain},
		{Path: filepath.Join(dir, "empty_ip_list.lst"), Type: lists.TypeIP},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %+v, want %+v", got, want)
	}
}

func TestSavedListFilesMissingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	files, err := SavedListFiles(dir)
	if err != nil || len(files) != 0 {
		t.Fatalf("files = %+v, err = %v", files, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("listing created the directory: %v", err)
	}
}

func TestSavedListFilesReadError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SavedListFiles(path); err == nil {
		t.Fatal("expected read error")
	}
}

func TestSavedListFilesBindToForkop(t *testing.T) {
	useBackend(t, Forkop)
	f := &fakeUCI{show: forkopShowFixture}
	f.install(t)
	dir := t.TempDir()
	bodies := map[string]string{"old_domain_list.lst": "example.com\n", "old_ip_list.lst": "1.1.1.1\n"}
	for name, body := range bodies {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o400); err != nil {
			t.Fatal(err)
		}
	}
	files, err := SavedListFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if err := Bind(context.Background(), "main", file.Type, file.Path); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(file.Path)
		if err != nil || string(data) != bodies[filepath.Base(file.Path)] {
			t.Fatalf("saved file changed: %q, err=%v", data, err)
		}
	}
	want := []string{
		"add_list forkop.main.domain_ip_lists=" + filepath.Join(dir, "old_domain_list.lst"), "commit forkop",
		"add_list forkop.main.domain_ip_lists=" + filepath.Join(dir, "old_ip_list.lst"), "commit forkop",
	}
	var got []string
	for _, call := range f.calls {
		got = append(got, strings.Join(call, " "))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("uci calls = %v, want %v", got, want)
	}
}
