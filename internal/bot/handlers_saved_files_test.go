package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"lst-signbox-lists-tgbot/internal/lists"
	"lst-signbox-lists-tgbot/internal/podkop"
	"lst-signbox-lists-tgbot/internal/service"
)

func savedFilesTestApp(t *testing.T, secs []podkop.Section, dir string) (*App, *tgbot.Bot, *recordingTelegramClient) {
	t.Helper()
	a := testApp(secs, nil)
	a.sess = NewSessionStore()
	a.svc = service.NewManager(filepath.Join(t.TempDir(), "state.json"))
	a.detectBackend = func() (podkop.Backend, error) { return podkop.Forkop, nil }
	a.readSavedFiles = func() ([]podkop.SavedListFile, error) { return podkop.SavedListFiles(dir) }
	client := &recordingTelegramClient{}
	b, err := tgbot.New("test", tgbot.WithSkipGetMe(), tgbot.WithHTTPClient(0, client))
	if err != nil {
		t.Fatal(err)
	}
	return a, b, client
}

func savedFilesTestClick(a *App, b *tgbot.Bot, data string) {
	update := &models.Update{CallbackQuery: &models.CallbackQuery{
		ID: "test", Data: data, Message: models.MaybeInaccessibleMessage{
			Message: &models.Message{ID: 1, Chat: models.Chat{ID: 1}},
		},
	}}
	if strings.HasPrefix(data, cbPrefix) {
		a.handleCallback(context.Background(), b, update)
	} else {
		a.handleMenuCallback(context.Background(), b, update)
	}
}

func savedFilesTestMessage(t *testing.T, client *recordingTelegramClient) (string, models.InlineKeyboardMarkup) {
	t.Helper()
	if len(client.messages) == 0 {
		t.Fatal("no messages sent")
	}
	msg := client.messages[len(client.messages)-1]
	var kb models.InlineKeyboardMarkup
	if err := json.Unmarshal([]byte(msg.keyboard), &kb); err != nil {
		t.Fatal(err)
	}
	return msg.text, kb
}

func savedFilesTestButton(t *testing.T, kb models.InlineKeyboardMarkup, label string) string {
	t.Helper()
	for _, row := range kb.InlineKeyboard {
		for _, button := range row {
			if len(button.CallbackData) > 64 {
				t.Fatalf("callback too long: %q", button.CallbackData)
			}
			if button.Text == label {
				return button.CallbackData
			}
		}
	}
	t.Fatalf("button %q missing in %+v", label, kb)
	return ""
}

func TestSavedFileRebindAfterBackendChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old_domain_list.lst")
	body := "// #cat: Video\nExample.COM\n// disabled.org\n"
	if err := os.WriteFile(path, []byte(body), 0o400); err != nil {
		t.Fatal(err)
	}
	secs := []podkop.Section{{Name: "main"}, {Name: "shared", DomainLists: []string{path}}}
	a, b, client := savedFilesTestApp(t, secs, dir)
	bindCalls := 0
	a.bindList = func(_ context.Context, section string, typ lists.EntryType, gotPath string) error {
		bindCalls++
		if section != "main" || typ != lists.TypeDomain || gotPath != path {
			t.Fatalf("unexpected binding: %s %v %s", section, typ, gotPath)
		}
		secs[0].DomainLists = []string{path}
		return nil
	}
	savedFilesTestClick(a, b, menuCbPrefix+"manage")
	_, kb := savedFilesTestMessage(t, client)
	savedFilesTestClick(a, b, savedFilesTestButton(t, kb, btnSavedFiles))
	text, kb := savedFilesTestMessage(t, client)
	if !strings.Contains(text, path) || !strings.Contains(text, "1 ✅ / 1 ⏸") {
		t.Fatalf("unexpected catalog: %q", text)
	}
	savedFilesTestClick(a, b, savedFilesTestButton(t, kb, "📄 old_domain_list.lst"))
	text, kb = savedFilesTestMessage(t, client)
	if !strings.Contains(text, "Уже привязан: shared") {
		t.Fatalf("missing existing binding: %q", text)
	}
	savedFilesTestClick(a, b, savedFilesTestButton(t, kb, "🗂 main"))
	text, kb = savedFilesTestMessage(t, client)
	if bindCalls != 0 || !strings.Contains(text, "Содержимое файла сохранится") {
		t.Fatalf("expected confirmation before binding: %q, calls=%d", text, bindCalls)
	}
	confirm := savedFilesTestButton(t, kb, btnBindConfirm)
	savedFilesTestClick(a, b, confirm)
	if bindCalls != 1 {
		t.Fatalf("bind calls = %d", bindCalls)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != body {
		t.Fatalf("saved file changed: %q, err=%v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o400 {
		t.Fatalf("file permissions changed: %v, err=%v", info, err)
	}
	state, err := a.svc.Load()
	if err != nil || !state.ServiceStale {
		t.Fatalf("restart requirement not recorded: %+v, err=%v", state, err)
	}
	savedFilesTestClick(a, b, confirm)
	if bindCalls != 1 {
		t.Fatal("stale confirmation created a duplicate binding")
	}
}

func TestSavedFileBindPreservesPendingInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old_domain_list.lst")
	for name, body := range map[string]string{"old_domain_list.lst": "example.com\n", "ip_list.lst": "1.1.1.1\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a, b, client := savedFilesTestApp(t, []podkop.Section{{Name: "main"}}, dir)
	a.bindList = func(context.Context, string, lists.EntryType, string) error { return nil }
	a.showBindConfirm(context.Background(), b, a.replyTo(nil, 1), PendingOp{
		Section: "main", ListType: lists.TypeDomain, Values: []string{"new.com"},
		ValueCategories: map[string]string{"new.com": "Video"},
	}, suggestBindPath("main", lists.TypeDomain))
	_, kb := savedFilesTestMessage(t, client)
	savedFilesTestClick(a, b, savedFilesTestButton(t, kb, btnSavedFiles))
	text, kb := savedFilesTestMessage(t, client)
	if strings.Contains(text, "ip_list.lst") {
		t.Fatalf("IP file offered for domains: %q", text)
	}
	savedFilesTestClick(a, b, savedFilesTestButton(t, kb, "📄 old_domain_list.lst"))
	_, kb = savedFilesTestMessage(t, client)
	savedFilesTestClick(a, b, savedFilesTestButton(t, kb, btnBindConfirm))
	text, kb = savedFilesTestMessage(t, client)
	if !strings.Contains(text, "new.com") || !strings.Contains(text, path) || !strings.Contains(text, "Video") {
		t.Fatalf("pending input lost: %q", text)
	}
	add := savedFilesTestButton(t, kb, "➕ Добавить")
	opID := strings.SplitN(strings.TrimPrefix(add, cbPrefix), ":", 2)[0]
	op, ok := a.sess.Get(opID)
	if !ok || op.ListPath != path || op.ValueCategories["new.com"] != "Video" {
		t.Fatalf("unexpected operation: %+v", op)
	}
}

func TestSavedFileRemovedBeforeConfirmation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "domains.lst")
	if err := os.WriteFile(path, []byte("example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, b, client := savedFilesTestApp(t, []podkop.Section{{Name: "main"}}, dir)
	a.bindList = func(context.Context, string, lists.EntryType, string) error {
		t.Fatal("missing file must not be bound")
		return nil
	}
	savedFilesTestClick(a, b, targetCallback(verbSavedBind, lists.TypeDomain, sectionToken("main"), pathToken(path)))
	_, kb := savedFilesTestMessage(t, client)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	savedFilesTestClick(a, b, savedFilesTestButton(t, kb, btnBindConfirm))
	text, _ := savedFilesTestMessage(t, client)
	if !strings.Contains(text, "больше недоступен") {
		t.Fatalf("missing file not reported: %q", text)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file was recreated: %v", err)
	}
}

func TestSavedFilesPagination(t *testing.T) {
	a, b, client := savedFilesTestApp(t, nil, t.TempDir())
	var files []podkop.SavedListFile
	for i := 0; i < 11; i++ {
		files = append(files, podkop.SavedListFile{Path: fmt.Sprintf("%s/domain%d.lst", listsDir, i), Type: lists.TypeDomain})
	}
	a.readSavedFiles = func() ([]podkop.SavedListFile, error) { return files, nil }
	savedFilesTestClick(a, b, menuCbPrefix+"savedpage_0")
	text, kb := savedFilesTestMessage(t, client)
	if !strings.Contains(text, "Страница 1 из 2") || strings.Contains(text, "domain8.lst") || len(text) > tgMaxMessageLen {
		t.Fatalf("unexpected first page: %q", text)
	}
	savedFilesTestClick(a, b, savedFilesTestButton(t, kb, "Далее ➡️"))
	text, kb = savedFilesTestMessage(t, client)
	if !strings.Contains(text, "Страница 2 из 2") || !strings.Contains(text, "domain10.lst") || strings.Contains(text, "domain0.lst") {
		t.Fatalf("unexpected second page: %q", text)
	}
	savedFilesTestClick(a, b, savedFilesTestButton(t, kb, "⬅️ Назад"))
	text, _ = savedFilesTestMessage(t, client)
	if !strings.Contains(text, "Страница 1 из 2") {
		t.Fatalf("failed to navigate back: %q", text)
	}
}
