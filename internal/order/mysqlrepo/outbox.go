package mysqlrepo

import (
	"context"
	"fmt"
	"strings"
	"time"

	"service_rpc/internal/order"
)

func (r *Repository) ClaimOutbox(ctx context.Context, workerID string, now time.Time, lease time.Duration, limit int) ([]order.OutboxEvent, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" || now.IsZero() || lease <= 0 || limit <= 0 || limit > 1000 {
		return nil, order.ErrInvalidOrder
	}
	now = now.UTC().Truncate(time.Millisecond)
	claimUntil := now.Add(lease).UTC().Truncate(time.Millisecond)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin order outbox claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	// SKIP LOCKED 让多个 relay 各取不同事件，不等待另一个批次的行锁。
	// 锁只覆盖“选 ID + 写短租约”，网络发布绝不能放在事务内，否则慢 broker 会占满 DB 连接。
	rows, err := tx.QueryContext(ctx, `
		SELECT id, event_id, order_id, aggregate_key, event_type, schema_version,
		       CAST(payload AS CHAR), attempts, created_at
		FROM order_outbox_events
		WHERE status = ? AND next_attempt_at <= ? AND (claim_until IS NULL OR claim_until <= ?)
		ORDER BY id
		LIMIT ?
		FOR UPDATE SKIP LOCKED
	`, order.OutboxStatusPending, now, now, limit)
	if err != nil {
		return nil, fmt.Errorf("select due order outbox: %w", err)
	}
	events := make([]order.OutboxEvent, 0, limit)
	for rows.Next() {
		var event order.OutboxEvent
		if err := rows.Scan(&event.ID, &event.EventID, &event.OrderID, &event.AggregateKey, &event.EventType, &event.SchemaVersion, &event.Payload, &event.Attempts, &event.CreatedAt); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan order outbox: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close order outbox rows: %w", err)
	}
	if len(events) > 0 {
		ids := make([]any, 0, len(events)+2)
		ids = append(ids, workerID, claimUntil)
		placeholders := make([]string, 0, len(events))
		for _, event := range events {
			placeholders = append(placeholders, "?")
			ids = append(ids, event.ID)
		}
		query := `UPDATE order_outbox_events SET claimed_by = ?, claim_until = ? WHERE id IN (` + strings.Join(placeholders, ",") + `)`
		if _, err := tx.ExecContext(ctx, query, ids...); err != nil {
			return nil, fmt.Errorf("lease order outbox batch: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit order outbox claim: %w", err)
	}
	return events, nil
}

func (r *Repository) MarkOutboxPublished(ctx context.Context, eventID uint64, workerID string, publishedAt time.Time) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE order_outbox_events
		SET status = ?, published_at = ?, claimed_by = NULL, claim_until = NULL, last_error_code = NULL
		WHERE id = ? AND status = ? AND claimed_by = ?
	`, order.OutboxStatusPublished, publishedAt.UTC().Truncate(time.Millisecond), eventID, order.OutboxStatusPending, strings.TrimSpace(workerID))
	if err != nil {
		return false, fmt.Errorf("mark order outbox published: %w", err)
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (r *Repository) MarkOutboxFailed(ctx context.Context, eventID uint64, workerID string, nextAttempt time.Time, errorCode string) (bool, error) {
	// 只保存稳定分类（如 BROKER_UNAVAILABLE），不保存地址、认证信息或随版本变化的原始错误文本。
	errorCode = strings.TrimSpace(errorCode)
	if errorCode == "" || len(errorCode) > 64 {
		return false, order.ErrInvalidOrder
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE order_outbox_events
		SET attempts = attempts + 1, next_attempt_at = ?, last_error_code = ?, claimed_by = NULL, claim_until = NULL
		WHERE id = ? AND status = ? AND claimed_by = ?
	`, nextAttempt.UTC().Truncate(time.Millisecond), errorCode, eventID, order.OutboxStatusPending, strings.TrimSpace(workerID))
	if err != nil {
		return false, fmt.Errorf("mark order outbox failed: %w", err)
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (r *Repository) OutboxBacklog(ctx context.Context) (int64, error) {
	var count int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM order_outbox_events WHERE status = ?`, order.OutboxStatusPending).Scan(&count); err != nil {
		return 0, fmt.Errorf("count order outbox backlog: %w", err)
	}
	return count, nil
}

var _ order.OutboxRepository = (*Repository)(nil)
