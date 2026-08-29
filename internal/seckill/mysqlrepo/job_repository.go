package mysqlrepo

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"service_rpc/internal/order"
	ordermysql "service_rpc/internal/order/mysqlrepo"
	"service_rpc/internal/seckill"
)

const maxJobErrorCodeLength = 64

type JobStats struct {
	Pending         int64
	Published       int64
	Succeeded       int64
	Failed          int64
	OldestPendingAt time.Time
	PublishAttempts uint64
	ConsumeAttempts uint64
}

var _ seckill.JobRepository = (*Repository)(nil)

func (r *Repository) EnsureJob(ctx context.Context, input seckill.EnsureJobInput) (seckill.OrderJob, bool, error) {
	input.EventID = strings.TrimSpace(input.EventID)
	input.OrderNo = strings.TrimSpace(input.OrderNo)
	input.ReservedAt = input.ReservedAt.UTC().Truncate(time.Microsecond)
	if input.EventID == "" || input.OrderNo == "" || input.UserID == 0 || input.ItemID == 0 || input.ReservedAt.IsZero() || !json.Valid(input.Payload) {
		return seckill.OrderJob{}, false, seckill.ErrInvalidArgument
	}

	// LAST_INSERT_ID(id) 让“首次插入”和“唯一键冲突后读取赢家”都获得同一个主键。
	// 不能先 SELECT 再 INSERT：两个 HTTP 重试可同时查不到，最终仍必须由 event_id/order_no
	// 唯一索引选出唯一赢家。自增 ID 出现空洞是可接受的，ID 只用于内部扫描，不承载业务连续性。
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO seckill_order_jobs
		    (event_id, order_no, user_id, seckill_item_id, reserved_at, payload, status, next_publish_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP(6))
		ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(id)
	`, input.EventID, input.OrderNo, input.UserID, input.ItemID, input.ReservedAt, input.Payload, seckill.JobStatusPendingPublish)
	if err != nil {
		return seckill.OrderJob{}, false, fmt.Errorf("ensure seckill order job: %w", err)
	}
	jobID, err := result.LastInsertId()
	if err != nil || jobID <= 0 {
		return seckill.OrderJob{}, false, fmt.Errorf("read seckill order job ID: %w", err)
	}
	job, err := r.findJobByID(ctx, uint64(jobID))
	if err != nil {
		return seckill.OrderJob{}, false, err
	}
	// 两个不同请求若意外复用 event_id/order_no，不能把“唯一键冲突”误当幂等成功。
	// 身份漂移会让一个用户查询到另一个用户的任务，是比返回 500 更严重的越权风险。
	if job.EventID != input.EventID || job.OrderNo != input.OrderNo || job.UserID != input.UserID || job.ItemID != input.ItemID || !job.ReservedAt.Equal(input.ReservedAt) || !equalJSON(job.Payload, input.Payload) {
		return seckill.OrderJob{}, false, seckill.ErrJobConflict
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return seckill.OrderJob{}, false, fmt.Errorf("read seckill job affected rows: %w", err)
	}
	return job, rows == 0, nil
}

func equalJSON(left, right []byte) bool {
	// MySQL JSON 会重排对象 key 并插入规范化空白，因此不能比较原始字节。这里用
	// UseNumber 防止大整数先转 float64 丢精度：event/user/item 任一数字变化都必须仍能
	// 被识别为身份漂移。面试点：JSON 对象的语义不包含 key 顺序，序列化文本却包含。
	decode := func(value []byte) (any, bool) {
		decoder := json.NewDecoder(bytes.NewReader(value))
		decoder.UseNumber()
		var result any
		if err := decoder.Decode(&result); err != nil {
			return nil, false
		}
		return result, true
	}
	leftValue, leftOK := decode(left)
	rightValue, rightOK := decode(right)
	return leftOK && rightOK && reflect.DeepEqual(leftValue, rightValue)
}

func (r *Repository) FindJobOwned(ctx context.Context, userID uint64, orderNo string) (seckill.OrderJob, error) {
	if userID == 0 || strings.TrimSpace(orderNo) == "" {
		return seckill.OrderJob{}, seckill.ErrJobNotFound
	}
	return scanJob(r.db.QueryRowContext(ctx, jobSelect+` WHERE user_id = ? AND order_no = ?`, userID, strings.TrimSpace(orderNo)))
}

func (r *Repository) FindOrderOwned(ctx context.Context, userID uint64, orderNo string) (order.Order, error) {
	if userID == 0 || strings.TrimSpace(orderNo) == "" {
		return order.Order{}, seckill.ErrJobNotFound
	}
	var orderID uint64
	err := r.db.QueryRowContext(ctx, `SELECT id FROM orders WHERE user_id = ? AND order_no = ?`, userID, strings.TrimSpace(orderNo)).Scan(&orderID)
	if errors.Is(err, sql.ErrNoRows) {
		return order.Order{}, seckill.ErrJobNotFound
	}
	if err != nil {
		return order.Order{}, fmt.Errorf("find async seckill order ID: %w", err)
	}
	created, err := ordermysql.New(r.db).FindOwned(ctx, userID, orderID)
	if err != nil {
		return order.Order{}, fmt.Errorf("find async seckill order: %w", err)
	}
	return created, nil
}

func (r *Repository) ListPendingJobs(ctx context.Context, now time.Time, limit int) ([]seckill.OrderJob, error) {
	if now.IsZero() || limit <= 0 || limit > 1000 {
		return nil, seckill.ErrInvalidArgument
	}
	rows, err := r.db.QueryContext(ctx, jobSelect+`
		WHERE status = ? AND next_publish_at <= ?
		ORDER BY next_publish_at, id
		LIMIT ?
	`, seckill.JobStatusPendingPublish, now.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("list pending seckill jobs: %w", err)
	}
	defer rows.Close()
	jobs := make([]seckill.OrderJob, 0)
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending seckill jobs: %w", err)
	}
	return jobs, nil
}

func (r *Repository) MarkJobPublished(ctx context.Context, jobID uint64, at time.Time) (bool, error) {
	return r.updateJobState(ctx, `
		UPDATE seckill_order_jobs
		SET status = ?, publish_attempts = publish_attempts + 1, published_at = ?, last_error_code = ''
		WHERE id = ? AND status = ?
	`, seckill.JobStatusPublished, at.UTC(), jobID, seckill.JobStatusPendingPublish)
}

func (r *Repository) ScheduleJobPublishRetry(ctx context.Context, jobID uint64, next time.Time, errorCode string) (bool, error) {
	code, err := normalizeJobErrorCode(errorCode)
	if err != nil || jobID == 0 || next.IsZero() {
		return false, seckill.ErrInvalidArgument
	}
	return r.updateJobState(ctx, `
		UPDATE seckill_order_jobs
		SET publish_attempts = publish_attempts + 1, next_publish_at = ?, last_error_code = ?
		WHERE id = ? AND status = ?
	`, next.UTC(), code, jobID, seckill.JobStatusPendingPublish)
}

func (r *Repository) MarkJobSucceeded(ctx context.Context, jobID uint64, at time.Time) (bool, error) {
	// 允许从 PENDING 直接成功：relay 已获得 broker ack、但尚未来得及标记 PUBLISHED 时，
	// consumer 可能已经完成订单。迟到的 MarkJobPublished 使用条件更新，不能把终态覆盖回中间态。
	return r.updateJobState(ctx, `
		UPDATE seckill_order_jobs
		SET status = ?, consume_attempts = consume_attempts + 1, completed_at = ?, last_error_code = ''
		WHERE id = ? AND status IN (?, ?)
	`, seckill.JobStatusSucceeded, at.UTC(), jobID, seckill.JobStatusPendingPublish, seckill.JobStatusPublished)
}

func (r *Repository) MarkJobFailed(ctx context.Context, jobID uint64, at time.Time, errorCode string) (bool, error) {
	code, err := normalizeJobErrorCode(errorCode)
	if err != nil || jobID == 0 || at.IsZero() {
		return false, seckill.ErrInvalidArgument
	}
	return r.updateJobState(ctx, `
		UPDATE seckill_order_jobs
		SET status = ?, consume_attempts = consume_attempts + 1, completed_at = ?, last_error_code = ?
		WHERE id = ? AND status IN (?, ?)
	`, seckill.JobStatusFailed, at.UTC(), code, jobID, seckill.JobStatusPendingPublish, seckill.JobStatusPublished)
}

func (r *Repository) updateJobState(ctx context.Context, query string, args ...any) (bool, error) {
	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("update seckill order job state: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read seckill job update count: %w", err)
	}
	return rows == 1, nil
}

func (r *Repository) findJobByID(ctx context.Context, jobID uint64) (seckill.OrderJob, error) {
	return scanJob(r.db.QueryRowContext(ctx, jobSelect+` WHERE id = ?`, jobID))
}

const jobSelect = `
	SELECT id, event_id, order_no, user_id, seckill_item_id, reserved_at, payload, status,
	       publish_attempts, consume_attempts, next_publish_at, last_error_code,
	       published_at, completed_at, created_at, updated_at
	FROM seckill_order_jobs
`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(row rowScanner) (seckill.OrderJob, error) {
	var job seckill.OrderJob
	var publishedAt, completedAt sql.NullTime
	err := row.Scan(
		&job.ID, &job.EventID, &job.OrderNo, &job.UserID, &job.ItemID, &job.ReservedAt, &job.Payload, &job.Status,
		&job.PublishAttempts, &job.ConsumeAttempts, &job.NextPublishAt, &job.LastErrorCode,
		&publishedAt, &completedAt, &job.CreatedAt, &job.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return seckill.OrderJob{}, seckill.ErrJobNotFound
	}
	if err != nil {
		return seckill.OrderJob{}, fmt.Errorf("scan seckill order job: %w", err)
	}
	job.ReservedAt = job.ReservedAt.UTC()
	job.NextPublishAt = job.NextPublishAt.UTC()
	job.CreatedAt = job.CreatedAt.UTC()
	job.UpdatedAt = job.UpdatedAt.UTC()
	if publishedAt.Valid {
		job.PublishedAt = publishedAt.Time.UTC()
	}
	if completedAt.Valid {
		job.CompletedAt = completedAt.Time.UTC()
	}
	return job, nil
}

func normalizeJobErrorCode(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxJobErrorCodeLength {
		return "", seckill.ErrInvalidArgument
	}
	for _, ch := range value {
		if !(ch == '_' || ch == '-' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9') {
			return "", seckill.ErrInvalidArgument
		}
	}
	return value, nil
}

func (r *Repository) InspectJobStats(ctx context.Context) (JobStats, error) {
	var stats JobStats
	var oldest sql.NullTime
	err := r.db.QueryRowContext(ctx, `
		SELECT
		  COALESCE(SUM(status = 1), 0), COALESCE(SUM(status = 2), 0),
		  COALESCE(SUM(status = 3), 0), COALESCE(SUM(status = 4), 0),
		  MIN(CASE WHEN status = 1 THEN created_at END),
		  COALESCE(SUM(publish_attempts), 0), COALESCE(SUM(consume_attempts), 0)
		FROM seckill_order_jobs
	`).Scan(&stats.Pending, &stats.Published, &stats.Succeeded, &stats.Failed, &oldest, &stats.PublishAttempts, &stats.ConsumeAttempts)
	if err != nil {
		return JobStats{}, fmt.Errorf("inspect seckill job stats: %w", err)
	}
	if oldest.Valid {
		stats.OldestPendingAt = oldest.Time.UTC()
	}
	return stats, nil
}
