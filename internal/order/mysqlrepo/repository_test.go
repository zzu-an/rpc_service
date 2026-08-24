package mysqlrepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"service_rpc/internal/config"
	"service_rpc/internal/order"
	"service_rpc/internal/platform/database"
)

func TestRepositoryOrderSnapshotsAndOwnership(t *testing.T) {
	dsn := os.Getenv("SERVICE_RPC_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("SERVICE_RPC_MYSQL_TEST_DSN is not set")
	}
	db, err := database.OpenMySQL(context.Background(), config.MySQLConfig{DataSource: dsn, MaxOpenConns: 4, MaxIdleConns: 2, ConnMaxLifetimeSeconds: 60})
	if err != nil {
		t.Fatalf("open MySQL: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	suffix := time.Now().UnixNano()
	userResult, err := db.ExecContext(context.Background(), `INSERT INTO users (email, password_hash, status) VALUES (?, 'test', 1)`, fmt.Sprintf("order-%d@example.com", suffix))
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID, _ := userResult.LastInsertId()
	otherResult, _ := db.ExecContext(context.Background(), `INSERT INTO users (email, password_hash, status) VALUES (?, 'test', 1)`, fmt.Sprintf("other-%d@example.com", suffix))
	otherID, _ := otherResult.LastInsertId()
	productResult, err := db.ExecContext(context.Background(), `INSERT INTO products (name, description, status) VALUES ('Snapshot Product', '', 1)`)
	if err != nil {
		t.Fatalf("insert product: %v", err)
	}
	productID, _ := productResult.LastInsertId()
	skuResult, err := db.ExecContext(context.Background(), `INSERT INTO product_skus (product_id, code, name, price_cent, status) VALUES (?, ?, 'Snapshot SKU', 1234, 1)`, productID, fmt.Sprintf("order-sku-%d", suffix))
	if err != nil {
		t.Fatalf("insert SKU: %v", err)
	}
	skuID, _ := skuResult.LastInsertId()
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), "DELETE FROM products WHERE id = ?", productID)
		_, _ = db.ExecContext(context.Background(), "DELETE FROM users WHERE id IN (?, ?)", userID, otherID)
	})
	service := order.NewService(New(db))
	created, err := service.Create(context.Background(), uint64(userID), []order.ItemInput{{SKUID: uint64(skuID), Quantity: 2}})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	if created.TotalAmountCent != 2468 || created.Items[0].UnitPriceCent != 1234 {
		t.Fatalf("created=%+v", created)
	}
	if _, err := db.ExecContext(context.Background(), `UPDATE product_skus SET price_cent = 9999, name = 'Changed' WHERE id = ?`, skuID); err != nil {
		t.Fatalf("change SKU: %v", err)
	}
	found, err := service.Get(context.Background(), uint64(userID), created.ID)
	if err != nil || found.Items[0].UnitPriceCent != 1234 || found.Items[0].SKUName != "Snapshot SKU" {
		t.Fatalf("snapshot=%+v error=%v", found, err)
	}
	if _, err := service.Get(context.Background(), uint64(otherID), created.ID); !errors.Is(err, order.ErrOrderNotFound) {
		t.Fatalf("other user error=%v", err)
	}
	var before int
	_ = db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM orders WHERE user_id = ?", userID).Scan(&before)
	if _, err := service.Create(context.Background(), uint64(userID), []order.ItemInput{{SKUID: uint64(skuID), Quantity: 1}, {SKUID: 999999999, Quantity: 1}}); !errors.Is(err, order.ErrInvalidOrder) {
		t.Fatalf("invalid multi-item error=%v", err)
	}
	var after int
	_ = db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM orders WHERE user_id = ?", userID).Scan(&after)
	if before != after {
		t.Fatalf("partial order persisted: before=%d after=%d", before, after)
	}
}
