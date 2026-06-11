package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	"lists-tg/internal/bot"
	"lists-tg/internal/config"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := bot.Run(ctx, cfg); err != nil {
		log.Fatalf("bot: %v", err)
	}
}
