package mysqlrepo

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"service_rpc/internal/seckill"
)

func TestPessimisticPurchaseSerializesHotInventory(t *testing.T) {
	fixture := newPurchaseFixture(t, 5, 20, true)
	repository := NewWithStockMode(fixture.db, StockModePessimistic)
	successes, soldOut := runConcurrentPurchases(t, fixture, repository)
	if successes != 5 || soldOut != 15 {
		t.Fatalf("successes=%d soldOut=%d, want 5/15", successes, soldOut)
	}
	assertPurchaseInvariants(t, fixture, 5)
}

func runConcurrentPurchases(t *testing.T, fixture purchaseFixture, repository *Repository) (int64, int64) {
	t.Helper()
	service := seckill.NewService(repository)
	start := make(chan struct{})
	errCh := make(chan error, len(fixture.userIDs))
	var successes atomic.Int64
	var soldOut atomic.Int64
	var group sync.WaitGroup
	group.Add(len(fixture.userIDs))
	for _, userID := range fixture.userIDs {
		userID := userID
		go func() {
			defer group.Done()
			<-start
			_, err := service.Purchase(context.Background(), userID, fixture.itemID)
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, seckill.ErrOutOfStock):
				soldOut.Add(1)
			default:
				errCh <- err
			}
		}()
	}
	close(start)
	group.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent purchase: %v", err)
		}
	}
	return successes.Load(), soldOut.Load()
}

func assertPurchaseInvariants(t *testing.T, fixture purchaseFixture, wantSuccess int64) {
	t.Helper()
	var stock, claims, orders int64
	if err := fixture.db.QueryRowContext(context.Background(), `SELECT available_stock FROM seckill_items WHERE id = ?`, fixture.itemID).Scan(&stock); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if err := fixture.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM seckill_order_claims WHERE activity_id = ?`, fixture.activityID).Scan(&claims); err != nil {
		t.Fatalf("count claims: %v", err)
	}
	if err := fixture.db.QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM orders o
		JOIN seckill_order_claims c ON c.order_id = o.id
		WHERE c.activity_id = ?
	`, fixture.activityID).Scan(&orders); err != nil {
		t.Fatalf("count orders: %v", err)
	}
	if stock != 0 || claims != wantSuccess || orders != wantSuccess {
		t.Fatalf("stock=%d claims=%d orders=%d, want 0/%d/%d", stock, claims, orders, wantSuccess, wantSuccess)
	}
}
