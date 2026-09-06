package bot

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"lst-signbox-lists-tgbot/internal/podkop"
	"lst-signbox-lists-tgbot/internal/probe"
	"lst-signbox-lists-tgbot/internal/proxylink"
	"lst-signbox-lists-tgbot/internal/singbox"
)

// proxyCbPrefix keeps the import flow in a callback namespace of its own: it
// works on a ProxyImport, not on the PendingOp everything under "s:" expects.
const proxyCbPrefix = "p:"

// Actions carried by the import screens.
const (
	cbProxyDefaultPing = "def"
	cbProxyAskPing     = "ask"
	cbProxyStop        = "stop"
	cbProxyAddGo       = "add"
	cbProxyReplaceGo   = "rep"
	cbProxyLatency     = "lat"
	cbProxyReport      = "back"
	cbProxySecPrefix   = "sec_"
	cbProxyConvPrefix  = "conv_"
	cbProxyConvGo      = "conv!_"
)

const (
	// defaultMaxPing is the threshold offered on the first screen.
	defaultMaxPing = 400 * time.Millisecond
	// importDeadline caps the whole measurement, however it goes.
	importDeadline = 10 * time.Minute
	// progressInterval is how often the progress message may be rewritten.
	// Telegram rate-limits edits, and the numbers are not worth a flood.
	progressInterval = 3 * time.Second
)

// handleDocument takes a subscription file sent into the chat. It is the only
// thing the bot does with a document, so there is no file type to pick.
func (a *App) handleDocument(ctx context.Context, b *tgbot.Bot, update *models.Update) {
	chatID := update.Message.Chat.ID
	doc := update.Message.Document
	a.sess.ClearAwait(chatID)

	limits := proxylink.DefaultLimits()
	a.logf(chatID, "proxy_import document name=%q size=%d", doc.FileName, doc.FileSize)

	if doc.FileSize > limits.MaxBytes {
		a.sendPlain(ctx, b, chatID,
			fmt.Sprintf("❌ Файл больше %d КБ — это не похоже на список ссылок.", limits.MaxBytes/1024),
			a.backToMainMenuInlineKeyboard())
		return
	}

	data, err := a.downloadDocument(ctx, b, doc, limits.MaxBytes)
	if err != nil {
		a.logf(chatID, "proxy_import download_error err=%v", err)
		a.sendPlain(ctx, b, chatID, "❌ Не удалось скачать файл: "+err.Error(), a.backToMainMenuInlineKeyboard())
		return
	}

	links, stats, err := proxylink.ParseAll(bytes.NewReader(data), limits)
	if err != nil {
		a.logf(chatID, "proxy_import parse_error err=%v", err)
		a.sendPlain(ctx, b, chatID, "❌ Не удалось разобрать файл: "+err.Error(), a.backToMainMenuInlineKeyboard())
		return
	}
	a.logf(chatID, "proxy_import parsed lines=%d parsed=%d bolt=%d lte=%d collapsed=%d kept=%d targets=%d",
		stats.Lines, stats.Parsed, stats.Bolt, stats.LTE, stats.Collapsed, stats.Kept, stats.Targets)

	if len(links) == 0 {
		a.sendPlain(ctx, b, chatID, importStatsText(doc.FileName, stats)+"\n\n"+noLinksHint(stats),
			a.backToMainMenuInlineKeyboard())
		return
	}

	imp := a.sess.CreateImport(ProxyImport{
		ChatID:   chatID,
		FileName: doc.FileName,
		Links:    links,
		Stats:    stats,
		MaxPing:  defaultMaxPing,
	})

	text := importStatsText(doc.FileName, stats) + "\n\n" +
		"Ссылки медленнее порога отсеются. Порог по умолчанию — " + probe.FormatLatency(defaultMaxPing) + "."
	a.sendPlain(ctx, b, chatID, text, &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: "⚡ Проверить с порогом " + probe.FormatLatency(defaultMaxPing),
				CallbackData: proxyCbPrefix + imp.ID + ":" + cbProxyDefaultPing}},
			{{Text: "✏️ Ввести порог", CallbackData: proxyCbPrefix + imp.ID + ":" + cbProxyAskPing}},
			{{Text: btnCancel, CallbackData: proxyCbPrefix + imp.ID + ":cancel"}},
		},
	})
}

// downloadDocument fetches the file Telegram is holding. The download link
// carries the bot token, so it never reaches a log or a message.
func (a *App) downloadDocument(ctx context.Context, b *tgbot.Bot, doc *models.Document, max int64) ([]byte, error) {
	f, err := b.GetFile(ctx, &tgbot.GetFileParams{FileID: doc.FileID})
	if err != nil {
		return nil, err
	}

	dctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(dctx, http.MethodGet, b.FileDownloadLink(f), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// The error text would repeat the URL, and the URL is the token.
		return nil, fmt.Errorf("сеть недоступна")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Telegram ответил %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, max))
}

func importStatsText(fileName string, st proxylink.Stats) string {
	var sb strings.Builder
	sb.WriteString("📥 Прокси-ссылки")
	if fileName != "" {
		sb.WriteString(" · " + fileName)
	}
	sb.WriteString(fmt.Sprintf("\n\nСтрок: %d\nСсылок: %d", st.Lines, st.Parsed))
	if st.Skipped > 0 {
		sb.WriteString(fmt.Sprintf(" (пропущено строк: %d)", st.Skipped))
	}
	sb.WriteString(fmt.Sprintf("\nС ⚡: %d\nОтсеяно по LTE: %d\nСхлопнуто дублей: %d", st.Bolt, st.LTE, st.Collapsed))
	sb.WriteString(fmt.Sprintf("\n\nК проверке: %d — %d уникальных адресов", st.Kept, st.Targets))
	if st.Truncated {
		sb.WriteString("\n\n⚠️ Файл прочитан не целиком — сработал лимит.")
	}
	return sb.String()
}

func noLinksHint(st proxylink.Stats) string {
	switch {
	case st.Parsed == 0:
		return "❌ В файле нет ссылок поддерживаемых схем (vless, ss, trojan, socks, hysteria2)."
	case st.Bolt == 0:
		return "❌ Ни одна ссылка не помечена ⚡ — брать нечего."
	default:
		return "❌ После отсева по LTE и дублям не осталось ни одной ссылки."
	}
}

// handleProxyCallback routes every button of the import flow.
func (a *App) handleProxyCallback(ctx context.Context, b *tgbot.Bot, update *models.Update) {
	if update.CallbackQuery == nil {
		return
	}
	chatID := update.CallbackQuery.Message.Message.Chat.ID
	data := strings.TrimPrefix(update.CallbackQuery.Data, proxyCbPrefix)

	impID, action, _ := strings.Cut(data, ":")
	imp, ok := a.sess.GetImport(impID)
	if !ok {
		a.logf(chatID, "proxy_callback stale id=%s action=%s", impID, action)
		_, _ = b.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "⏳ Импорт устарел. Пришлите файл снова.",
		})
		return
	}
	a.logf(chatID, "proxy_callback id=%s action=%s", impID, action)

	switch {
	case action == "cancel":
		a.sess.DeleteImport(impID)
		a.sess.ClearAwait(chatID)
		a.answerAndEditMarkup(ctx, b, update, "❌ Импорт отменён.", a.backToMainMenuInlineKeyboard())
	case action == cbProxyDefaultPing:
		a.startProxyRun(ctx, b, update, imp, defaultMaxPing)
	case action == cbProxyAskPing:
		a.sess.Await(chatID, awaitMaxPing, imp.ID)
		a.answerAndEditMarkup(ctx, b, update,
			"✏️ Пришлите порог задержки одним сообщением: «400», «400 мс» или «0.4s».",
			a.proxyCancelKeyboard(imp.ID))
	case action == cbProxyStop:
		a.sess.UpdateImport(imp.ID, func(p *ProxyImport) {
			if p.cancel != nil {
				p.cancel()
			}
		})
		a.answerCallback(ctx, b, update)
	case action == cbProxyReport:
		a.answerCallback(ctx, b, update)
		a.showProxyReport(ctx, b, imp)
	case strings.HasPrefix(action, cbProxySecPrefix):
		a.handleProxySectionPick(ctx, b, update, imp, strings.TrimPrefix(action, cbProxySecPrefix))
	case strings.HasPrefix(action, cbProxyConvGo):
		a.execProxyConvert(ctx, b, update, imp, strings.TrimPrefix(action, cbProxyConvGo))
	case strings.HasPrefix(action, cbProxyConvPrefix):
		a.showProxyConvertConfirm(ctx, b, update, imp, strings.TrimPrefix(action, cbProxyConvPrefix))
	case action == cbProxyAddGo:
		a.execProxyWrite(ctx, b, update, imp, false)
	case action == cbProxyReplaceGo:
		a.execProxyWrite(ctx, b, update, imp, true)
	case action == cbProxyLatency:
		a.showGroupLatency(ctx, b, update, imp)
	default:
		a.answerCallback(ctx, b, update)
	}
}

func (a *App) proxyCancelKeyboard(impID string) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: btnCancel, CallbackData: proxyCbPrefix + impID + ":cancel"}},
		},
	}
}

// handleMaxPingText reads a threshold typed by hand and starts the run.
func (a *App) handleMaxPingText(ctx context.Context, b *tgbot.Bot, chatID int64, impID, text string) {
	imp, ok := a.sess.GetImport(impID)
	if !ok {
		a.sendPlain(ctx, b, chatID, "⏳ Импорт устарел. Пришлите файл снова.", a.backToMainMenuInlineKeyboard())
		return
	}
	maxPing, err := probe.ParseMaxPing(text)
	if err != nil {
		// A rejected threshold is a typo, not a reason to unwind the flow.
		a.sess.Await(chatID, awaitMaxPing, impID)
		a.sendPlain(ctx, b, chatID, "❌ "+err.Error()+"\n\nПришлите другое значение.", a.proxyCancelKeyboard(impID))
		return
	}
	a.startProxyRun(ctx, b, nil, imp, maxPing)
}

// startProxyRun kicks the measurement off in the background: it takes minutes,
// and the update that started it is long gone by then.
func (a *App) startProxyRun(ctx context.Context, b *tgbot.Bot, update *models.Update, imp *ProxyImport, maxPing time.Duration) {
	chatID := imp.ChatID
	if imp.Running {
		if update != nil {
			a.answerCallback(ctx, b, update)
		}
		return
	}

	text := fmt.Sprintf("⏱ Проверяю %s\nПорог: %s\n\nГотово: 0 / %d",
		pluralLinks(len(imp.Links)), probe.FormatLatency(maxPing), imp.Stats.Targets)
	kb := &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: "⏹ Остановить", CallbackData: proxyCbPrefix + imp.ID + ":" + cbProxyStop}},
		},
	}

	messageID := 0
	if update != nil {
		a.answerAndEditMarkup(ctx, b, update, text, kb)
		messageID = update.CallbackQuery.Message.Message.ID
	} else {
		sent, err := b.SendMessage(ctx, &tgbot.SendMessageParams{ChatID: chatID, Text: text, ReplyMarkup: kb})
		if err != nil {
			a.logf(chatID, "proxy_run status_message_error err=%v", err)
			return
		}
		messageID = sent.ID
	}

	runCtx, cancel := context.WithTimeout(context.Background(), importDeadline)
	a.sess.UpdateImport(imp.ID, func(p *ProxyImport) {
		p.MaxPing = maxPing
		p.MessageID = messageID
		p.Running = true
		p.cancel = cancel
	})
	a.logf(chatID, "proxy_run started id=%s links=%d targets=%d max_ping=%s",
		imp.ID, len(imp.Links), imp.Stats.Targets, maxPing)

	go func() {
		defer cancel()
		a.runProxyMeasurement(runCtx, b, imp.ID)
	}()
}

// runProxyMeasurement is the two-stage measurement: a cheap TCP/ICMP sweep
// first, then — for what survived it — the real thing through a tunnel.
func (a *App) runProxyMeasurement(ctx context.Context, b *tgbot.Bot, impID string) {
	imp, ok := a.sess.GetImport(impID)
	if !ok {
		return
	}
	chatID, maxPing, links := imp.ChatID, imp.MaxPing, imp.Links

	progress := newProgressWriter(b, chatID, imp.MessageID, impID)

	targets := make([]probe.Target, 0, imp.Stats.Targets)
	for _, l := range proxylink.Endpoints(links) {
		targets = append(targets, probe.Target{Host: l.Host, Port: l.Port, UDP: l.UDP})
	}

	byEndpoint := probe.Run(ctx, targets, probe.DefaultOptions(maxPing), func(done, total int) {
		progress.write(fmt.Sprintf("⏱ Этап 1: доступность серверов\nГотово: %d / %d", done, total))
	})

	results := make(map[string]linkResult, len(links))
	var survivors []proxylink.Link
	usedICMP := false
	for _, l := range links {
		r, measured := byEndpoint[l.Endpoint()]
		if r.Method == probe.MethodICMP {
			usedICMP = true
		}
		switch {
		case !measured:
			// The run was stopped before this target came up.
			results[l.DedupKey()] = linkResult{Reason: "проверка остановлена"}
		case !r.OK:
			results[l.DedupKey()] = linkResult{Reason: "сервер не ответил"}
		case r.Latency > maxPing:
			results[l.DedupKey()] = linkResult{Latency: r.Latency, OK: true}
		default:
			results[l.DedupKey()] = linkResult{Latency: r.Latency, OK: true}
			survivors = append(survivors, l)
		}
	}

	method := probe.MethodTCP
	if usedICMP {
		method = probe.MethodICMP
	}
	tunnel, note := a.measureThroughTunnel(ctx, chatID, survivors, maxPing, results, progress)

	a.sess.UpdateImport(impID, func(p *ProxyImport) {
		p.Results = results
		p.Method = method
		p.Tunnel = tunnel
		p.TunnelNote = note
		p.Running = false
		p.cancel = nil
	})
	imp, ok = a.sess.GetImport(impID)
	if !ok {
		return
	}
	a.logf(chatID, "proxy_run finished id=%s passed=%d failed=%d tunnel=%t",
		impID, len(imp.Passed()), len(imp.Failed()), tunnel)

	a.showProxyReport(context.Background(), b, imp)
}

// measureThroughTunnel is stage B. It is allowed to fail: the numbers from
// stage A are still worth something, and the reason is shown in the report.
func (a *App) measureThroughTunnel(
	ctx context.Context,
	chatID int64,
	survivors []proxylink.Link,
	maxPing time.Duration,
	results map[string]linkResult,
	progress *progressWriter,
) (bool, string) {
	if len(survivors) == 0 || ctx.Err() != nil {
		return false, ""
	}
	if ok, reason := singbox.Available(ctx); !ok {
		a.logf(chatID, "proxy_run tunnel_unavailable reason=%q", reason)
		return false, reason
	}
	batch := survivors
	if len(batch) > singbox.MaxBatch {
		batch = batch[:singbox.MaxBatch]
	}

	measured, err := singbox.Measure(ctx, batch, singbox.DefaultOptions(maxPing), func(done, total int) {
		progress.write(fmt.Sprintf("⏱ Этап 2: задержка через туннель\nГотово: %d / %d", done, total))
	})
	if err != nil {
		a.logf(chatID, "proxy_run tunnel_error err=%v", err)
		return false, err.Error()
	}

	for _, l := range batch {
		r, ok := measured[l.DedupKey()]
		switch {
		case !ok:
			results[l.DedupKey()] = linkResult{Reason: "проверка прервана"}
		case r.OK:
			results[l.DedupKey()] = linkResult{Latency: r.Latency, OK: true}
		default:
			reason := "нет ответа через туннель"
			if r.Err != nil {
				reason = r.Err.Error()
			}
			results[l.DedupKey()] = linkResult{Reason: reason}
		}
	}
	// Anything past the batch cap has no comparable number: the report is
	// about to say every figure in it came through the tunnel.
	for _, l := range survivors[len(batch):] {
		results[l.DedupKey()] = linkResult{Reason: "не поместилась в пачку проверки через туннель"}
	}
	return true, ""
}

// progressWriter rewrites one message as the run goes, no more often than
// Telegram is happy to be edited.
type progressWriter struct {
	bot       *tgbot.Bot
	chatID    int64
	messageID int
	impID     string
	// mu guards last: stage A reports progress from every worker at once.
	mu   sync.Mutex
	last time.Time
}

func newProgressWriter(b *tgbot.Bot, chatID int64, messageID int, impID string) *progressWriter {
	return &progressWriter{bot: b, chatID: chatID, messageID: messageID, impID: impID}
}

func (w *progressWriter) write(text string) {
	w.mu.Lock()
	if w.messageID == 0 || time.Since(w.last) < progressInterval {
		w.mu.Unlock()
		return
	}
	w.last = time.Now()
	w.mu.Unlock()

	_, _ = w.bot.EditMessageText(context.Background(), &tgbot.EditMessageTextParams{
		ChatID:    w.chatID,
		MessageID: w.messageID,
		Text:      text,
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{
				{{Text: "⏹ Остановить", CallbackData: proxyCbPrefix + w.impID + ":" + cbProxyStop}},
			},
		},
	})
}

// showProxyReport is what the run ends at: what passed, what did not, and
// where the numbers came from.
func (a *App) showProxyReport(ctx context.Context, b *tgbot.Bot, imp *ProxyImport) {
	passed, failed := imp.Passed(), imp.Failed()

	head := fmt.Sprintf("📊 Проверка завершена\nМетрика: %s\nПорог: %s\n\n✅ Прошли: %d\n❌ Не прошли: %d",
		metricLabel(imp), probe.FormatLatency(imp.MaxPing), len(passed), len(failed))
	if imp.TunnelNote != "" {
		head += "\n\nℹ️ Через туннель померить не вышло: " + imp.TunnelNote
	}

	full := head + "\n\n" + reportBody(imp, passed, failed)

	var kb *models.InlineKeyboardMarkup
	if len(passed) == 0 {
		kb = a.backToMainMenuInlineKeyboard()
	} else {
		rows := a.proxySectionRows(ctx, imp.ID)
		if len(rows) == 0 {
			full += "\n\n🔗 В podkop нет секции, которая принимает список ссылок."
			kb = a.backToMainMenuInlineKeyboard()
		} else {
			full = full + "\n\nВ какую секцию записать?"
			rows = append(rows, []models.InlineKeyboardButton{{
				Text: btnCancel, CallbackData: proxyCbPrefix + imp.ID + ":cancel",
			}})
			kb = &models.InlineKeyboardMarkup{InlineKeyboard: rows}
		}
	}

	// A report that does not fit a message goes out as a file rather than
	// being cut: the lines it drops would be exactly the ones being judged.
	if len(full) > listMessageMaxLen {
		a.sendReportFile(ctx, b, imp, head+"\n\n"+reportBody(imp, passed, failed))
		short := head
		if kb != nil && len(passed) > 0 {
			short += "\n\nПолный отчёт отправлен файлом.\n\nВ какую секцию записать?"
		} else {
			short += "\n\nПолный отчёт отправлен файлом."
		}
		a.editOrSend(ctx, b, imp, short, kb)
		return
	}
	a.editOrSend(ctx, b, imp, full, kb)
}

func reportBody(imp *ProxyImport, passed, failed []proxylink.Link) string {
	var sb strings.Builder
	if len(passed) > 0 {
		sb.WriteString(fmt.Sprintf("✅ Прошли (%d):\n", len(passed)))
		for _, l := range passed {
			sb.WriteString(fmt.Sprintf("• %s — %s\n  %s\n",
				probe.FormatLatency(imp.Results[l.DedupKey()].Latency), l.Title(), l.Masked()))
		}
	}
	if len(failed) > 0 {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(fmt.Sprintf("❌ Не прошли (%d):\n", len(failed)))
		for _, l := range failed {
			r := imp.Results[l.DedupKey()]
			why := r.Reason
			if why == "" {
				why = probe.FormatLatency(r.Latency)
			}
			sb.WriteString(fmt.Sprintf("• %s — %s\n  %s\n", why, l.Title(), l.Masked()))
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (a *App) sendReportFile(ctx context.Context, b *tgbot.Bot, imp *ProxyImport, body string) {
	if _, err := b.SendDocument(ctx, &tgbot.SendDocumentParams{
		ChatID: imp.ChatID,
		Document: &models.InputFileUpload{
			Filename: "proxy-report.txt",
			Data:     strings.NewReader(body + "\n"),
		},
	}); err != nil {
		a.logf(imp.ChatID, "proxy_report file_error err=%v", err)
	}
}

// editOrSend puts a screen into the message the run has been writing into,
// falling back to a new message when that one is gone.
func (a *App) editOrSend(ctx context.Context, b *tgbot.Bot, imp *ProxyImport, text string, kb *models.InlineKeyboardMarkup) {
	if imp.MessageID != 0 {
		if _, err := b.EditMessageText(ctx, &tgbot.EditMessageTextParams{
			ChatID:      imp.ChatID,
			MessageID:   imp.MessageID,
			Text:        text,
			ReplyMarkup: kb,
		}); err == nil {
			return
		}
	}
	a.sendPlain(ctx, b, imp.ChatID, text, kb)
}

// metricLabel says what the numbers in the report actually measure, because
// the two stages measure different things and are not comparable.
func metricLabel(imp *ProxyImport) string {
	if imp.Tunnel {
		return "через туннель (sing-box)"
	}
	if imp.Method == probe.MethodICMP {
		return "TCP-хендшейк до сервера, ICMP ping для hysteria2"
	}
	return probe.MethodTCP.Label()
}

// proxySectionRows lays out the sections a list of links can go into, plus the
// single-link ones that would have to be switched over first.
func (a *App) proxySectionRows(ctx context.Context, impID string) [][]models.InlineKeyboardButton {
	var rows [][]models.InlineKeyboardButton
	for _, s := range a.sections(ctx) {
		switch {
		case s.AcceptsProxyLinks():
			label := fmt.Sprintf("🗂 %s · %s · %d", s.Name, s.ProxyConfigType, len(s.ProxyLinks))
			rows = append(rows, []models.InlineKeyboardButton{{
				Text:         label,
				CallbackData: proxyCbPrefix + impID + ":" + cbProxySecPrefix + sectionToken(s.Name),
			}})
		case s.ConvertibleToURLTest():
			rows = append(rows, []models.InlineKeyboardButton{{
				Text:         "🔁 " + s.Name + " · одна ссылка (URL)",
				CallbackData: proxyCbPrefix + impID + ":" + cbProxyConvPrefix + sectionToken(s.Name),
			}})
		}
	}
	return rows
}

// findProxySection resolves a token to a section, which may have changed under
// the button since it was drawn.
func (a *App) findProxySection(ctx context.Context, token string) (podkop.Section, error) {
	for _, s := range a.sections(ctx) {
		if s.Name != "" && sectionToken(s.Name) == token {
			return s, nil
		}
	}
	return podkop.Section{}, errSectionGone
}

func (a *App) handleProxySectionPick(ctx context.Context, b *tgbot.Bot, update *models.Update, imp *ProxyImport, token string) {
	s, err := a.findProxySection(ctx, token)
	if err != nil {
		a.answerAndEditMarkup(ctx, b, update, targetError(err), a.backToMainMenuInlineKeyboard())
		return
	}
	if !s.AcceptsProxyLinks() {
		a.answerAndEditMarkup(ctx, b, update,
			fmt.Sprintf("🔗 Секция «%s» больше не принимает список ссылок (тип %s).", s.Name, s.ProxyConfigType),
			a.backToMainMenuInlineKeyboard())
		return
	}
	a.sess.UpdateImport(imp.ID, func(p *ProxyImport) { p.Section = s.Name })
	a.showProxyWritePreview(ctx, b, update, imp)
}

// showProxyConvertConfirm asks before changing how podkop picks its outbound:
// a section of type URL carries one link, and a list needs URLTest.
func (a *App) showProxyConvertConfirm(ctx context.Context, b *tgbot.Bot, update *models.Update, imp *ProxyImport, token string) {
	s, err := a.findProxySection(ctx, token)
	if err != nil {
		a.answerAndEditMarkup(ctx, b, update, targetError(err), a.backToMainMenuInlineKeyboard())
		return
	}
	if !s.ConvertibleToURLTest() {
		a.answerAndEditMarkup(ctx, b, update, "🔗 Эта секция уже не типа URL — вернитесь к отчёту.",
			a.proxyBackToReportKeyboard(imp.ID))
		return
	}

	text := fmt.Sprintf(
		"🔁 Секция «%s» настроена на одну ссылку (тип URL), список туда не положить.\n\n"+
			"Переключить её на URLTest? Ссылка, которая там сейчас, станет первой в списке, "+
			"а podkop будет сам выбирать самый быстрый узел.\n\n"+
			"Это меняет конфиг podkop — после переключения его нужно перезапустить.", s.Name)
	a.answerAndEditMarkup(ctx, b, update, text, &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: "✅ Переключить на URLTest", CallbackData: proxyCbPrefix + imp.ID + ":" + cbProxyConvGo + token}},
			{{Text: "⬅️ К отчёту", CallbackData: proxyCbPrefix + imp.ID + ":" + cbProxyReport}},
			{{Text: btnCancel, CallbackData: proxyCbPrefix + imp.ID + ":cancel"}},
		},
	})
}

func (a *App) execProxyConvert(ctx context.Context, b *tgbot.Bot, update *models.Update, imp *ProxyImport, token string) {
	s, err := a.findProxySection(ctx, token)
	if err != nil {
		a.answerAndEditMarkup(ctx, b, update, targetError(err), a.backToMainMenuInlineKeyboard())
		return
	}
	if err := podkop.ConvertToURLTest(ctx, s.Name); err != nil {
		a.logf(imp.ChatID, "proxy_convert error section=%q err=%v", s.Name, err)
		a.answerAndEditMarkup(ctx, b, update, "❌ Не удалось переключить секцию: "+err.Error(),
			a.proxyBackToReportKeyboard(imp.ID))
		return
	}
	a.logf(imp.ChatID, "proxy_convert section=%q", s.Name)
	_ = a.svc.MarkFilesChanged()

	a.sess.UpdateImport(imp.ID, func(p *ProxyImport) { p.Section = s.Name })
	a.showProxyWritePreview(ctx, b, update, imp)
}

func (a *App) proxyBackToReportKeyboard(impID string) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: "⬅️ К отчёту", CallbackData: proxyCbPrefix + impID + ":" + cbProxyReport}},
			{{Text: menuBtnMainMenu, CallbackData: menuCbPrefix + "main_menu"}},
		},
	}
}

// showProxyWritePreview is the last screen before podkop's config is touched:
// what is there now, what would be there after.
func (a *App) showProxyWritePreview(ctx context.Context, b *tgbot.Bot, update *models.Update, imp *ProxyImport) {
	current, ok := a.sess.GetImport(imp.ID)
	if !ok {
		a.answerAndEditMarkup(ctx, b, update, "⏳ Импорт устарел. Пришлите файл снова.",
			a.backToMainMenuInlineKeyboard())
		return
	}
	imp = current
	if imp.Section == "" {
		a.answerAndEditMarkup(ctx, b, update, "⏳ Секция не выбрана — вернитесь к отчёту.",
			a.proxyBackToReportKeyboard(imp.ID))
		return
	}
	s, err := a.findProxySection(ctx, sectionToken(imp.Section))
	if err != nil {
		a.answerAndEditMarkup(ctx, b, update, targetError(err), a.backToMainMenuInlineKeyboard())
		return
	}

	passed := imp.Passed()
	links := forUCI(passed)
	have := make(map[string]struct{}, len(s.ProxyLinks))
	for _, l := range s.ProxyLinks {
		have[proxyLinkKey(l)] = struct{}{}
	}
	dupes := 0
	for _, l := range links {
		if _, ok := have[proxyLinkKey(l)]; ok {
			dupes++
		}
	}

	text := fmt.Sprintf("🗂 %s · %s\n\nСейчас в секции: %s\nГотовы к записи: %s\nИз них уже есть: %d\n\n"+
		"➕ Добавить → станет %d\n♻️ Перезаписать → станет %d",
		s.Name, s.ProxyConfigType,
		pluralLinks(len(s.ProxyLinks)), pluralLinks(len(links)), dupes,
		len(s.ProxyLinks)+len(links)-dupes, len(links))

	a.answerAndEditMarkup(ctx, b, update, text, &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: "➕ Добавить", CallbackData: proxyCbPrefix + imp.ID + ":" + cbProxyAddGo}},
			{{Text: "♻️ Перезаписать", CallbackData: proxyCbPrefix + imp.ID + ":" + cbProxyReplaceGo}},
			{{Text: "⬅️ К отчёту", CallbackData: proxyCbPrefix + imp.ID + ":" + cbProxyReport}},
			{{Text: btnCancel, CallbackData: proxyCbPrefix + imp.ID + ":cancel"}},
		},
	})
}

func (a *App) execProxyWrite(ctx context.Context, b *tgbot.Bot, update *models.Update, imp *ProxyImport, replace bool) {
	if imp.Section == "" {
		a.answerAndEditMarkup(ctx, b, update, "⏳ Секция не выбрана — вернитесь к отчёту.",
			a.proxyBackToReportKeyboard(imp.ID))
		return
	}
	links := forUCI(imp.Passed())
	if len(links) == 0 {
		a.answerAndEditMarkup(ctx, b, update, "ℹ️ Записывать нечего.", a.backToMainMenuInlineKeyboard())
		return
	}

	var msg string
	if replace {
		if err := podkop.ReplaceProxyLinks(ctx, imp.Section, links); err != nil {
			a.logf(imp.ChatID, "proxy_write replace_error section=%q err=%v", imp.Section, err)
			a.answerAndEditMarkup(ctx, b, update, "❌ Не удалось записать ссылки: "+err.Error(),
				a.proxyBackToReportKeyboard(imp.ID))
			return
		}
		a.logf(imp.ChatID, "proxy_write replaced section=%q count=%d", imp.Section, len(links))
		msg = fmt.Sprintf("♻️ Секция «%s» перезаписана: %s.", imp.Section, pluralLinks(len(links)))
	} else {
		added, skipped, err := podkop.AddProxyLinks(ctx, imp.Section, links)
		if err != nil {
			a.logf(imp.ChatID, "proxy_write add_error section=%q err=%v", imp.Section, err)
			a.answerAndEditMarkup(ctx, b, update, "❌ Не удалось записать ссылки: "+err.Error(),
				a.proxyBackToReportKeyboard(imp.ID))
			return
		}
		a.logf(imp.ChatID, "proxy_write added section=%q added=%d skipped=%d", imp.Section, added, skipped)
		msg = fmt.Sprintf("➕ В секцию «%s» добавлено: %d", imp.Section, added)
		if skipped > 0 {
			msg += fmt.Sprintf("\nУже были в секции: %d", skipped)
		}
	}

	_ = a.svc.MarkFilesChanged()
	a.answerAndEditMarkup(ctx, b, update, msg+"\n\nЧтобы podkop подхватил ссылки, его нужно перезапустить.",
		&models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{
				{{Text: "📡 Задержки в podkop", CallbackData: proxyCbPrefix + imp.ID + ":" + cbProxyLatency}},
				{{Text: menuBtnMainMenu, CallbackData: menuCbPrefix + "main_menu"}},
			},
		})
	a.maybeAutoRestart(imp.ChatID, b)
}

// showGroupLatency asks podkop what the links measure now that they are live —
// the only number that is measured the way podkop itself measures.
func (a *App) showGroupLatency(ctx context.Context, b *tgbot.Bot, update *models.Update, imp *ProxyImport) {
	if imp.Section == "" {
		a.answerAndEditMarkup(ctx, b, update, "⏳ Секция не выбрана.", a.backToMainMenuInlineKeyboard())
		return
	}
	latency, err := podkop.GroupLatency(ctx, imp.Section)
	if err != nil {
		a.logf(imp.ChatID, "proxy_group_latency error section=%q err=%v", imp.Section, err)
		a.answerAndEditMarkup(ctx, b, update,
			"ℹ️ Не удалось получить задержки от podkop: "+err.Error()+
				"\n\nОбычно это значит, что podkop ещё не перезапущен с новыми ссылками.",
			a.backToMainMenuInlineKeyboard())
		return
	}

	tags := make([]string, 0, len(latency))
	for tag := range latency {
		tags = append(tags, tag)
	}
	sortByValue(tags, latency)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📡 Задержки в секции «%s» (замер podkop):\n", imp.Section))
	for _, tag := range tags {
		sb.WriteString(fmt.Sprintf("\n• %d мс — %s", latency[tag], tag))
	}
	a.answerAndEditMarkup(ctx, b, update, truncateForMessage(sb.String(), listMessageMaxLen),
		a.backToMainMenuInlineKeyboard())
}

func sortByValue(keys []string, m map[string]int) {
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && m[keys[j]] < m[keys[j-1]]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
}

// forUCI renders the links the way they must be written into podkop's config.
func forUCI(links []proxylink.Link) []string {
	out := make([]string, 0, len(links))
	for _, l := range links {
		out = append(out, l.ForUCI())
	}
	return out
}

// proxyLinkKey matches podkop's own idea of "the same link": everything but
// the label.
func proxyLinkKey(raw string) string {
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		return raw[:i]
	}
	return raw
}

// pluralLinks spells a count of links out.
func pluralLinks(n int) string {
	form := "ссылок"
	if mod100 := n % 100; mod100 < 11 || mod100 > 14 {
		switch n % 10 {
		case 1:
			form = "ссылка"
		case 2, 3, 4:
			form = "ссылки"
		}
	}
	return fmt.Sprintf("%d %s", n, form)
}
