package order

import (
	"context"
	"time"
)

const (
	OutboxStatusPending   uint8 = 1
	OutboxStatusPublished uint8 = 2
)

type OutboxEvent struct {
	ID            uint64
	EventID       string
	OrderID       uint64
	AggregateKey  string
	EventType     string
	SchemaVersion int
	Payload       []byte
	Attempts      int
	CreatedAt     time.Time
}

type OutboxRepository interface {
	ClaimOutbox(ctx context.Context, workerID string, now time.Time, lease time.Duration, limit int) ([]OutboxEvent, error)
	MarkOutboxPublished(ctx context.Context, eventID uint64, workerID string, publishedAt time.Time) (bool, error)
	MarkOutboxFailed(ctx context.Context, eventID uint64, workerID string, nextAttempt time.Time, errorCode string) (bool, error)
	OutboxBacklog(ctx context.Context) (int64, error)
}
