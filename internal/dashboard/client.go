// Package dashboard is a thin HTTP client for the CHE1 Dashboard BFF
// (https://github.com/CHE1-Bot/Dashboard). The Dashboard mostly fronts
// browser sessions (cookie-auth), but exposes a small set of endpoints
// the bot uses to look up per-guild config (ticket panels, application
// forms, leveling rewards, etc.) without touching Postgres directly.
package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	apiKey  string
	hc      *http.Client
}

func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		hc:      &http.Client{Timeout: 10 * time.Second},
	}
}

// Get fetches a JSON resource from the Dashboard API and decodes it into out.
// path must include a leading slash, e.g. "/api/guilds/123/leveling/settings".
func (c *Client) Get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("dashboard GET %s: %s: %s", path, resp.Status, string(b))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
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
