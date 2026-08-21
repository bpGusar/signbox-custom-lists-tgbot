package bot

import (
	"context"
	"fmt"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"lst-signbox-lists-tgbot/internal/service"
	"lst-signbox-lists-tgbot/internal/version"
)

const (
	menuBtnUpgrade      = "⬆️ Обновить бота"
	upgradePollInterval = 3 * time.Second
	// Cap on how long an install may take before we stop waiting on it. The
	// download and two opkg installs are slow on a router, but not this slow.
	upgradeTimeout = 20 * time.Minute
)

// handleUpgradePrompt shows the installed and available versions, and offers
// to start the upgrade.
func (a *App) handleUpgradePrompt(ctx context.Context, b *tgbot.Bot, update *models.Update, chatID int64) {
	a.editCallbackMessageMarkup(ctx, b, update, "🔎 Проверяю наличие обновлений…", nil)

	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	info, err := service.CheckUpgrade(cctx)
	if err != nil {
		a.logf(chatID, "upgrade check_error err=%v", err)
		a.editCallbackMessageMarkup(ctx, b, update,
			"❌ Не удалось проверить обновления: "+err.Error(),
			a.backToSettingsInlineKeyboard())
		return
	}

	if info.InProgress() {
		a.editCallbackMessageMarkup(ctx, b, update,
			"⏳ Обновление уже выполняется. Дождитесь его завершения.",
			a.backToSettingsInlineKeyboard())
		return
	}

	text := fmt.Sprintf("📦 Установлено: %s\n🌐 Доступно: %s", info.Current, orUnknown(info.Latest))
	if !info.UpdateAvailable {
		a.logf(chatID, "upgrade check up_to_date current=%s latest=%s", info.Current, info.Latest)
		a.editCallbackMessageMarkup(ctx, b, update,
			text+"\n\n✅ Установлена актуальная версия.",
			a.backToSettingsInlineKeyboard())
		return
	}

	a.logf(chatID, "upgrade check available current=%s latest=%s", info.Current, info.Latest)
	kb := &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{{Text: "⬆️ Обновить сейчас", CallbackData: menuCbPrefix + "upgrade_go"}},
			{{Text: menuBtnSettings, CallbackData: menuCbPrefix + "settings"}},
		},
	}
	a.editCallbackMessageMarkup(ctx, b, update,
		text+"\n\nОбновление скачает и установит пакеты, затем перезапустит бота.\n"+
			"Во время установки бот будет недоступен около минуты.",
		kb)
}

// handleUpgradeStart launches the background install. The install ends by
// restarting this service, so the pending record is persisted first: the
// process that comes back finishes reporting into the same message.
func (a *App) handleUpgradeStart(ctx context.Context, b *tgbot.Bot, update *models.Update, chatID int64) {
	messageID := update.CallbackQuery.Message.Message.ID

	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	info, err := service.UpgradeStatus(cctx)
	if err != nil {
		a.logf(chatID, "upgrade status_error err=%v", err)
		a.editCallbackMessageMarkup(ctx, b, update,
			"❌ Не удалось запустить обновление: "+err.Error(),
			a.backToSettingsInlineKeyboard())
		return
	}
	if info.InProgress() {
		a.editCallbackMessageMarkup(ctx, b, update,
			"⏳ Обновление уже выполняется.",
			a.backToSettingsInlineKeyboard())
		return
	}

	pending := service.PendingUpgrade{
		ChatID:      chatID,
		MessageID:   messageID,
		FromVersion: version.Display(),
		ToVersion:   info.Latest,
		StartedAt:   time.Now(),
	}
	if err := a.svc.SetPendingUpgrade(pending); err != nil {
		a.logf(chatID, "upgrade pending_save_error err=%v", err)
		a.editCallbackMessageMarkup(ctx, b, update,
			"❌ Не удалось сохранить состояние обновления: "+err.Error(),
			a.backToSettingsInlineKeyboard())
		return
	}

	a.editCallbackMessageMarkup(ctx, b, update,
		fmt.Sprintf("⏳ Обновление %s → %s запущено…", pending.FromVersion, orUnknown(pending.ToVersion)),
		nil)

	if err := service.StartUpgrade(cctx); err != nil {
		a.logf(chatID, "upgrade start_error err=%v", err)
		_ = a.svc.ClearPendingUpgrade()
		a.editCallbackMessageMarkup(ctx, b, update,
			"❌ Не удалось запустить обновление: "+err.Error(),
			a.backToSettingsInlineKeyboard())
		return
	}

	a.logf(chatID, "upgrade started from=%s to=%s", pending.FromVersion, pending.ToVersion)
	go a.trackUpgrade(context.Background(), b, pending)
}

// resumePendingUpgrade picks up an upgrade that was running when this process
// was started — normally because the install restarted the service.
func (a *App) resumePendingUpgrade(ctx context.Context, b *tgbot.Bot) {
	pending := a.svc.PendingUpgradeInfo()
	if pending == nil {
		return
	}
	a.logf(pending.ChatID, "upgrade resume from=%s to=%s started_at=%s",
		pending.FromVersion, pending.ToVersion, pending.StartedAt.Format(time.RFC3339))
	a.trackUpgrade(ctx, b, *pending)
}

// trackUpgrade waits for the install to finish and reports the outcome into
// the message the user pressed the button on. It may be killed partway
// through — the install restarts this service — in which case the next
// process resumes it from the persisted record.
func (a *App) trackUpgrade(ctx context.Context, b *tgbot.Bot, pending service.PendingUpgrade) {
	deadline := pending.StartedAt.Add(upgradeTimeout)

	ticker := time.NewTicker(upgradePollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if time.Now().After(deadline) {
			a.logf(pending.ChatID, "upgrade timeout from=%s to=%s", pending.FromVersion, pending.ToVersion)
			a.finishUpgrade(ctx, b, pending,
				"⚠️ Обновление не завершилось за отведённое время.\n"+
					"Проверьте состояние на роутере: lst-signbox-lists-tgbot-upgrade status")
			return
		}

		sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		info, err := service.UpgradeStatus(sctx)
		cancel()
		if err != nil {
			// Transient: opkg may be mid-install, or we are being shut down.
			a.logf(pending.ChatID, "upgrade poll_error err=%v", err)
			continue
		}
		if info.InProgress() {
			a.editUpgradeProgress(ctx, b, pending)
			continue
		}

		switch info.State {
		case service.UpgradeStateSuccess:
			a.logf(pending.ChatID, "upgrade success version=%s", version.Display())
			a.finishUpgrade(ctx, b, pending, fmt.Sprintf(
				"✅ Обновление завершено.\n📦 Версия: %s", version.Display()))
		case service.UpgradeStateFailed:
			text := "❌ Обновление не удалось."
			if tail := service.UpgradeLogTail(15); tail != "" {
				text += "\n\n" + tail
			}
			a.logf(pending.ChatID, "upgrade failed from=%s to=%s", pending.FromVersion, pending.ToVersion)
			a.finishUpgrade(ctx, b, pending, text)
		default:
			// idle: the log was rotated or cleared out from under us, so fall
			// back to comparing what is actually running now.
			if version.Display() != pending.FromVersion {
				a.finishUpgrade(ctx, b, pending, fmt.Sprintf(
					"✅ Обновление завершено.\n📦 Версия: %s", version.Display()))
				return
			}
			a.logf(pending.ChatID, "upgrade inconclusive state=%s version=%s", info.State, version.Display())
			a.finishUpgrade(ctx, b, pending,
				"⚠️ Не удалось определить результат обновления.\n"+
					fmt.Sprintf("📦 Текущая версия: %s", version.Display()))
		}
		return
	}
}

func (a *App) editUpgradeProgress(ctx context.Context, b *tgbot.Bot, pending service.PendingUpgrade) {
	elapsed := int(time.Since(pending.StartedAt).Seconds())
	text := fmt.Sprintf("⏳ Обновление %s → %s… (%ds)",
		pending.FromVersion, orUnknown(pending.ToVersion), elapsed)
	if _, err := b.EditMessageText(ctx, &tgbot.EditMessageTextParams{
		ChatID:    pending.ChatID,
		MessageID: pending.MessageID,
		Text:      text,
	}); err != nil {
		a.logf(pending.ChatID, "upgrade progress_edit_error err=%v", err)
	}
}

func (a *App) finishUpgrade(ctx context.Context, b *tgbot.Bot, pending service.PendingUpgrade, text string) {
	if err := a.svc.ClearPendingUpgrade(); err != nil {
		a.logf(pending.ChatID, "upgrade pending_clear_error err=%v", err)
	}
	a.editOrResend(ctx, b, pending.ChatID, pending.MessageID, text)
}

func orUnknown(s string) string {
	if s == "" {
		return "неизвестно"
	}
	return s
}
