package mysqlrepo

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"service_rpc/internal/config"
	"service_rpc/internal/platform/database"
	"service_rpc/internal/product"
	productmysql "service_rpc/internal/product/mysqlrepo"
	"service_rpc/internal/seckill"
)

func TestUnsafeCheckThenSetDemonstratesOversell(t *testing.T) {
	dsn := os.Getenv("SERVICE_RPC_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("SERVICE_RPC_MYSQL_TEST_DSN is not set")
	}
	const workers = 20
	db, err := database.OpenMySQL(context.Background(), config.MySQLConfig{DataSource: dsn, MaxOpenConns: workers + 2, MaxIdleConns: workers, ConnMaxLifetimeSeconds: 60})
	if err != nil {
		t.Fatalf("open MySQL: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	productService := product.NewService(productmysql.New(db))
	createdProduct, err := productService.Create(context.Background(), product.CreateInput{
		Name: "超卖复现商品",
		SKUs: []product.SKU{{Code: fmt.Sprintf("oversell-%d", time.Now().UnixNano()), Name: "默认 SKU", PriceCent: 100}},
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
	start := time.Now().UTC().Add(-time.Minute)
	activity, err := repository.CreateActivity(context.Background(), seckill.CreateActivityInput{Name: "超卖复现", StartAt: start, EndAt: start.Add(time.Hour)})
	if err != nil {
		t.Fatalf("create activity: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM seckill_activities WHERE id = ?`, activity.ID)
	})
	item, err := repository.AddItem(context.Background(), seckill.AddItemInput{ActivityID: activity.ID, SKUID: createdProduct.SKUs[0].ID, Stock: 1})
	if err != nil {
		t.Fatalf("add item: %v", err)
	}

	ready := make(chan struct{}, workers)
	release := make(chan struct{})
	errCh := make(chan error, workers)
	var admitted atomic.Int64
	var group sync.WaitGroup
	group.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer group.Done()
			var observed int64
			if err := db.QueryRowContext(context.Background(), `SELECT available_stock FROM seckill_items WHERE id = ?`, item.ID).Scan(&observed); err != nil {
				errCh <- err
				ready <- struct{}{}
				return
			}
			ready <- struct{}{}
			<-release
			if observed <= 0 {
				return
			}

			// 这是故意保留在测试里的错误写法：检查和更新之间没有锁，也没有把 stock > 0 放进 UPDATE 条件。
			// 所有协程都读到 1 后，会分别认为自己获得资格；即使后续都把同一行写成 0，业务成功数仍然是 20。
			// 面试回答“为什么先查库存会超卖”时，关键不是最终库存是否为负，而是资格/订单数已经超过初始库存。
			if _, err := db.ExecContext(context.Background(), `UPDATE seckill_items SET available_stock = ? WHERE id = ?`, observed-1, item.ID); err != nil {
				errCh <- err
				return
			}
			admitted.Add(1)
		}()
	}
	for i := 0; i < workers; i++ {
		<-ready
	}
	close(release)
	group.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("unsafe worker: %v", err)
		}
	}

	var remaining int64
	if err := db.QueryRowContext(context.Background(), `SELECT available_stock FROM seckill_items WHERE id = ?`, item.ID).Scan(&remaining); err != nil {
		t.Fatalf("read remaining stock: %v", err)
	}
	if admitted.Load() <= item.InitialStock {
		t.Fatalf("unsafe path unexpectedly admitted %d for initial stock %d", admitted.Load(), item.InitialStock)
	}
	if remaining != 0 {
		t.Fatalf("remaining stock = %d, want 0", remaining)
	}
	t.Logf("错误方案稳定复现：初始库存=%d，错误放行=%d，最终库存=%d", item.InitialStock, admitted.Load(), remaining)
}
