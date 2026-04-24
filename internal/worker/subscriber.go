package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// EventType mirrors models.EventType in the Worker repo.
type EventType string

const (
	EventTaskCreated   EventType = "task.created"
	EventTaskUpdated   EventType = "task.updated"
	EventTaskCompleted EventType = "task.completed"
)

// Event mirrors models.Event in the Worker repo.
type Event struct {
	ID        string         `json:"id"`
	Type      EventType      `json:"type"`
	Source    string         `json:"source,omitempty"`
	Subject   string         `json:"subject,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
}

// Task is the shared shape used by the Worker's REST and WS APIs.
type Task struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Status    string         `json:"status"`
	Input     map[string]any `json:"input,omitempty"`
	Result    map[string]any `json:"result,omitempty"`
	Error     string         `json:"error,omitempty"`
	CreatedBy string         `json:"created_by,omitempty"`
	CreatedAt time.Time      `json:"created_at,omitempty"`
	UpdatedAt time.Time      `json:"updated_at,omitempty"`
}

// TaskHandler runs when a task.created event arrives whose Kind matches.
// Returning non-nil error marks the task failed back to the Worker.
type TaskHandler func(ctx context.Context, t Task) (output map[string]any, err error)

// EventHandler runs for any event of a given Type (used for task.completed etc).
type EventHandler func(ctx context.Context, e Event)

// Subscriber connects to the Worker's WebSocket hub and dispatches events
// to registered handlers. After a task handler returns, the Subscriber
// reports completion back via Queue.Complete so the Dashboard sees it.
type Subscriber struct {
	url   string
	queue *Queue

	// Timings — exposed for tests; good defaults for prod.
	HandshakeTimeout time.Duration
	ReadDeadline     time.Duration
	PingInterval     time.Duration
	MaxBackoff       time.Duration
	TaskTimeout      time.Duration

	mu     sync.RWMutex
	tasks  map[string]TaskHandler
	events map[EventType][]EventHandler
}

func NewSubscriber(wsURL string, queue *Queue) *Subscriber {
	return &Subscriber{
		url:              wsURL,
		queue:            queue,
		HandshakeTimeout: 10 * time.Second,
		ReadDeadline:     70 * time.Second,
		PingInterval:     30 * time.Second,
		MaxBackoff:       30 * time.Second,
		TaskTimeout:      2 * time.Minute,
		tasks:            map[string]TaskHandler{},
		events:           map[EventType][]EventHandler{},
	}
}

func (s *Subscriber) OnTask(kind string, h TaskHandler) {
	s.mu.Lock()
	s.tasks[kind] = h
	s.mu.Unlock()
}

func (s *Subscriber) OnEvent(t EventType, h EventHandler) {
	s.mu.Lock()
	s.events[t] = append(s.events[t], h)
	s.mu.Unlock()
}

// Run blocks until ctx is cancelled, reconnecting with backoff on disconnect.
func (s *Subscriber) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		err := s.connectAndPump(ctx)
		if ctx.Err() != nil {
			return
		}
		slog.Warn("worker ws disconnected", "err", err, "retry_in", backoff.String())
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < s.MaxBackoff {
			backoff *= 2
			if backoff > s.MaxBackoff {
				backoff = s.MaxBackoff
			}
		}
	}
}

func (s *Subscriber) connectAndPump(ctx context.Context) error {
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = s.HandshakeTimeout

	c, _, err := dialer.DialContext(ctx, s.url, http.Header{})
	if err != nil {
		return err
	}
	defer c.Close()

	slog.Info("worker ws connected", "url", s.url)

	// Keepalive: read deadline extended by pong; periodic ping from writer.
	_ = c.SetReadDeadline(time.Now().Add(s.ReadDeadline))
	c.SetPongHandler(func(string) error {
		return c.SetReadDeadline(time.Now().Add(s.ReadDeadline))
	})

	// Writer goroutine: pings on interval, exits on ctx or reader failure.
	writerCtx, cancelWriter := context.WithCancel(ctx)
	defer cancelWriter()
	var writerWG sync.WaitGroup
	writerWG.Add(1)
	go func() {
		defer writerWG.Done()
		ticker := time.NewTicker(s.PingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-writerCtx.Done():
				// Best-effort close frame — peer sees a clean shutdown.
				_ = c.WriteControl(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
					time.Now().Add(time.Second),
				)
				return
			case <-ticker.C:
				if err := c.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
					return
				}
			}
		}
	}()

	for {
		var ev Event
		if err := c.ReadJSON(&ev); err != nil {
			cancelWriter()
			writerWG.Wait()
			return err
		}
		s.dispatch(ctx, ev)
	}
}

func (s *Subscriber) dispatch(ctx context.Context, e Event) {
	s.mu.RLock()
	handlers := append([]EventHandler{}, s.events[e.Type]...)
	s.mu.RUnlock()

	for _, h := range handlers {
		h := h
		go func() {
			defer recoverPanic("event handler", "type", string(e.Type))
			h(ctx, e)
		}()
	}

	if e.Type == EventTaskCreated {
		s.dispatchTask(ctx, e)
	}
}

func (s *Subscriber) dispatchTask(ctx context.Context, e Event) {
	raw, err := json.Marshal(e.Payload)
	if err != nil {
		slog.Error("marshal task payload", "err", err)
		return
	}
	var t Task
	if err := json.Unmarshal(raw, &t); err != nil {
		slog.Error("decode task payload", "err", err)
		return
	}

	s.mu.RLock()
	h, ok := s.tasks[t.Kind]
	s.mu.RUnlock()
	if !ok {
		return
	}

	go func() {
		defer recoverPanic("task handler", "kind", t.Kind, "id", t.ID)

		taskCtx, cancel := context.WithTimeout(ctx, s.TaskTimeout)
		defer cancel()

		out, hErr := h(taskCtx, t)

		if hErr != nil {
			slog.Error("task failed", "kind", t.Kind, "id", t.ID, "err", hErr)
		} else {
			slog.Info("task completed", "kind", t.Kind, "id", t.ID)
		}

		if s.queue == nil || t.ID == "" {
			return
		}
		errMsg := ""
		if hErr != nil {
			errMsg = hErr.Error()
		}
		// Use a fresh context so completion still lands if taskCtx timed out.
		completeCtx, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel2()
		if err := s.queue.Complete(completeCtx, t.ID, out, errMsg); err != nil {
			slog.Error("worker complete failed", "id", t.ID, "err", err)
		}
	}()
}

// DeriveWSURL turns the Worker HTTP base URL into the WebSocket URL using
// the Worker's default WS port (:8090) and path (/ws). Override via
// WORKER_WS_URL if your deployment differs.
func DeriveWSURL(httpURL string) string {
	httpURL = strings.TrimRight(httpURL, "/")
	u, err := url.Parse(httpURL)
	if err != nil || u.Host == "" {
		return ""
	}
	scheme := "ws"
	if u.Scheme == "https" {
		scheme = "wss"
	}
	return scheme + "://" + u.Hostname() + ":8090/ws"
}

func recoverPanic(where string, attrs ...any) {
	if r := recover(); r != nil {
		args := append([]any{"where", where, "panic", fmt.Sprint(r), "stack", string(debug.Stack())}, attrs...)
		slog.Error("recovered from panic", args...)
	}
}
