package mysqlrepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"

	"service_rpc/internal/seckill"
	"service_rpc/internal/seckill/redisgate"
)

func TestRedisAdmissionPurchaseConcurrentInvariants(t *testing.T) {
	fixture := newPurchaseFixture(t, 100, 1000, true)
	client := redisIntegrationClient(t)
	repository := New(fixture.db)
	gate, err := redisgate.New(client, time.Second)
	if err != nil {
		t.Fatalf("redisgate.New() error = %v", err)
	}
	snapshot, err := repository.LoadPreheatSnapshot(context.Background(), fixture.activityID)
	if err != nil {
		t.Fatalf("LoadPreheatSnapshot() error = %v", err)
	}
	item := snapshot.Items[0]
	t.Cleanup(func() {
		_ = client.Del(context.Background(), redisgate.StateKey(item.ID), redisgate.BuyersKey(item.ID)).Err()
	})
	// fixture 的活动已经开始。PublishActivity 的 now 参数只用于防止线上活动重建，
	// 测试传入开始前时间来准备同一份历史窗口，真实 Purchase 仍使用当前时间校验。
	if _, err := gate.PublishActivity(context.Background(), snapshot, snapshot.Activity.StartAt.Add(-time.Second)); err != nil {
		t.Fatalf("PublishActivity() error = %v", err)
	}
	service, err := seckill.NewServiceWithAdmission(repository, repository, gate, gate)
	if err != nil {
		t.Fatalf("NewServiceWithAdmission() error = %v", err)
	}

	var success, soldOut, unexpected atomic.Int64
	var wg sync.WaitGroup
	for _, userID := range fixture.userIDs {
		wg.Add(1)
		go func(userID uint64) {
			defer wg.Done()
			_, purchaseErr := service.Purchase(context.Background(), userID, fixture.itemID)
			switch {
			case purchaseErr == nil:
				success.Add(1)
			case errors.Is(purchaseErr, seckill.ErrOutOfStock):
				soldOut.Add(1)
			default:
				unexpected.Add(1)
				t.Errorf("Purchase(%d) error = %v", userID, purchaseErr)
			}
		}(userID)
	}
	wg.Wait()
	if success.Load() != 100 || soldOut.Load() != 900 || unexpected.Load() != 0 {
		t.Fatalf("success=%d soldOut=%d unexpected=%d", success.Load(), soldOut.Load(), unexpected.Load())
	}
	assertDualStoreState(t, fixture, client, 0, 100)
}

func TestRedisAdmissionSameUserReplayStorm(t *testing.T) {
	fixture := newPurchaseFixture(t, 5, 1, true)
	client := redisIntegrationClient(t)
	repository := New(fixture.db)
	gate, err := redisgate.New(client, time.Second)
	if err != nil {
		t.Fatalf("redisgate.New() error = %v", err)
	}
	snapshot, err := repository.LoadPreheatSnapshot(context.Background(), fixture.activityID)
	if err != nil {
		t.Fatalf("LoadPreheatSnapshot() error = %v", err)
	}
	item := snapshot.Items[0]
	t.Cleanup(func() {
		_ = client.Del(context.Background(), redisgate.StateKey(item.ID), redisgate.BuyersKey(item.ID)).Err()
	})
	if _, err := gate.PublishActivity(context.Background(), snapshot, snapshot.Activity.StartAt.Add(-time.Second)); err != nil {
		t.Fatalf("PublishActivity() error = %v", err)
	}
	service, err := seckill.NewServiceWithAdmission(repository, repository, gate, gate)
	if err != nil {
		t.Fatalf("NewServiceWithAdmission() error = %v", err)
	}

	var wg sync.WaitGroup
	orderIDs := make(chan uint64, 100)
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, purchaseErr := service.Purchase(context.Background(), fixture.userIDs[0], fixture.itemID)
			if purchaseErr != nil {
				t.Errorf("Purchase() error = %v", purchaseErr)
				return
			}
			orderIDs <- result.Order.ID
		}()
	}
	wg.Wait()
	close(orderIDs)
	var first uint64
	for id := range orderIDs {
		if first == 0 {
			first = id
		}
		if id != first {
			t.Fatalf("replay returned order %d, want %d", id, first)
		}
	}
	assertDualStoreState(t, fixture, client, 4, 1)
}

func assertDualStoreState(t *testing.T, fixture purchaseFixture, client *redis.Client, wantStock, wantClaims int64) {
	t.Helper()
	var dbStock, claims int64
	if err := fixture.db.QueryRowContext(context.Background(), `SELECT available_stock FROM seckill_items WHERE id = ?`, fixture.itemID).Scan(&dbStock); err != nil {
		t.Fatalf("read DB stock: %v", err)
	}
	if err := fixture.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM seckill_order_claims WHERE seckill_item_id = ?`, fixture.itemID).Scan(&claims); err != nil {
		t.Fatalf("read claims: %v", err)
	}
	redisStock, err := client.HGet(context.Background(), redisgate.StateKey(fixture.itemID), "stock").Int64()
	if err != nil {
		t.Fatalf("read Redis stock: %v", err)
	}
	buyers, err := client.HLen(context.Background(), redisgate.BuyersKey(fixture.itemID)).Result()
	if err != nil {
		t.Fatalf("read Redis buyers: %v", err)
	}
	if dbStock != wantStock || redisStock != wantStock || claims != wantClaims || buyers != wantClaims {
		t.Fatalf("dbStock=%d redisStock=%d claims=%d buyers=%d, want %d/%d/%d/%d", dbStock, redisStock, claims, buyers, wantStock, wantStock, wantClaims, wantClaims)
	}
}

func redisIntegrationClient(t *testing.T) *redis.Client {
	t.Helper()
	address := os.Getenv("TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("set TEST_REDIS_ADDR to run Redis+MySQL integration tests")
	}
	db, err := strconv.Atoi(os.Getenv("TEST_REDIS_DB"))
	if err != nil && os.Getenv("TEST_REDIS_DB") != "" {
		t.Fatalf("invalid TEST_REDIS_DB: %v", err)
	}
	client := redis.NewClient(&redis.Options{
		Addr: address, Password: os.Getenv("TEST_REDIS_PASSWORD"), DB: db,
		DialTimeout: time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second,
	})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("connect Redis %s: %v", fmt.Sprintf("db=%d", db), err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}
