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
	productmysql "service_rpc/internal/product/mysqlrepo"
	"service_rpc/internal/seckill"
)

func TestRepositoryActivityLifecycle(t *testing.T) {
	dsn := os.Getenv("SERVICE_RPC_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("SERVICE_RPC_MYSQL_TEST_DSN is not set")
	}
	db, err := database.OpenMySQL(context.Background(), config.MySQLConfig{DataSource: dsn, MaxOpenConns: 8, MaxIdleConns: 4, ConnMaxLifetimeSeconds: 60})
	if err != nil {
		t.Fatalf("open MySQL: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	productService := product.NewService(productmysql.New(db))
	createdProduct, err := productService.Create(context.Background(), product.CreateInput{
		Name: "秒杀 Repository 测试商品",
		SKUs: []product.SKU{{Code: fmt.Sprintf("seckill-repo-%d", time.Now().UnixNano()), Name: "默认 SKU", PriceCent: 1999}},
	})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM products WHERE id = ?`, createdProduct.ID)
	})
	if err := productService.SetStatus(context.Background(), createdProduct.ID, product.StatusActive); err != nil {
		t.Fatalf("activate product: %v", err)
	}

	repository := New(db)
	start := time.Now().UTC().Add(time.Minute)
	activity, err := repository.CreateActivity(context.Background(), seckill.CreateActivityInput{Name: "测试活动", StartAt: start, EndAt: start.Add(time.Hour)})
	if err != nil {
		t.Fatalf("CreateActivity() error = %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM seckill_activities WHERE id = ?`, activity.ID)
	})

	item, err := repository.AddItem(context.Background(), seckill.AddItemInput{ActivityID: activity.ID, SKUID: createdProduct.SKUs[0].ID, Stock: 10})
	if err != nil {
		t.Fatalf("AddItem() error = %v", err)
	}
	if item.AvailableStock != 10 || item.InitialStock != 10 {
		t.Fatalf("unexpected item = %+v", item)
	}
	if _, err := repository.AddItem(context.Background(), seckill.AddItemInput{ActivityID: activity.ID, SKUID: createdProduct.SKUs[0].ID, Stock: 10}); !errors.Is(err, seckill.ErrConflict) {
		t.Fatalf("duplicate item error = %v, want ErrConflict", err)
	}

	if err := repository.SetActivityStatus(context.Background(), activity.ID, seckill.StatusEnabled); err != nil {
		t.Fatalf("enable activity: %v", err)
	}
	// 重复设置相同状态应幂等成功，覆盖 MySQL affected rows 为 0 的边界。
	if err := repository.SetActivityStatus(context.Background(), activity.ID, seckill.StatusEnabled); err != nil {
		t.Fatalf("enable activity again: %v", err)
	}
	if err := repository.SetActivityStatus(context.Background(), ^uint64(0), seckill.StatusEnabled); !errors.Is(err, seckill.ErrActivityNotFound) {
		t.Fatalf("missing activity error = %v, want ErrActivityNotFound", err)
	}
}

func TestParseStockMode(t *testing.T) {
	tests := []struct {
		input string
		want  StockMode
	}{
		{input: "", want: StockModeAtomic},
		{input: " atomic ", want: StockModeAtomic},
		{input: "PESSIMISTIC", want: StockModePessimistic},
		{input: "optimistic", want: StockModeOptimistic},
	}
	for _, test := range tests {
		got, err := ParseStockMode(test.input)
		if err != nil || got != test.want || got.String() == "" {
			t.Fatalf("ParseStockMode(%q) = %v, %v; want %v", test.input, got, err, test.want)
		}
	}
	if _, err := ParseStockMode("mutex"); err == nil {
		t.Fatal("ParseStockMode(\"mutex\") error = nil, want validation error")
	}
}
