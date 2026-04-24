package worker

import (
	"bytes"
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

// Job kinds shared with the Worker repo (CHE1-Bot/Worker).
// Sent as the `kind` field in POST /api/v1/tasks.
const (
	JobTicketTranscript = "ticket.transcript"
	JobLevelCard        = "level.card"
	JobGiveawayTimer    = "giveaway.timer"
)

// Queue is the bot-side client for the Worker REST API
// (https://github.com/CHE1-Bot/Worker). Every action the bot wants the
// Worker to perform is submitted as a task via POST /api/v1/tasks.
type Queue struct {
	baseURL string
	apiKey  string
	hc      *http.Client

	maxRetries int
	retryBase  time.Duration
}

type createTaskRequest struct {
	Kind      string `json:"kind"`
	Input     any    `json:"input"`
	CreatedBy string `json:"created_by"`
}

func NewQueue(baseURL, apiKey string) *Queue {
	return &Queue{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		hc:         &http.Client{Timeout: 15 * time.Second},
		maxRetries: 3,
		retryBase:  200 * time.Millisecond,
	}
}

// Enqueue submits a task to the Worker and returns the assigned task ID.
func (q *Queue) Enqueue(ctx context.Context, jobType string, payload any) (string, error) {
	body, err := json.Marshal(createTaskRequest{
		Kind:      jobType,
		Input:     payload,
		CreatedBy: "bot",
	})
	if err != nil {
		return "", err
	}

	var out Task
	if err := q.doJSON(ctx, http.MethodPost, "/api/v1/tasks", body, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// WaitResult polls GET /api/v1/tasks/{id} until the task finishes or timeout.
// Returns the task's result map encoded as JSON.
func (q *Queue) WaitResult(ctx context.Context, taskID string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	for {
		var t Task
		if err := q.doJSON(ctx, http.MethodGet, "/api/v1/tasks/"+taskID, nil, &t); err != nil {
			return nil, err
		}

		switch t.Status {
		case "succeeded", "done", "completed", "success":
			return json.Marshal(t.Result)
		case "failed", "error":
			return nil, fmt.Errorf("task %s failed: %s", taskID, t.Error)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-tick.C:
		}
	}
}

// Complete reports the task's outcome back to the Worker. The Worker then
// emits task.completed, which is how the Dashboard observes bot-driven
// actions finishing.
func (q *Queue) Complete(ctx context.Context, taskID string, output any, errMsg string) error {
	body, err := json.Marshal(map[string]any{"result": output, "error": errMsg})
	if err != nil {
		return err
	}
	return q.doJSON(ctx, http.MethodPost, "/api/v1/tasks/"+taskID+"/complete", body, nil)
}

func (q *Queue) Close() error { return nil }

// doJSON performs one REST call with retries on network errors and 5xx.
// 4xx responses are returned immediately (client error, no retry).
func (q *Queue) doJSON(ctx context.Context, method, path string, body []byte, out any) error {
	url := q.baseURL + path

	var lastErr error
	for attempt := 0; attempt <= q.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := q.retryBase * (1 << (attempt - 1))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, reader)
		if err != nil {
			return err
		}
		if q.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+q.apiKey)
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := q.hc.Do(req)
		if err != nil {
			lastErr = err
			slog.Warn("worker request failed, retrying", "method", method, "path", path, "attempt", attempt+1, "err", err)
			continue
		}

		if resp.StatusCode >= 500 {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("worker %s %s: %s: %s", method, path, resp.Status, string(b))
			slog.Warn("worker 5xx, retrying", "method", method, "path", path, "status", resp.Status, "attempt", attempt+1)
			continue
		}

		if resp.StatusCode >= 400 {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return fmt.Errorf("worker %s %s: %s: %s", method, path, resp.Status, string(b))
		}

		if out != nil {
			err = json.NewDecoder(resp.Body).Decode(out)
			resp.Body.Close()
			if err != nil {
				return fmt.Errorf("decode %s %s: %w", method, path, err)
			}
		} else {
			resp.Body.Close()
		}
		return nil
	}

	if lastErr == nil {
		lastErr = errors.New("worker request failed")
	}
	return fmt.Errorf("worker %s %s after %d attempts: %w", method, path, q.maxRetries+1, lastErr)
}
