package mysqlrepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"service_rpc/internal/config"
	"service_rpc/internal/platform/database"
	"service_rpc/internal/product"
)

func TestRepositoryProductLifecycle(t *testing.T) {
	dsn := os.Getenv("SERVICE_RPC_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("SERVICE_RPC_MYSQL_TEST_DSN is not set")
	}
	db, err := database.OpenMySQL(context.Background(), config.MySQLConfig{DataSource: dsn, MaxOpenConns: 4, MaxIdleConns: 2, ConnMaxLifetimeSeconds: 60})
	if err != nil {
		t.Fatalf("open MySQL: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service := product.NewService(New(db))
	code := fmt.Sprintf("sku-%d", time.Now().UnixNano())
	created, err := service.Create(context.Background(), product.CreateInput{Name: "Test product", SKUs: []product.SKU{{Code: code, Name: "Default", PriceCent: 1999}}})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DELETE FROM products WHERE id = ?", created.ID) })
	if _, err := service.GetPublic(context.Background(), created.ID); !errors.Is(err, product.ErrProductNotFound) {
		t.Fatalf("inactive product error=%v", err)
	}
	if err := service.SetStatus(context.Background(), created.ID, product.StatusActive); err != nil {
		t.Fatalf("activate: %v", err)
	}
	found, err := service.GetPublic(context.Background(), created.ID)
	if err != nil || len(found.SKUs) != 1 || found.SKUs[0].PriceCent != 1999 {
		t.Fatalf("found=%+v error=%v", found, err)
	}
	page, err := service.ListPublic(context.Background(), 1, 20)
	if err != nil || page.Total < 1 {
		t.Fatalf("page=%+v error=%v", page, err)
	}
}
