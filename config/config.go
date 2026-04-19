package config

import (
	"fmt"
	"os"
)

type Config struct {
	DiscordToken string
	PostgresURL  string
	RedisAddr    string
	RedisPass    string
	GuildID      string
}

func Load() (*Config, error) {
	c := &Config{
		DiscordToken: os.Getenv("DISCORD_TOKEN"),
		PostgresURL:  os.Getenv("POSTGRES_URL"),
		RedisAddr:    os.Getenv("REDIS_ADDR"),
		RedisPass:    os.Getenv("REDIS_PASSWORD"),
		GuildID:      os.Getenv("GUILD_ID"),
	}
	if c.DiscordToken == "" {
		return nil, fmt.Errorf("DISCORD_TOKEN is required")
	}
	if c.PostgresURL == "" {
		return nil, fmt.Errorf("POSTGRES_URL is required")
	}
	if c.RedisAddr == "" {
		c.RedisAddr = "localhost:6379"
	}
	return c, nil
}
