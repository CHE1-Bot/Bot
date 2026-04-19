package worker

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
)

// Job names shared with the Worker repo.
const (
	JobTicketTranscript = "ticket.transcript"
	JobLevelCard        = "level.card"
	JobGiveawayTimer    = "giveaway.timer"
)

// Queue pushes jobs onto Redis streams consumed by the Worker service.
// The Worker repo reads from the same stream key and dispatches by JobType.
type Queue struct {
	rdb    *redis.Client
	stream string
}

type Job struct {
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	EnqueuedAt time.Time      `json:"enqueued_at"`
}

func NewQueue(addr, password string) *Queue {
	return &Queue{
		rdb: redis.NewClient(&redis.Options{
			Addr:     addr,
			Password: password,
		}),
		stream: "che1:jobs",
	}
}

func (q *Queue) Enqueue(ctx context.Context, jobType string, payload any) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(Job{
		Type:       jobType,
		Payload:    raw,
		EnqueuedAt: time.Now().UTC(),
	})
	if err != nil {
		return "", err
	}
	return q.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: q.stream,
		Values: map[string]any{"job": body},
	}).Result()
}

// WaitResult blocks on a per-job response key published by the Worker.
func (q *Queue) WaitResult(ctx context.Context, jobID string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res, err := q.rdb.BLPop(ctx, timeout, "che1:result:"+jobID).Result()
	if err != nil {
		return nil, err
	}
	return []byte(res[1]), nil
}

func (q *Queue) Close() error { return q.rdb.Close() }
