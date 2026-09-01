// Package mysqlrepo persists notification facts and consumption ledger in one local transaction.
package mysqlrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	gomysql "github.com/go-sql-driver/mysql"

	"service_rpc/internal/notification"
	"service_rpc/internal/order/events"
)

type Repository struct{ db *sql.DB }

func New(db *sql.DB) *Repository { return &Repository{db: db} }

func (r *Repository) ConsumeOrderCreated(ctx context.Context, event events.OrderCreatedV1) (bool, error) {
	title, body := notification.ContentFromOrder(event)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin notification consumption: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	createdAt := events.CreatedAt(event).Truncate(time.Microsecond)
	result, err := tx.ExecContext(ctx, `
		INSERT INTO notifications (user_id, business_type, title, body, order_no, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, event.UserID, notification.BusinessTypeOrderCreated, title, body, event.OrderNo, createdAt)
	if err != nil {
		return false, fmt.Errorf("insert notification: %w", err)
	}
	notificationID, err := result.LastInsertId()
	if err != nil {
		return false, fmt.Errorf("read notification ID: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO notification_consumptions (event_id, notification_id, consumed_at)
		VALUES (?, ?, ?)
	`, event.EventID, notificationID, time.Now().UTC().Truncate(time.Microsecond))
	if isDuplicate(err) {
		// 两个 consumer 可能都先插入通知，但 event_id 唯一键只允许一个事务成为赢家；
		// 失败事务整体回滚，因此不会留下“没有 ledger 的孤儿通知”。Kafka offset 不是幂等边界。
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return false, fmt.Errorf("rollback duplicate notification: %w", rollbackErr)
		}
		var exists int
		if findErr := r.db.QueryRowContext(ctx, "SELECT 1 FROM notification_consumptions WHERE event_id = ?", event.EventID).Scan(&exists); findErr != nil {
			return false, fmt.Errorf("load duplicate notification consumption: %w", findErr)
		}
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("insert notification consumption: %w", err)
	}
	if err := tx.Commit(); err != nil {
		// Commit 结果未知时让 Kafka 重投；稳定 event_id 会在下一次调用收敛，不能在这里提交 offset。
		return false, fmt.Errorf("commit notification consumption: %w", err)
	}
	return true, nil
}

func (r *Repository) ListOwned(ctx context.Context, userID uint64, page, pageSize int) (notification.Page, error) {
	var total int64
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM notifications WHERE user_id = ?", userID).Scan(&total); err != nil {
		return notification.Page{}, fmt.Errorf("count owned notifications: %w", err)
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, user_id, business_type, title, body, order_no, created_at, read_at
		FROM notifications WHERE user_id = ? ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?
	`, userID, pageSize, (page-1)*pageSize)
	if err != nil {
		return notification.Page{}, fmt.Errorf("list owned notifications: %w", err)
	}
	defer rows.Close()
	items := make([]notification.Notification, 0, pageSize)
	for rows.Next() {
		var value notification.Notification
		var readAt sql.NullTime
		if err := rows.Scan(&value.ID, &value.UserID, &value.BusinessType, &value.Title, &value.Body, &value.OrderNo, &value.CreatedAt, &readAt); err != nil {
			return notification.Page{}, fmt.Errorf("scan notification: %w", err)
		}
		if readAt.Valid {
			read := readAt.Time.UTC()
			value.ReadAt = &read
		}
		items = append(items, value)
	}
	if err := rows.Err(); err != nil {
		return notification.Page{}, fmt.Errorf("iterate notifications: %w", err)
	}
	return notification.Page{Items: items, Page: page, PageSize: pageSize, Total: total}, nil
}

func (r *Repository) MarkReadOwned(ctx context.Context, userID, notificationID uint64, readAt time.Time) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE notifications SET read_at = COALESCE(read_at, ?) WHERE id = ? AND user_id = ?
	`, readAt, notificationID, userID)
	if err != nil {
		return fmt.Errorf("mark owned notification read: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read notification update count: %w", err)
	}
	if rows == 0 {
		// 对“不存在”和“属于别人”统一返回 NotFound，避免枚举其他用户的通知 ID。
		return notification.ErrNotificationNotFound
	}
	return nil
}

func isDuplicate(err error) bool {
	var mysqlError *gomysql.MySQLError
	return errors.As(err, &mysqlError) && mysqlError.Number == 1062
}

var _ notification.Repository = (*Repository)(nil)
