package mysqlrepo

import (
	"context"
	"testing"
)

func TestOptimisticPurchaseRetriesBoundedContention(t *testing.T) {
	fixture := newPurchaseFixture(t, 5, 20, true)
	repository := NewWithStockMode(fixture.db, StockModeOptimistic)
	successes, soldOut := runConcurrentPurchases(t, fixture, repository)
	if successes != 5 || soldOut != 15 {
		t.Fatalf("successes=%d soldOut=%d, want 5/15", successes, soldOut)
	}
	assertPurchaseInvariants(t, fixture, 5)

	var version uint64
	if err := fixture.db.QueryRowContext(context.Background(), `SELECT version FROM seckill_items WHERE id = ?`, fixture.itemID).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != 5 {
		t.Fatalf("version=%d, want one increment for each of 5 successful deductions", version)
	}
}
