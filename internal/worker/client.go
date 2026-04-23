package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
}

type taskRequest struct {
	Kind      string `json:"kind"`
	Input     any    `json:"input"`
	CreatedBy string `json:"created_by"`
}

type taskResponse struct {
	ID     string          `json:"id"`
	Status string          `json:"status"`
	Output json.RawMessage `json:"output"`
	Error  string          `json:"error,omitempty"`
}

func NewQueue(baseURL, apiKey string) *Queue {
	return &Queue{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		hc:      &http.Client{Timeout: 15 * time.Second},
	}
}

// Enqueue submits a task to the Worker and returns the assigned task ID.
func (q *Queue) Enqueue(ctx context.Context, jobType string, payload any) (string, error) {
	body, err := json.Marshal(taskRequest{
		Kind:      jobType,
		Input:     payload,
		CreatedBy: "bot",
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.baseURL+"/api/v1/tasks", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+q.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := q.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("worker /api/v1/tasks: %s: %s", resp.Status, string(b))
	}

	var out taskResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// WaitResult polls GET /api/v1/tasks/{id} until the task finishes or timeout.
func (q *Queue) WaitResult(ctx context.Context, taskID string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, q.baseURL+"/api/v1/tasks/"+taskID, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+q.apiKey)

		resp, err := q.hc.Do(req)
		if err != nil {
			return nil, err
		}
		var out taskResponse
		decErr := json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		if decErr != nil {
			return nil, decErr
		}

		switch out.Status {
		case "done", "completed", "success":
			return out.Output, nil
		case "failed", "error":
			return nil, fmt.Errorf("task %s failed: %s", taskID, out.Error)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-tick.C:
		}
	}
}

// Complete marks a task done from the bot side (rarely used; the Worker
// usually owns task state). Mirrors POST /api/v1/tasks/{id}/complete.
func (q *Queue) Complete(ctx context.Context, taskID string, output any, errMsg string) error {
	body, err := json.Marshal(map[string]any{"output": output, "error": errMsg})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.baseURL+"/api/v1/tasks/"+taskID+"/complete", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+q.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := q.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("worker complete: %s: %s", resp.Status, string(b))
	}
	return nil
}

func (q *Queue) Close() error { return nil }
