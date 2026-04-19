package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/bwmarrin/discordgo"

	"github.com/che1/bot/config"
	"github.com/che1/bot/internal/applications"
	"github.com/che1/bot/internal/db"
	"github.com/che1/bot/internal/giveaways"
	"github.com/che1/bot/internal/handler"
	"github.com/che1/bot/internal/leveling"
	"github.com/che1/bot/internal/moderation"
	"github.com/che1/bot/internal/tickets"
	"github.com/che1/bot/internal/worker"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	database, err := db.New(ctx, cfg.PostgresURL)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer database.Close()

	jobs := worker.NewQueue(cfg.RedisAddr, cfg.RedisPass)
	defer jobs.Close()

	session, err := discordgo.New("Bot " + cfg.DiscordToken)
	if err != nil {
		log.Fatalf("discord: %v", err)
	}
	session.Identify.Intents = discordgo.IntentsGuilds |
		discordgo.IntentsGuildMessages |
		discordgo.IntentsGuildMembers |
		discordgo.IntentsGuildMessageReactions |
		discordgo.IntentsMessageContent

	router := handler.New()
	router.Register(&tickets.Module{DB: database, Worker: jobs})
	router.Register(leveling.New(database, jobs))
	router.Register(&moderation.Module{DB: database})
	router.Register(&applications.Module{DB: database})
	router.Register(&giveaways.Module{DB: database, Worker: jobs})

	if err := session.Open(); err != nil {
		log.Fatalf("open: %v", err)
	}
	defer session.Close()

	if err := router.Attach(session, cfg.GuildID); err != nil {
		log.Fatalf("attach: %v", err)
	}

	log.Printf("CHE1 bot online as %s", session.State.User.Username)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Println("shutting down")
}
