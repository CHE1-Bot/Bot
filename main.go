package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/che1/bot/config"
	"github.com/che1/bot/internal/actions"
	"github.com/che1/bot/internal/applications"
	"github.com/che1/bot/internal/dashboard"
	"github.com/che1/bot/internal/db"
	"github.com/che1/bot/internal/giveaways"
	"github.com/che1/bot/internal/handler"
	"github.com/che1/bot/internal/httpsrv"
	"github.com/che1/bot/internal/leveling"
	"github.com/che1/bot/internal/moderation"
	"github.com/che1/bot/internal/tickets"
	"github.com/che1/bot/internal/worker"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		// slog isn't configured yet; stderr is fine for startup failures.
		_, _ = os.Stderr.WriteString("config error: " + err.Error() + "\n")
		os.Exit(1)
	}

	setupLogger(cfg)

	slog.Info("starting CHE1 bot",
		"env", cfg.Env,
		"guild_id", cfg.GuildID,
		"discord_token", cfg.RedactedToken(),
		"worker_url", cfg.WorkerURL,
		"dashboard_url", cfg.DashboardURL,
		"postgres", cfg.PostgresURL != "",
	)

	// Root context cancelled by SIGINT/SIGTERM. Everything hangs off this.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup

	// Health server comes up first so probes can observe "starting" state.
	health := &httpsrv.Health{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := httpsrv.Serve(ctx, cfg.HealthListenAddr, health); err != nil {
			slog.Error("health server stopped", "err", err)
		}
	}()

	var database *db.DB
	if cfg.PostgresURL != "" {
		database, err = db.New(ctx, cfg.PostgresURL)
		if err != nil {
			slog.Error("postgres connect failed", "err", err)
			os.Exit(1)
		}
		defer database.Close()
		slog.Info("postgres connected")
	} else {
		slog.Warn("POSTGRES_URL not set — DB-backed modules disabled")
	}

	var jobs *worker.Queue
	if cfg.WorkerURL != "" {
		jobs = worker.NewQueue(cfg.WorkerURL, cfg.WorkerAPIKey)
		defer jobs.Close()
		slog.Info("worker linked", "url", cfg.WorkerURL)
	} else {
		slog.Warn("WORKER_URL not set — Worker integration disabled")
	}

	var dash *dashboard.Client
	if cfg.DashboardURL != "" {
		dash = dashboard.New(cfg.DashboardURL, cfg.DashboardAPIKey)
		slog.Info("dashboard linked", "url", cfg.DashboardURL)
	} else {
		slog.Warn("DASHBOARD_URL not set — Dashboard integration disabled")
	}

	session, err := discordgo.New("Bot " + cfg.DiscordToken)
	if err != nil {
		slog.Error("discord session init failed", "err", err)
		os.Exit(1)
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
		slog.Error("discord open failed", "err", err)
		os.Exit(1)
	}

	if err := router.Attach(session, cfg.GuildID); err != nil {
		slog.Error("attach failed", "err", err)
		// Non-fatal: the session is open, handlers still fire. Commands
		// just may not be registered for this guild.
	}

	// Worker WS subscriber for dashboard-driven actions.
	if jobs != nil {
		wsURL := cfg.WorkerWSURL
		if wsURL == "" {
			wsURL = worker.DeriveWSURL(cfg.WorkerURL)
		}
		if wsURL != "" {
			sub := worker.NewSubscriber(wsURL, jobs)
			(&actions.Handlers{Session: session}).Register(sub)
			wg.Add(1)
			go func() {
				defer wg.Done()
				sub.Run(ctx)
			}()
			slog.Info("subscribed to worker events", "ws_url", wsURL)
		}
	}

	slog.Info("CHE1 bot online", "username", session.State.User.Username)
	health.MarkReady()

	<-ctx.Done()
	slog.Info("shutdown signal received")
	health.MarkNotReady()

	// Grace window for in-flight WS task handlers. 15s mirrors a typical
	// k8s terminationGracePeriodSeconds window minus a safety buffer.
	if err := session.Close(); err != nil {
		slog.Warn("discord close", "err", err)
	}

	waitCh := make(chan struct{})
	go func() { wg.Wait(); close(waitCh) }()
	select {
	case <-waitCh:
		slog.Info("shutdown complete")
	case <-time.After(15 * time.Second):
		slog.Warn("shutdown timed out after 15s; forcing exit")
	}
}

func setupLogger(cfg *config.Config) {
	var level slog.Level
	switch strings.ToLower(cfg.LogLevel) {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if strings.ToLower(cfg.LogFormat) == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(handler))
}
