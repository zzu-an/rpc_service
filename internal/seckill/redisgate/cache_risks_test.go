package redisgate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"

	"service_rpc/internal/seckill"
)

func TestUnknownItemFailsClosedWithoutLazyLoading(t *testing.T) {
	client := integrationClient(t)
	itemID := uint64(time.Now().UnixNano())
	stateKey, buyersKey := StateKey(itemID), BuyersKey(itemID)
	_ = client.Del(context.Background(), stateKey, buyersKey).Err()
	gate, err := New(client, time.Second)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = gate.Reserve(context.Background(), seckill.ReservationInput{
		UserID: 1, ItemID: itemID, OrderNo: "unknown-item", Now: time.Now().UTC(),
	})
	if !errors.Is(err, seckill.ErrCacheNotReady) {
		t.Fatalf("Reserve() error = %v, want ErrCacheNotReady", err)
	}
	// Gate 没有 MySQL reader，也没有 lazy-load 回调；领域层 spy 测试进一步断言该错误时
	// Purchase 调用为 0。两层证据共同证明“缓存穿透不会回源热点事务”。
}

func TestBuyerHashIsBoundedByInitialStock(t *testing.T) {
	client := integrationClient(t)
	itemID := uint64(time.Now().UnixNano())
	now := time.Now().UTC()
	seedRiskState(t, client, itemID, 3, now)
	gate, err := New(client, time.Second)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	var wg sync.WaitGroup
	for userID := uint64(1); userID <= 100; userID++ {
		wg.Add(1)
		go func(userID uint64) {
			defer wg.Done()
			_, reserveErr := gate.Reserve(context.Background(), seckill.ReservationInput{
				UserID: userID, ItemID: itemID, OrderNo: fmt.Sprintf("risk-%d", userID), Now: now,
			})
			if reserveErr != nil && !errors.Is(reserveErr, seckill.ErrOutOfStock) {
				t.Errorf("Reserve(%d) error = %v", userID, reserveErr)
			}
		}(userID)
	}
	wg.Wait()
	for range 20 {
		_, _ = gate.Reserve(context.Background(), seckill.ReservationInput{UserID: 1, ItemID: itemID, OrderNo: "new-order", Now: now})
	}
	buyers, err := client.HLen(context.Background(), BuyersKey(itemID)).Result()
	if err != nil {
		t.Fatalf("HLEN buyers: %v", err)
	}
	if buyers != 3 {
		t.Fatalf("buyers=%d, want bounded by stock 3", buyers)
	}
}

func TestPreheatTTLPreventsBreakdownAndAddsBoundedJitter(t *testing.T) {
	client := integrationClient(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	snapshot := seckill.PreheatSnapshot{
		Activity: seckill.Activity{ID: 1, Status: seckill.StatusEnabled, StartAt: now.Add(time.Minute), EndAt: now.Add(10 * time.Minute)},
		Items: []seckill.Item{
			{ID: uint64(time.Now().UnixNano()), ActivityID: 1, SKUID: 1, InitialStock: 2, AvailableStock: 2},
			{ID: uint64(time.Now().UnixNano()) + 1, ActivityID: 1, SKUID: 2, InitialStock: 2, AvailableStock: 2},
		},
	}
	gate, err := New(client, time.Second)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := gate.PublishActivity(context.Background(), snapshot, now); err != nil {
		t.Fatalf("PublishActivity() error = %v", err)
	}
	for _, item := range snapshot.Items {
		stateKey, buyersKey := StateKey(item.ID), BuyersKey(item.ID)
		t.Cleanup(func() { _ = client.Del(context.Background(), stateKey, buyersKey).Err() })
		state, err := gate.InspectItem(context.Background(), item.ID)
		if err != nil {
			t.Fatalf("InspectItem(%d) error = %v", item.ID, err)
		}
		minimum := time.Until(snapshot.Activity.EndAt.Add(preheatGrace)) - time.Second
		maximum := time.Until(snapshot.Activity.EndAt.Add(preheatGrace+preheatJitterRange)) + time.Second
		if state.TTL < minimum || state.TTL > maximum {
			t.Fatalf("item %d TTL=%v, want [%v,%v]", item.ID, state.TTL, minimum, maximum)
		}
	}
	if itemExpiry(snapshot.Activity.EndAt, snapshot.Items[0].ID) == itemExpiry(snapshot.Activity.EndAt, snapshot.Items[1].ID) {
		t.Fatal("chosen adjacent item IDs unexpectedly share jitter; fixture should demonstrate staggered expiry")
	}
}

func BenchmarkReserveHotItem(b *testing.B) {
	address := os.Getenv("TEST_REDIS_ADDR")
	if address == "" {
		b.Skip("set TEST_REDIS_ADDR to benchmark a real Redis")
	}
	db, err := strconv.Atoi(os.Getenv("TEST_REDIS_DB"))
	if err != nil && os.Getenv("TEST_REDIS_DB") != "" {
		b.Fatalf("invalid TEST_REDIS_DB: %v", err)
	}
	client := redis.NewClient(&redis.Options{
		Addr: address, Password: os.Getenv("TEST_REDIS_PASSWORD"), DB: db,
		DialTimeout: time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second,
	})
	defer func() { _ = client.Close() }()
	if err := client.Ping(context.Background()).Err(); err != nil {
		b.Fatalf("connect Redis: %v", err)
	}
	itemID := uint64(time.Now().UnixNano())
	now := time.Now().UTC()
	seedRiskState(b, client, itemID, int64(b.N+1), now)
	gate, err := New(client, 2*time.Second)
	if err != nil {
		b.Fatalf("New() error = %v", err)
	}
	// 预先加载脚本，避免把首次 SCRIPT LOAD 混入稳态热 key 样本。
	if _, err := gate.Reserve(context.Background(), seckill.ReservationInput{UserID: 1, ItemID: itemID, OrderNo: "warmup", Now: now}); err != nil {
		b.Fatalf("warmup Reserve() error = %v", err)
	}
	b.Cleanup(func() { _ = client.Del(context.Background(), StateKey(itemID), BuyersKey(itemID)).Err() })
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := gate.Reserve(context.Background(), seckill.ReservationInput{
			UserID: uint64(i + 2), ItemID: itemID, OrderNo: fmt.Sprintf("bench-%d", i), Now: now,
		}); err != nil {
			b.Fatalf("Reserve() error = %v", err)
		}
	}
}

type testingTB interface {
	Helper()
	Fatalf(format string, args ...any)
	Cleanup(func())
}

func seedRiskState(tb testingTB, client *redis.Client, itemID uint64, stock int64, now time.Time) {
	tb.Helper()
	stateKey, buyersKey := StateKey(itemID), BuyersKey(itemID)
	tb.Cleanup(func() { _ = client.Del(context.Background(), stateKey, buyersKey).Err() })
	expiresAt := now.Add(time.Hour)
	if err := client.HSet(context.Background(), stateKey, map[string]any{
		"ready": 1, "status": 1,
		"start_at_ms": now.Add(-time.Minute).UnixMilli(), "end_at_ms": now.Add(30 * time.Minute).UnixMilli(),
		"stock": stock, "expire_at_ms": expiresAt.UnixMilli(), "generation": "risk-test",
	}).Err(); err != nil {
		tb.Fatalf("seed risk state: %v", err)
	}
	if err := client.PExpireAt(context.Background(), stateKey, expiresAt).Err(); err != nil {
		tb.Fatalf("expire risk state: %v", err)
	}
}
