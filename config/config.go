package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// Environment tags a runtime. Prod/staging enforce stricter validation.
type Environment string

const (
	EnvDevelopment Environment = "development"
	EnvStaging     Environment = "staging"
	EnvProduction  Environment = "production"
)

type Config struct {
	Env Environment

	DiscordToken string
	GuildID      string

	PostgresURL string

	WorkerURL    string
	WorkerWSURL  string
	WorkerAPIKey string

	DashboardURL    string
	DashboardAPIKey string

	LogLevel  string // debug|info|warn|error
	LogFormat string // json|text

	HealthListenAddr string // default :8081
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	env := Environment(strings.ToLower(firstNonEmpty(os.Getenv("APP_ENV"), os.Getenv("ENV"))))
	switch env {
	case "", EnvDevelopment, "dev":
		env = EnvDevelopment
	case EnvStaging, "stage":
		env = EnvStaging
	case EnvProduction, "prod":
		env = EnvProduction
	default:
		return nil, fmt.Errorf("APP_ENV must be development|staging|production, got %q", env)
	}

	c := &Config{
		Env:              env,
		DiscordToken:     os.Getenv("DISCORD_TOKEN"),
		GuildID:          os.Getenv("GUILD_ID"),
		PostgresURL:      os.Getenv("POSTGRES_URL"),
		WorkerURL:        strings.TrimRight(os.Getenv("WORKER_URL"), "/"),
		WorkerWSURL:      strings.TrimRight(os.Getenv("WORKER_WS_URL"), "/"),
		WorkerAPIKey:     os.Getenv("WORKER_API_KEY"),
		DashboardURL:     strings.TrimRight(os.Getenv("DASHBOARD_URL"), "/"),
		DashboardAPIKey:  os.Getenv("DASHBOARD_API_KEY"),
		LogLevel:         firstNonEmpty(os.Getenv("LOG_LEVEL"), defaultLogLevel(env)),
		LogFormat:        firstNonEmpty(os.Getenv("LOG_FORMAT"), defaultLogFormat(env)),
		HealthListenAddr: firstNonEmpty(os.Getenv("HEALTH_LISTEN_ADDR"), ":8081"),
	}

	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Config) validate() error {
	var errs []string

	if c.DiscordToken == "" {
		errs = append(errs, "DISCORD_TOKEN is required")
	}
	if c.GuildID == "" {
		errs = append(errs, "GUILD_ID is required")
	}

	if c.WorkerURL != "" {
		if err := validateHTTPURL(c.WorkerURL); err != nil {
			errs = append(errs, "WORKER_URL: "+err.Error())
		}
		if c.WorkerAPIKey == "" && c.Env != EnvDevelopment {
			errs = append(errs, "WORKER_API_KEY is required in staging/production when WORKER_URL is set")
		}
	}
	if c.WorkerWSURL != "" {
		if err := validateWSURL(c.WorkerWSURL); err != nil {
			errs = append(errs, "WORKER_WS_URL: "+err.Error())
		}
	}

	if c.DashboardURL != "" {
		if err := validateHTTPURL(c.DashboardURL); err != nil {
			errs = append(errs, "DASHBOARD_URL: "+err.Error())
		}
	}

	if c.Env == EnvProduction {
		if c.WorkerURL == "" {
			errs = append(errs, "WORKER_URL is required in production")
		}
		if c.PostgresURL == "" {
			errs = append(errs, "POSTGRES_URL is required in production")
		}
	}

	switch strings.ToLower(c.LogLevel) {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, "LOG_LEVEL must be debug|info|warn|error")
	}
	switch strings.ToLower(c.LogFormat) {
	case "json", "text":
	default:
		errs = append(errs, "LOG_FORMAT must be json|text")
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// RedactedToken returns a log-safe fingerprint of the Discord token.
func (c *Config) RedactedToken() string {
	t := c.DiscordToken
	if len(t) <= 8 {
		return "***"
	}
	return t[:4] + "..." + t[len(t)-4:]
}

func validateHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("must use http(s) scheme, got %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("missing host")
	}
	return nil
}

func validateWSURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "ws" && u.Scheme != "wss" {
		return fmt.Errorf("must use ws(s) scheme, got %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("missing host")
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func defaultLogLevel(env Environment) string {
	if env == EnvDevelopment {
		return "debug"
	}
	return "info"
}

func defaultLogFormat(env Environment) string {
	if env == EnvDevelopment {
		return "text"
	}
	return "json"
}
