// Package dashboard is a thin HTTP client for the CHE1 Dashboard BFF
// (https://github.com/CHE1-Bot/Dashboard). The Dashboard mostly fronts
// browser sessions (cookie-auth), but exposes a small set of endpoints
// the bot uses to look up per-guild config (ticket panels, application
// forms, leveling rewards, etc.) without touching Postgres directly.
package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	apiKey  string
	hc      *http.Client

	maxRetries int
	retryBase  time.Duration
}

func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		hc:         &http.Client{Timeout: 10 * time.Second},
		maxRetries: 2,
		retryBase:  200 * time.Millisecond,
	}
}

// Get fetches a JSON resource from the Dashboard API and decodes it into out.
// path must include a leading slash, e.g. "/api/guilds/123/leveling/settings".
func (c *Client) Get(ctx context.Context, path string, out any) error {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := c.retryBase * (1 << (attempt - 1))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
		if err != nil {
			return err
		}
		if c.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
		}

		resp, err := c.hc.Do(req)
		if err != nil {
			lastErr = err
			slog.Warn("dashboard request failed, retrying", "path", path, "attempt", attempt+1, "err", err)
			continue
		}

		if resp.StatusCode >= 500 {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("dashboard GET %s: %s: %s", path, resp.Status, string(b))
			slog.Warn("dashboard 5xx, retrying", "path", path, "status", resp.Status, "attempt", attempt+1)
			continue
		}

		if resp.StatusCode >= 400 {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return fmt.Errorf("dashboard GET %s: %s: %s", path, resp.Status, string(b))
		}

		defer resp.Body.Close()
		if out == nil {
			return nil
		}
		return json.NewDecoder(resp.Body).Decode(out)
	}

	if lastErr == nil {
		lastErr = errors.New("dashboard request failed")
	}
	return fmt.Errorf("dashboard GET %s after %d attempts: %w", path, c.maxRetries+1, lastErr)
}

// ApplicationForm matches the Dashboard's application form shape.
type ApplicationForm struct {
	ID    string `json:"id"`
	Role  string `json:"role"`
	URL   string `json:"url"`
	Title string `json:"title"`
}

// ApplicationForms returns the configured application forms for a guild.
func (c *Client) ApplicationForms(ctx context.Context, guildID string) ([]ApplicationForm, error) {
	var out []ApplicationForm
	if err := c.Get(ctx, "/api/guilds/"+guildID+"/applications/forms", &out); err != nil {
		return nil, err
	}
	return out, nil
}
