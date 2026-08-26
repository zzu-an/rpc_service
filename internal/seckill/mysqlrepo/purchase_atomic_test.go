package mysqlrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"service_rpc/internal/config"
	"service_rpc/internal/platform/database"
	"service_rpc/internal/seckill"
)

type purchaseFixture struct {
	db         *sql.DB
	activityID uint64
	itemID     uint64
	productID  uint64
	userIDs    []uint64
	emailLike  string
}

func newPurchaseFixture(t *testing.T, stock int64, userCount int, enabled bool) purchaseFixture {
	t.Helper()
	dsn := os.Getenv("SERVICE_RPC_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("SERVICE_RPC_MYSQL_TEST_DSN is not set")
	}
	db, err := database.OpenMySQL(context.Background(), config.MySQLConfig{DataSource: dsn, MaxOpenConns: 32, MaxIdleConns: 16, ConnMaxLifetimeSeconds: 60})
	if err != nil {
		t.Fatalf("open MySQL: %v", err)
	}
	suffix := time.Now().UnixNano()
	productResult, err := db.ExecContext(context.Background(), `INSERT INTO products (name, description, status) VALUES ('秒杀测试商品', '', 1)`)
	if err != nil {
		t.Fatalf("insert product: %v", err)
	}
	productID, _ := productResult.LastInsertId()
	skuResult, err := db.ExecContext(context.Background(), `INSERT INTO product_skus (product_id, code, name, price_cent, status) VALUES (?, ?, '秒杀 SKU', 1234, 1)`, productID, fmt.Sprintf("purchase-%d", suffix))
	if err != nil {
		t.Fatalf("insert SKU: %v", err)
	}
	skuID, _ := skuResult.LastInsertId()
	status := seckill.StatusDisabled
	if enabled {
		status = seckill.StatusEnabled
	}
	activityResult, err := db.ExecContext(context.Background(), `INSERT INTO seckill_activities (name, start_at, end_at, status) VALUES ('购买测试', ?, ?, ?)`, time.Now().UTC().Add(-time.Minute), time.Now().UTC().Add(time.Hour), status)
	if err != nil {
		t.Fatalf("insert activity: %v", err)
	}
	activityID, _ := activityResult.LastInsertId()
	itemResult, err := db.ExecContext(context.Background(), `INSERT INTO seckill_items (activity_id, sku_id, initial_stock, available_stock) VALUES (?, ?, ?, ?)`, activityID, skuID, stock, stock)
	if err != nil {
		t.Fatalf("insert item: %v", err)
	}
	itemID, _ := itemResult.LastInsertId()
	userIDs := make([]uint64, 0, userCount)
	for i := 0; i < userCount; i++ {
		result, err := db.ExecContext(context.Background(), `INSERT INTO users (email, password_hash, status) VALUES (?, 'test', 1)`, fmt.Sprintf("purchase-%d-%d@example.com", suffix, i))
		if err != nil {
			t.Fatalf("insert user %d: %v", i, err)
		}
		id, _ := result.LastInsertId()
		userIDs = append(userIDs, uint64(id))
	}
	fixture := purchaseFixture{db: db, activityID: uint64(activityID), itemID: uint64(itemID), productID: uint64(productID), userIDs: userIDs, emailLike: fmt.Sprintf("purchase-%d-%%", suffix)}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM seckill_order_claims WHERE activity_id = ?`, fixture.activityID)
		_, _ = db.ExecContext(context.Background(), `DELETE o FROM orders o JOIN users u ON u.id = o.user_id WHERE u.email LIKE ?`, fixture.emailLike)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM seckill_activities WHERE id = ?`, fixture.activityID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM products WHERE id = ?`, fixture.productID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM users WHERE email LIKE ?`, fixture.emailLike)
		_ = db.Close()
	})
	return fixture
}

func TestAtomicPurchaseAndIdempotentReplay(t *testing.T) {
	fixture := newPurchaseFixture(t, 2, 3, true)
	service := seckill.NewService(New(fixture.db))

	first, err := service.Purchase(context.Background(), fixture.userIDs[0], fixture.itemID)
	if err != nil || first.Replayed {
		t.Fatalf("first purchase=%+v error=%v", first, err)
	}
	replayed, err := service.Purchase(context.Background(), fixture.userIDs[0], fixture.itemID)
	if err != nil || !replayed.Replayed || replayed.Order.ID != first.Order.ID {
		t.Fatalf("replayed purchase=%+v error=%v", replayed, err)
	}
	if !replayed.Order.CreatedAt.Equal(first.Order.CreatedAt) {
		t.Fatalf("replayed created_at=%v, want first response value %v", replayed.Order.CreatedAt, first.Order.CreatedAt)
	}
	second, err := service.Purchase(context.Background(), fixture.userIDs[1], fixture.itemID)
	if err != nil || second.Order.ID == first.Order.ID {
		t.Fatalf("second purchase=%+v error=%v", second, err)
	}
	if _, err := service.Purchase(context.Background(), fixture.userIDs[2], fixture.itemID); !errors.Is(err, seckill.ErrOutOfStock) {
		t.Fatalf("third purchase error=%v, want ErrOutOfStock", err)
	}

	var stock, orders, claims int64
	if err := fixture.db.QueryRowContext(context.Background(), `SELECT available_stock FROM seckill_items WHERE id = ?`, fixture.itemID).Scan(&stock); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if err := fixture.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM orders WHERE user_id IN (?, ?, ?)`, fixture.userIDs[0], fixture.userIDs[1], fixture.userIDs[2]).Scan(&orders); err != nil {
		t.Fatalf("count orders: %v", err)
	}
	if err := fixture.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM seckill_order_claims WHERE activity_id = ?`, fixture.activityID).Scan(&claims); err != nil {
		t.Fatalf("count claims: %v", err)
	}
	if stock != 0 || orders != 2 || claims != 2 {
		t.Fatalf("stock=%d orders=%d claims=%d, want 0/2/2", stock, orders, claims)
	}
}

func TestAtomicPurchaseRejectsDisabledActivityWithoutSideEffects(t *testing.T) {
	fixture := newPurchaseFixture(t, 1, 1, false)
	service := seckill.NewService(New(fixture.db))
	if _, err := service.Purchase(context.Background(), fixture.userIDs[0], fixture.itemID); !errors.Is(err, seckill.ErrUnavailable) {
		t.Fatalf("purchase error=%v, want ErrUnavailable", err)
	}
	var stock int64
	_ = fixture.db.QueryRowContext(context.Background(), `SELECT available_stock FROM seckill_items WHERE id = ?`, fixture.itemID).Scan(&stock)
	if stock != 1 {
		t.Fatalf("stock=%d, want unchanged 1", stock)
	}
}

func TestAtomicPurchaseRejectsOutsideActivityWindow(t *testing.T) {
	tests := []struct {
		name  string
		start time.Time
		end   time.Time
	}{
		{name: "not started", start: time.Now().UTC().Add(time.Hour), end: time.Now().UTC().Add(2 * time.Hour)},
		{name: "ended", start: time.Now().UTC().Add(-2 * time.Hour), end: time.Now().UTC().Add(-time.Hour)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPurchaseFixture(t, 1, 1, true)
			if _, err := fixture.db.ExecContext(context.Background(), `UPDATE seckill_activities SET start_at = ?, end_at = ? WHERE id = ?`, test.start, test.end, fixture.activityID); err != nil {
				t.Fatalf("set activity window: %v", err)
			}
			service := seckill.NewService(New(fixture.db))
			if _, err := service.Purchase(context.Background(), fixture.userIDs[0], fixture.itemID); !errors.Is(err, seckill.ErrUnavailable) {
				t.Fatalf("purchase error=%v, want ErrUnavailable", err)
			}
			// 活动时间采用 [start_at, end_at)；时间校验发生在扣库存前，失败不能留下订单或库存副作用。
			var stock, claims int64
			_ = fixture.db.QueryRowContext(context.Background(), `SELECT available_stock FROM seckill_items WHERE id = ?`, fixture.itemID).Scan(&stock)
			_ = fixture.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM seckill_order_claims WHERE activity_id = ?`, fixture.activityID).Scan(&claims)
			if stock != 1 || claims != 0 {
				t.Fatalf("stock=%d claims=%d, want 1/0", stock, claims)
			}
		})
	}
}
