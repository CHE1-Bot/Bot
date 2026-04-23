package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	DiscordToken    string
	GuildID         string
	PostgresURL     string
	WorkerURL       string
	WorkerWSURL     string
	WorkerAPIKey    string
	DashboardURL    string
	DashboardAPIKey string
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	c := &Config{
		DiscordToken:    os.Getenv("DISCORD_TOKEN"),
		GuildID:         os.Getenv("GUILD_ID"),
		PostgresURL:     os.Getenv("POSTGRES_URL"),
		WorkerURL:       os.Getenv("WORKER_URL"),
		WorkerWSURL:     os.Getenv("WORKER_WS_URL"),
		WorkerAPIKey:    os.Getenv("WORKER_API_KEY"),
		DashboardURL:    os.Getenv("DASHBOARD_URL"),
		DashboardAPIKey: os.Getenv("DASHBOARD_API_KEY"),
	}
	if c.DiscordToken == "" {
		return nil, fmt.Errorf("DISCORD_TOKEN is required")
	}
	if c.GuildID == "" {
		return nil, fmt.Errorf("GUILD_ID is required")
	}
	return c, nil
}
