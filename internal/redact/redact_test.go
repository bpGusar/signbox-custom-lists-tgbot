package redact

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestString(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			name: "telegram url",
			in:   `Post "https://api.telegram.org/bot8792349048:AAHcxUBV7sz6MRmY3uj-xYnIY2maKLbvWX8/getUpdates": EOF`,
			want: `Post "https://api.telegram.org/bot<redacted>/getUpdates": EOF`,
		},
		{
			name: "file url",
			in:   `https://api.telegram.org/file/bot123456789:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA/documents/file_1.txt`,
			want: `https://api.telegram.org/file/bot<redacted>/documents/file_1.txt`,
		},
		{
			name: "vless link",
			in:   `parse failed line="vless://0b9a1c2d-1111-2222-3333-444455556666@1.2.3.4:443?security=reality#⚡ NL" next`,
			want: `parse failed line="vless://<redacted> NL" next`,
		},
		{
			name: "ss link",
			in:   `bad ss://YWVzLTI1Ni1nY206cGFzcw@host:8388#name`,
			want: `bad ss://<redacted>`,
		},
		{
			name: "http userinfo",
			in:   `fetch http://user:p4ss@example.com/sub failed`,
			want: `fetch http://<redacted>@example.com/sub failed`,
		},
		{
			name: "uci secrets",
			in:   `lst-signbox-lists-tgbot.main.frp_token='s3cr3t' frp_luci_password="hunter2" password: qwerty`,
			want: `lst-signbox-lists-tgbot.main.frp_token=<redacted> frp_luci_password=<redacted> password: <redacted>`,
		},
		{
			name: "plain text untouched",
			in:   `2026/10/07 13:15:03 bot.go:182: lst-signbox-lists-tgbot chat_id=5393506054 menu proxy_links`,
			want: `2026/10/07 13:15:03 bot.go:182: lst-signbox-lists-tgbot chat_id=5393506054 menu proxy_links`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := String(c.in); got != c.want {
				t.Fatalf("String(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}

func TestStringLiteral(t *testing.T) {
	if got := String("cfg value=abc-short-secret end", "abc-short-secret"); got != "cfg value=<redacted> end" {
		t.Fatalf("got %q", got)
	}
}

func TestWriterThroughLogger(t *testing.T) {
	var buf bytes.Buffer
	l := log.New(NewWriter(&buf), "", 0)
	l.Printf("error get updates, Post %q: timeout", "https://api.telegram.org/bot8792349048:AAHcxUBV7sz6MRmY3uj-xYnIY2maKLbvWX8/getUpdates")
	if strings.Contains(buf.String(), "AAHcxUBV7sz6") {
		t.Fatalf("token leaked: %q", buf.String())
	}
}

func TestFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bot.log")
	in := "line one\nPost https://api.telegram.org/bot8792349048:AAHcxUBV7sz6MRmY3uj-xYnIY2maKLbvWX8/getUpdates\nno newline vless://id@h:1"
	if err := os.WriteFile(path, []byte(in), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := File(path); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "line one\nPost https://api.telegram.org/bot<redacted>/getUpdates\nno newline vless://<redacted>"
	if string(got) != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if _, err := os.Stat(path + ".redact"); !os.IsNotExist(err) {
		t.Fatalf("temp file left behind: %v", err)
	}
}

func TestFileMissing(t *testing.T) {
	if err := File(filepath.Join(t.TempDir(), "absent.log")); err != nil {
		t.Fatal(err)
	}
}
