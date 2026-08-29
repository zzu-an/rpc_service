package redisgate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	migratemysql "github.com/golang-migrate/migrate/v4/database/mysql"
	_ "github.com/golang-migrate/migrate/v4/source/file"

	"service_rpc/internal/seckill"
)

// TestV03StageEnvironmentGate 是 Makefile 阶段门禁的“防假绿”开关。
//
// 普通开发者执行 go test ./... 时可以没有外部基础设施；但 verify-v03 会显式设置
// V03_STAGE_VERIFY=1，此时缺少 MySQL/Redis 或连接失败必须让整个门禁失败，不能被
// integrationClient 的 Skip 掩盖。面试点：集成测试可选与发布门禁必跑是两种语义。
func TestV03StageEnvironmentGate(t *testing.T) {
	if os.Getenv("V03_STAGE_VERIFY") != "1" {
		t.Skip("V03_STAGE_VERIFY is not enabled")
	}
	dsn := requireStageEnvironment(t, "TEST_DSN")
	requireStageEnvironment(t, "TEST_REDIS_ADDR")

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open stage MySQL: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping stage MySQL: %v", err)
	}

	// integrationClient 还会验证认证、DB 编号与 PING，并且错误文本不会打印密码。
	client := integrationClient(t)
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping stage Redis: %v", err)
	}
}

func TestV03StageRedis1000Requests100Stock(t *testing.T) {
	client := integrationClient(t)
	itemID := uint64(time.Now().UnixNano())
	now := time.Now().UTC()
	seedRiskState(t, client, itemID, 100, now)
	gate, err := New(client, time.Second)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	var reserved, soldOut, unexpected atomic.Int64
	var group sync.WaitGroup
	for userID := uint64(1); userID <= 1000; userID++ {
		group.Add(1)
		go func(userID uint64) {
			defer group.Done()
			_, reserveErr := gate.Reserve(context.Background(), seckill.ReservationInput{
				UserID: userID, ItemID: itemID, OrderNo: fmt.Sprintf("stage-v03-%d", userID), Now: now,
			})
			switch {
			case reserveErr == nil:
				reserved.Add(1)
			case errors.Is(reserveErr, seckill.ErrOutOfStock):
				soldOut.Add(1)
			default:
				unexpected.Add(1)
				t.Errorf("Reserve(%d) error = %v", userID, reserveErr)
			}
		}(userID)
	}
	group.Wait()

	state, err := gate.InspectItem(context.Background(), itemID)
	if err != nil {
		t.Fatalf("InspectItem() error = %v", err)
	}
	if reserved.Load() != 100 || soldOut.Load() != 900 || unexpected.Load() != 0 || state.Stock != 0 || state.BuyerCount != 100 {
		t.Fatalf("reserved=%d soldOut=%d unexpected=%d stock=%d buyers=%d, want 100/900/0/0/100",
			reserved.Load(), soldOut.Load(), unexpected.Load(), state.Stock, state.BuyerCount)
	}

	// 库存为 0 后仍必须先查 buyer，才能让结果未知的第一次请求携原订单号恢复。
	replay, err := gate.Reserve(context.Background(), seckill.ReservationInput{
		UserID: 1, ItemID: itemID, OrderNo: "must-not-replace-first-order", Now: now,
	})
	if err != nil || !replay.Replayed || replay.OrderNo != "stage-v03-1" {
		t.Fatalf("replay=%+v error=%v, want original reservation", replay, err)
	}
}

func TestV03MigrationRoundTrip(t *testing.T) {
	if os.Getenv("V03_MIGRATION_VERIFY") != "1" {
		t.Skip("V03_MIGRATION_VERIFY is not enabled")
	}
	dsn := requireStageEnvironment(t, "TEST_DSN")
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open migration MySQL: %v", err)
	}
	driver, err := migratemysql.WithInstance(db, &migratemysql.Config{})
	if err != nil {
		_ = db.Close()
		t.Fatalf("initialize migration driver: %v", err)
	}
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	sourceURL := (&url.URL{Scheme: "file", Path: filepath.Join(repositoryRoot, "migrations")}).String()
	migrator, err := migrate.NewWithDatabaseInstance(sourceURL, "mysql", driver)
	if err != nil {
		_ = driver.Close()
		t.Fatalf("initialize migrator: %v", err)
	}
	t.Cleanup(func() {
		sourceErr, databaseErr := migrator.Close()
		if sourceErr != nil || databaseErr != nil {
			t.Errorf("close migrator: source=%v database=%v", sourceErr, databaseErr)
		}
	})

	before, dirty, err := migrator.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		// 全新隔离库先执行完整 up，再继续验证 latest -> previous -> latest。
		// 这同时覆盖“从零部署”和“单步回滚可恢复”，不会为了门禁预置人工 schema。
		if err := migrator.Up(); err != nil {
			t.Fatalf("initial migration up: %v", err)
		}
		before, dirty, err = migrator.Version()
	}
	if err != nil || dirty {
		t.Fatalf("migration version before round trip: version=%d dirty=%t error=%v", before, dirty, err)
	}
	if err := migrator.Steps(-1); err != nil {
		t.Fatalf("migration down from version %d: %v", before, err)
	}
	afterDown, dirty, err := migrator.Version()
	if err != nil || dirty || afterDown+1 != before {
		t.Fatalf("migration after down: version=%d dirty=%t error=%v, want %d/false", afterDown, dirty, err, before-1)
	}
	if err := migrator.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migration up: %v", err)
	}
	afterUp, dirty, err := migrator.Version()
	if err != nil || dirty || afterUp != before {
		t.Fatalf("migration after restore: version=%d dirty=%t error=%v, want %d/false", afterUp, dirty, err, before)
	}
}

func requireStageEnvironment(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Fatalf("%s is required by the v0.3 stage gate", name)
	}
	return value
}
