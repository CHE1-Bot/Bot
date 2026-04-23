package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/bwmarrin/discordgo"

	"github.com/che1/bot/config"
	"github.com/che1/bot/internal/actions"
	"github.com/che1/bot/internal/applications"
	"github.com/che1/bot/internal/dashboard"
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

	var database *db.DB
	if cfg.PostgresURL != "" {
		database, err = db.New(ctx, cfg.PostgresURL)
		if err != nil {
			log.Fatalf("db: %v", err)
		}
		defer database.Close()
	} else {
		log.Println("POSTGRES_URL not set — running without database; DB-backed modules disabled")
	}

	var jobs *worker.Queue
	if cfg.WorkerURL != "" {
		jobs = worker.NewQueue(cfg.WorkerURL, cfg.WorkerAPIKey)
		defer jobs.Close()
		log.Printf("worker linked: %s", cfg.WorkerURL)
	} else {
		log.Println("WORKER_URL not set — Worker integration disabled")
	}

	var dash *dashboard.Client
	if cfg.DashboardURL != "" {
		dash = dashboard.New(cfg.DashboardURL, cfg.DashboardAPIKey)
		log.Printf("dashboard linked: %s", cfg.DashboardURL)
	} else {
		log.Println("DASHBOARD_URL not set — Dashboard integration disabled")
	}

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
	if database != nil {
		router.Register(&tickets.Module{DB: database, Worker: jobs})
		router.Register(leveling.New(database, jobs))
		router.Register(&moderation.Module{DB: database})
		router.Register(&applications.Module{DB: database, Dashboard: dash})
		router.Register(&giveaways.Module{DB: database, Worker: jobs})
	}

	if err := session.Open(); err != nil {
		log.Fatalf("open: %v", err)
	}
	defer session.Close()

	if err := router.Attach(session, cfg.GuildID); err != nil {
		log.Fatalf("attach: %v", err)
	}

	// Subscribe to dashboard-driven actions emitted by the Worker hub.
	if jobs != nil {
		wsURL := cfg.WorkerWSURL
		if wsURL == "" {
			wsURL = worker.DeriveWSURL(cfg.WorkerURL)
		}
		if wsURL != "" {
			sub := worker.NewSubscriber(wsURL, jobs)
			(&actions.Handlers{Session: session}).Register(sub)
			go sub.Run(ctx)
			log.Printf("subscribed to worker events: %s", wsURL)
		}
	}

	log.Printf("CHE1 bot online as %s", session.State.User.Username)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Println("shutting down")
}
