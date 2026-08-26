package mysqlrepo

import (
	"context"
	"sync"
	"testing"

	"service_rpc/internal/seckill"
)

func TestStageV02ConcurrentCorrectness(t *testing.T) {
	tests := []struct {
		name       string
		mode       StockMode
		concurrent int
		stock      int64
	}{
		{name: "atomic-100", mode: StockModeAtomic, concurrent: 100, stock: 10},
		{name: "pessimistic-100", mode: StockModePessimistic, concurrent: 100, stock: 10},
		{name: "optimistic-100", mode: StockModeOptimistic, concurrent: 100, stock: 10},
		{name: "atomic-1000", mode: StockModeAtomic, concurrent: 1000, stock: 50},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPurchaseFixture(t, test.stock, test.concurrent, true)
			successes, soldOut := runConcurrentPurchases(t, fixture, NewWithStockMode(fixture.db, test.mode))
			if successes != test.stock || soldOut != int64(test.concurrent)-test.stock {
				t.Fatalf("successes=%d soldOut=%d, want %d/%d", successes, soldOut, test.stock, int64(test.concurrent)-test.stock)
			}
			assertPurchaseInvariants(t, fixture, test.stock)
		})
	}
}

func TestStageV02ConcurrentReplayDeductsStockOnce(t *testing.T) {
	const concurrent = 100
	fixture := newPurchaseFixture(t, 10, 1, true)
	service := seckill.NewService(New(fixture.db))
	start := make(chan struct{})
	results := make(chan seckill.PurchaseResult, concurrent)
	errors := make(chan error, concurrent)
	var group sync.WaitGroup
	group.Add(concurrent)
	for i := 0; i < concurrent; i++ {
		go func() {
			defer group.Done()
			<-start
			result, err := service.Purchase(context.Background(), fixture.userIDs[0], fixture.itemID)
			if err != nil {
				errors <- err
				return
			}
			results <- result
		}()
	}
	close(start)
	group.Wait()
	close(results)
	close(errors)
	for err := range errors {
		t.Fatalf("concurrent replay: %v", err)
	}
	var orderID uint64
	var firstResponses int
	for result := range results {
		if orderID == 0 {
			orderID = result.Order.ID
		}
		if result.Order.ID != orderID {
			t.Fatalf("replay returned different order IDs: first=%d current=%d", orderID, result.Order.ID)
		}
		if !result.Replayed {
			firstResponses++
		}
	}
	// 并发请求的返回顺序不确定，但数据库只能有一个首次创建者；其余请求必须读取同一订单。
	if firstResponses != 1 {
		t.Fatalf("non-replayed responses=%d, want exactly 1", firstResponses)
	}
	var stock, claims int64
	_ = fixture.db.QueryRowContext(context.Background(), `SELECT available_stock FROM seckill_items WHERE id = ?`, fixture.itemID).Scan(&stock)
	_ = fixture.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM seckill_order_claims WHERE activity_id = ?`, fixture.activityID).Scan(&claims)
	if stock != 9 || claims != 1 {
		t.Fatalf("stock=%d claims=%d, want 9/1", stock, claims)
	}
}
