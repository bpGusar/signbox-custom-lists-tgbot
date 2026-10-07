package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"

	"lst-signbox-lists-tgbot/internal/bot"
	"lst-signbox-lists-tgbot/internal/config"
	"lst-signbox-lists-tgbot/internal/redact"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if err := setupLogging(cfg.LogPath, cfg.Token); err != nil {
		log.Fatalf("setup logging: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := bot.Run(ctx, cfg); err != nil {
		log.Fatalf("bot: %v", err)
	}
}

func setupLogging(path, token string) error {
	if path == "" {
		return fmt.Errorf("log path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := redact.File(path, token); err != nil {
		log.Printf("redact existing log: %v", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	log.SetOutput(redact.NewWriter(f, token))
	return nil
}
