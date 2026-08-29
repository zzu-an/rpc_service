package streamqueue

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"

	"service_rpc/internal/config"
	"service_rpc/internal/platform/database"
	"service_rpc/internal/seckill"
	seckillmysql "service_rpc/internal/seckill/mysqlrepo"
	"service_rpc/internal/seckill/redisgate"
)

// TestV042StageStreamToMySQL 是 v0.4.2 的真实依赖验收。它故意把同一业务任务复制成
// 100 条 Stream entry，证明 at-least-once 重投最终由 MySQL 唯一约束收敛，而不是依赖
// 单进程内存去重。普通 go test 跳过；verify-v042 必须显式开启且缺环境即失败。
func TestV042StageStreamToMySQL(t *testing.T) {
	if os.Getenv("V042_STAGE_VERIFY") != "1" {
		t.Skip("V042_STAGE_VERIFY is not enabled")
	}
	dsn := os.Getenv("TEST_DSN")
	if dsn == "" {
		t.Fatal("TEST_DSN is required for v0.4.2 stage verification")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := database.OpenMySQL(ctx, config.MySQLConfig{
		DataSource: dsn, MaxOpenConns: 24, MaxIdleConns: 12, ConnMaxLifetimeSeconds: 60,
	})
	if err != nil {
		t.Fatalf("open stage MySQL: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	fixture := seedV042Fixture(t, ctx, db)

	client := streamIntegrationClient(t)
	cleanupStreamKeys(t, client, fixture.itemID)
	allRedisKeys := []string{redisgate.StateKey(fixture.itemID), redisgate.BuyersKey(fixture.itemID)}
	t.Cleanup(func() { _ = client.Del(context.Background(), allRedisKeys...).Err() })
	now := time.Now().UTC().Truncate(time.Millisecond)
	expiresAt := now.Add(2 * time.Hour)
	if err := client.HSet(ctx, redisgate.StateKey(fixture.itemID), map[string]any{
		"ready": 1, "status": seckill.StatusEnabled,
		"start_at_ms": now.Add(-time.Minute).UnixMilli(), "end_at_ms": now.Add(time.Hour).UnixMilli(),
		"stock": 1, "expire_at_ms": expiresAt.UnixMilli(), "generation": "v042-stage",
	}).Err(); err != nil {
		t.Fatalf("seed stage Redis state: %v", err)
	}
	if err := client.PExpireAt(ctx, redisgate.StateKey(fixture.itemID), expiresAt).Err(); err != nil {
		t.Fatalf("expire stage Redis state: %v", err)
	}
	gate, err := redisgate.New(client, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	repository := seckillmysql.New(db)
	api, err := seckill.NewServiceWithStreamAdmission(repository, repository, gate, gate, gate, gate, repository)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := api.Enqueue(ctx, fixture.userID, fixture.itemID)
	if err != nil || accepted.Replayed || accepted.OrderNo == "" {
		t.Fatalf("Enqueue()=%+v error=%v", accepted, err)
	}
	entries, err := client.XRange(ctx, redisgate.StreamKey(fixture.itemID), "-", "+").Result()
	if err != nil || len(entries) != 1 {
		t.Fatalf("read atomic stream entry: len=%d error=%v", len(entries), err)
	}
	for index := 1; index < 100; index++ {
		// 复制的是完整消息契约，不是重新执行准入。它模拟 consumer 至少一次语义中的
		// 重复投递；Redis buyer 幂等已经由 gate 并发测试单独验证。
		if err := client.XAdd(ctx, &redis.XAddArgs{Stream: redisgate.StreamKey(fixture.itemID), Values: entries[0].Values}).Err(); err != nil {
			t.Fatalf("append duplicate entry %d: %v", index, err)
		}
	}

	runtime, err := NewRuntime(client, repository, seckill.NewService(repository), RuntimeConfig{
		ConsumerGroup: "v042-stage", ConsumerPrefix: "stage", ConsumerConcurrency: 4,
		BatchSize: 8, Block: 20 * time.Millisecond, ClaimIdle: 250 * time.Millisecond,
		DiscoveryInterval: 20 * time.Millisecond, ShutdownTimeout: 5 * time.Second,
		MaxDeliveries: 3, Retention: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(runCtx) }()
	waitFor(t, func() bool {
		return client.XLen(context.Background(), redisgate.StreamKey(fixture.itemID)).Val() == 0
	})
	stop()
	if err := <-done; err != nil {
		t.Fatalf("stream runtime: %v", err)
	}

	state, err := repository.InspectItemState(ctx, fixture.itemID)
	if err != nil {
		t.Fatal(err)
	}
	orders := queryCount(t, ctx, db, `SELECT COUNT(*) FROM orders WHERE user_id = ? AND order_no = ?`, fixture.userID, accepted.OrderNo)
	if state.AvailableStock != 0 || state.ClaimCount != 1 || orders != 1 {
		t.Fatalf("db stock=%d claims=%d orders=%d, want 0/1/1", state.AvailableStock, state.ClaimCount, orders)
	}
	result, err := api.GetAsyncResult(ctx, fixture.userID, accepted.OrderNo)
	if err != nil || result.Status != seckill.AsyncResultSucceeded || result.Order.ID == 0 {
		t.Fatalf("GetAsyncResult()=%+v error=%v", result, err)
	}
	if runtime.PeakConcurrency() > 4 {
		t.Fatalf("peak MySQL concurrency=%d, configured=4", runtime.PeakConcurrency())
	}
}

type v042Fixture struct {
	activityID uint64
	itemID     uint64
	productID  uint64
	userID     uint64
	email      string
}

func seedV042Fixture(t *testing.T, ctx context.Context, db *sql.DB) v042Fixture {
	t.Helper()
	suffix := time.Now().UTC().UnixNano()
	product := mustExecID(t, ctx, db, `INSERT INTO products (name, description, status) VALUES ('v042 stage product', '', 1)`)
	sku := mustExecID(t, ctx, db, `INSERT INTO product_skus (product_id, code, name, price_cent, status) VALUES (?, ?, 'v042 stage sku', 100, 1)`, product, fmt.Sprintf("v042-%d", suffix))
	activity := mustExecID(t, ctx, db, `INSERT INTO seckill_activities (name, start_at, end_at, status) VALUES ('v042 stage', ?, ?, ?)`, time.Now().UTC().Add(-time.Minute), time.Now().UTC().Add(time.Hour), seckill.StatusEnabled)
	item := mustExecID(t, ctx, db, `INSERT INTO seckill_items (activity_id, sku_id, initial_stock, available_stock) VALUES (?, ?, 1, 1)`, activity, sku)
	email := fmt.Sprintf("v042-%d@example.invalid", suffix)
	user := mustExecID(t, ctx, db, `INSERT INTO users (email, password_hash, status) VALUES (?, 'stage-only', 1)`, email)
	fixture := v042Fixture{activityID: activity, itemID: item, productID: product, userID: user, email: email}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// 只删除本轮精确 ID；远程测试库允许共享，不能用模糊范围清理其他人的数据。
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM seckill_order_claims WHERE seckill_item_id = ?`, fixture.itemID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM orders WHERE user_id = ?`, fixture.userID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM seckill_activities WHERE id = ?`, fixture.activityID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM products WHERE id = ?`, fixture.productID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM users WHERE id = ?`, fixture.userID)
	})
	return fixture
}

func mustExecID(t *testing.T, ctx context.Context, db *sql.DB, query string, args ...any) uint64 {
	t.Helper()
	result, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		t.Fatalf("seed v0.4.2 stage fixture: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("read v0.4.2 fixture ID: %v", err)
	}
	return uint64(id)
}

func queryCount(t *testing.T, ctx context.Context, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var count int64
	if err := db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		t.Fatalf("query v0.4.2 stage count: %v", err)
	}
	return count
}
