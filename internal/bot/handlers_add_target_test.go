package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"lst-signbox-lists-tgbot/internal/podkop"
	"lst-signbox-lists-tgbot/internal/service"
)

type recordedMessage struct {
	text     string
	keyboard string
}

type recordingTelegramClient struct {
	messages []recordedMessage
}

func (c *recordingTelegramClient) Do(req *http.Request) (*http.Response, error) {
	if err := req.ParseMultipartForm(1 << 20); err != nil {
		return nil, err
	}
	defer req.MultipartForm.RemoveAll()
	if strings.HasSuffix(req.URL.Path, "/answerCallbackQuery") {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":true}`)),
			Header:     make(http.Header),
		}, nil
	}
	c.messages = append(c.messages, recordedMessage{
		text: req.FormValue("text"), keyboard: req.FormValue("reply_markup"),
	})
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":1}}`)),
		Header:     make(http.Header),
	}, nil
}

func TestDomainInputShowsExistingListsBeforeSelection(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.lst")
	second := filepath.Join(dir, "second.lst")
	empty := filepath.Join(dir, "empty.lst")
	for path, body := range map[string]string{
		first:  "// #cat: Video\nExample.COM\n// Other.ORG\n",
		second: "// EXAMPLE.com\n",
		empty:  "unrelated.com\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a := testApp([]podkop.Section{
		{Name: "main", DomainLists: []string{first, second, empty}},
		{Name: "shared", DomainLists: []string{first}},
		{Name: "Exclude"},
	}, nil)
	a.svc = service.NewManager(filepath.Join(dir, "state.json"))
	a.sess = NewSessionStore()
	client := &recordingTelegramClient{}
	b, err := tgbot.New("test", tgbot.WithSkipGetMe(), tgbot.WithHTTPClient(0, client))
	if err != nil {
		t.Fatal(err)
	}
	a.handleListInput(context.Background(), b, &models.Update{Message: &models.Message{
		Chat: models.Chat{ID: 1}, Text: "EXAMPLE.COM, example.com, OTHER.ORG, new.net",
	}})
	if len(client.messages) != 1 {
		t.Fatalf("messages = %+v", client.messages)
	}
	msg := client.messages[0]
	for _, want := range []string{first, second, "main, shared", "✅ example.com", "⏸ example.com", "⏸ other.org", "В какую секцию добавить?"} {
		if !strings.Contains(msg.text, want) {
			t.Errorf("missing %q in %q", want, msg.text)
		}
	}
	if strings.Count(msg.text, first) != 1 || strings.Contains(msg.text, empty) || strings.Contains(msg.text, "new.net") {
		t.Errorf("unexpected matches: %q", msg.text)
	}
	var kb models.InlineKeyboardMarkup
	if err := json.Unmarshal([]byte(msg.keyboard), &kb); err != nil {
		t.Fatal(err)
	}
	if len(kb.InlineKeyboard) != 4 {
		t.Fatalf("unexpected section choices: %+v", kb)
	}
	parts := strings.SplitN(strings.TrimPrefix(kb.InlineKeyboard[0][0].CallbackData, cbPrefix), ":", 2)
	op, ok := a.sess.Get(parts[0])
	if !ok || strings.Join(op.Values, ",") != "example.com,other.org,new.net" || op.ListPath != "" {
		t.Fatalf("unexpected pending operation: %+v", op)
	}
}

func TestDomainPresenceNoMatchesAndReadErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "domains.lst")
	if err := os.WriteFile(path, []byte("example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	secs := []podkop.Section{{Name: "main", DomainLists: []string{path}}}
	text := domainPresence(secs, []string{"sub.example.com"})
	if !strings.Contains(text, "совпадений нет") || strings.Contains(text, path) {
		t.Fatalf("unexpected matches: %q", text)
	}
	secs[0].DomainLists = append(secs[0].DomainLists, dir)
	text = domainPresence(secs, []string{"EXAMPLE.COM"})
	if !strings.Contains(text, "✅ EXAMPLE.COM") || !strings.Contains(text, "Не удалось прочитать "+dir) {
		t.Fatalf("expected matches alongside error: %q", text)
	}
	text = domainPresence(secs, []string{"missing.com"})
	if !strings.Contains(text, "В прочитанных списках") || !strings.Contains(text, "Не удалось прочитать "+dir) {
		t.Fatalf("incomplete search must be reported: %q", text)
	}
}

func TestDomainPresenceLongReportIsSplitWithoutLoss(t *testing.T) {
	path := filepath.Join(t.TempDir(), "domains.lst")
	var values []string
	for i := 0; i < 400; i++ {
		values = append(values, fmt.Sprintf("domain%d.example.com", i))
	}
	if err := os.WriteFile(path, []byte(strings.Join(values, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	report := domainPresence([]podkop.Section{{Name: "Секция", DomainLists: []string{path}}}, values)
	messages := splitPlainMessages(report, listMessageMaxLen)
	if len(messages) < 2 || strings.Join(messages, "") != report {
		t.Fatalf("report was lost or not split: %d messages", len(messages))
	}
	for _, msg := range messages {
		if len(msg) > listMessageMaxLen || !utf8.ValidString(msg) {
			t.Fatalf("invalid message: %q", msg)
		}
	}
}
