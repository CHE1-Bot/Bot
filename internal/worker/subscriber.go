package worker

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
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

// Task is what the Worker stuffs into Event.Payload for task.* events.
type Task struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Status    string         `json:"status"`
	Input     map[string]any `json:"input,omitempty"`
	Result    map[string]any `json:"result,omitempty"`
	Error     string         `json:"error,omitempty"`
	CreatedBy string         `json:"created_by,omitempty"`
}

// TaskHandler runs when a task.created event arrives whose Kind matches.
// Returning non-nil error marks the task failed back to the Worker.
type TaskHandler func(ctx context.Context, t Task) (output map[string]any, err error)

// EventHandler runs for any event of a given Type (used for settings.updated etc).
type EventHandler func(ctx context.Context, e Event)

// Subscriber connects to the Worker's WebSocket hub and dispatches events
// to registered handlers. After a task handler returns, the Subscriber
// reports completion back via Queue.Complete so the Dashboard sees it.
type Subscriber struct {
	url   string
	queue *Queue

	mu      sync.RWMutex
	tasks   map[string]TaskHandler
	events  map[EventType][]EventHandler
}

func NewSubscriber(wsURL string, queue *Queue) *Subscriber {
	return &Subscriber{
		url:    wsURL,
		queue:  queue,
		tasks:  map[string]TaskHandler{},
		events: map[EventType][]EventHandler{},
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
		log.Printf("worker ws disconnected: %v (retry in %s)", err, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (s *Subscriber) connectAndPump(ctx context.Context) error {
	c, _, err := websocket.DefaultDialer.DialContext(ctx, s.url, http.Header{})
	if err != nil {
		return err
	}
	defer c.Close()
	log.Printf("worker ws connected: %s", s.url)

	for {
		var ev Event
		if err := c.ReadJSON(&ev); err != nil {
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
		go h(ctx, e)
	}

	if e.Type == EventTaskCreated {
		s.dispatchTask(ctx, e)
	}
}

func (s *Subscriber) dispatchTask(ctx context.Context, e Event) {
	raw, err := json.Marshal(e.Payload)
	if err != nil {
		return
	}
	var t Task
	if err := json.Unmarshal(raw, &t); err != nil {
		return
	}

	s.mu.RLock()
	h, ok := s.tasks[t.Kind]
	s.mu.RUnlock()
	if !ok {
		return
	}

	go func() {
		out, hErr := h(ctx, t)
		if s.queue == nil || t.ID == "" {
			if hErr != nil {
				log.Printf("task %s (%s): %v", t.ID, t.Kind, hErr)
			}
			return
		}
		errMsg := ""
		if hErr != nil {
			errMsg = hErr.Error()
		}
		if err := s.queue.Complete(ctx, t.ID, out, errMsg); err != nil {
			log.Printf("worker complete %s: %v", t.ID, err)
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
