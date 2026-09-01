package mysqlrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"service_rpc/internal/seckill"
)

// ActivityRepository 只访问 seckill_activities。不要复用旧 Repository：旧类型还包含
// v0.4.2 单体时代的订单事务和商品 JOIN，把它装入 seckill-rpc 会让进程边界名存实亡。
type ActivityRepository struct{ db *sql.DB }

func NewActivityRepository(db *sql.DB) *ActivityRepository { return &ActivityRepository{db: db} }

func (r *ActivityRepository) CreateActivity(ctx context.Context, input seckill.CreateActivityInput) (seckill.Activity, error) {
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO seckill_activities (name, start_at, end_at, status, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, input.Name, input.StartAt, input.EndAt, seckill.StatusDisabled, createdAt)
	if err != nil {
		return seckill.Activity{}, fmt.Errorf("insert seckill activity: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return seckill.Activity{}, fmt.Errorf("read seckill activity ID: %w", err)
	}
	return seckill.Activity{ID: uint64(id), Name: input.Name, StartAt: input.StartAt, EndAt: input.EndAt, Status: seckill.StatusDisabled, CreatedAt: createdAt}, nil
}

func (r *ActivityRepository) FindActivity(ctx context.Context, activityID uint64) (seckill.Activity, error) {
	var activity seckill.Activity
	err := r.db.QueryRowContext(ctx, `
		SELECT id, name, start_at, end_at, status, created_at
		FROM seckill_activities WHERE id = ?
	`, activityID).Scan(&activity.ID, &activity.Name, &activity.StartAt, &activity.EndAt, &activity.Status, &activity.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return seckill.Activity{}, seckill.ErrActivityNotFound
	}
	if err != nil {
		return seckill.Activity{}, fmt.Errorf("find seckill activity: %w", err)
	}
	return activity, nil
}

func (r *ActivityRepository) SetActivityStatus(ctx context.Context, activityID uint64, status uint8) error {
	result, err := r.db.ExecContext(ctx, `UPDATE seckill_activities SET status = ? WHERE id = ?`, status, activityID)
	if err != nil {
		return fmt.Errorf("set seckill activity status: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read seckill activity update count: %w", err)
	}
	if rows > 0 {
		return nil
	}
	// MySQL 默认把“更新成原值”记为 0 affected rows，必须再查存在性，不能误报 404。
	if _, err := r.FindActivity(ctx, activityID); err != nil {
		return err
	}
	return nil
}

func (r *ActivityRepository) ListActiveActivityIDs(ctx context.Context, now time.Time) ([]uint64, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id FROM seckill_activities
		WHERE status = ? AND end_at > ?
		ORDER BY id
	`, seckill.StatusEnabled, now.UTC())
	if err != nil {
		return nil, fmt.Errorf("list active seckill activity IDs: %w", err)
	}
	defer rows.Close()
	ids := make([]uint64, 0)
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan active seckill activity ID: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active seckill activity IDs: %w", err)
	}
	return ids, nil
}

var _ seckill.ActivityRepository = (*ActivityRepository)(nil)
