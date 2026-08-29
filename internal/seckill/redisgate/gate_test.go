package redisgate

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
)

type fakeRunner struct {
	mu         sync.Mutex
	loadCalls  int
	evalCalls  int
	replies    []any
	evalErrors []error
	delCalls   int
	delErrors  []error
}

func (f *fakeRunner) ScriptLoad(context.Context, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loadCalls++
	return fmt.Sprintf("sha-%d", f.loadCalls), nil
}

func (f *fakeRunner) EvalSha(context.Context, string, []string, ...any) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	index := f.evalCalls
	f.evalCalls++
	if index < len(f.evalErrors) && f.evalErrors[index] != nil {
		return nil, f.evalErrors[index]
	}
	if index < len(f.replies) {
		return f.replies[index], nil
	}
	return []any{decisionReserved, "order-default"}, nil
}

func (f *fakeRunner) Eval(context.Context, string, []string, ...any) (any, error) {
	return f.EvalSha(context.Background(), "eval", nil)
}

func (f *fakeRunner) Del(context.Context, ...string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	index := f.delCalls
	f.delCalls++
	if index < len(f.delErrors) && f.delErrors[index] != nil {
		return 0, f.delErrors[index]
	}
	return 2, nil
}

func TestGateMapsStableDecisions(t *testing.T) {
	tests := []struct {
		name    string
		reply   any
		want    seckill.Reservation
		wantErr error
	}{
		{name: "reserved", reply: []any{int64(1), "order-1"}, want: seckill.Reservation{OrderNo: "order-1"}},
		{name: "replayed", reply: []any{int64(2), "order-1"}, want: seckill.Reservation{OrderNo: "order-1", Replayed: true}},
		{name: "not-ready", reply: []any{int64(-1), ""}, wantErr: seckill.ErrCacheNotReady},
		{name: "unavailable", reply: []any{int64(-2), ""}, wantErr: seckill.ErrUnavailable},
		{name: "sold-out", reply: []any{int64(-3), ""}, wantErr: seckill.ErrOutOfStock},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &fakeRunner{replies: []any{tt.reply}}
			gate, err := newWithRunner(runner, time.Second)
			if err != nil {
				t.Fatalf("newWithRunner() error = %v", err)
			}
			got, err := gate.Reserve(context.Background(), validReservationInput())
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Reserve() error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("Reserve() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestGateReloadsNOSCRIPTOnlyOnce(t *testing.T) {
	runner := &fakeRunner{
		replies:    []any{nil, []any{decisionReserved, "order-1"}},
		evalErrors: []error{errors.New("NOSCRIPT No matching script")},
	}
	gate, err := newWithRunner(runner, time.Second)
	if err != nil {
		t.Fatalf("newWithRunner() error = %v", err)
	}
	if _, err := gate.Reserve(context.Background(), validReservationInput()); err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	if runner.loadCalls != 2 || runner.evalCalls != 2 {
		t.Fatalf("calls load=%d eval=%d, want 2/2", runner.loadCalls, runner.evalCalls)
	}
}

func TestGateDoesNotRetryUnknownInfrastructureError(t *testing.T) {
	runner := &fakeRunner{evalErrors: []error{context.DeadlineExceeded}}
	gate, err := newWithRunner(runner, time.Second)
	if err != nil {
		t.Fatalf("newWithRunner() error = %v", err)
	}
	_, err = gate.Reserve(context.Background(), validReservationInput())
	if !errors.Is(err, seckill.ErrAdmissionFailure) {
		t.Fatalf("Reserve() error = %v, want admission failure", err)
	}
	if runner.evalCalls != 1 {
		t.Fatalf("EvalSha calls = %d, want 1", runner.evalCalls)
	}
}

func TestGateKeysShareClusterHashTag(t *testing.T) {
	state, buyers := StateKey(42), BuyersKey(42)
	if state != "seckill:v03:{item:42}:state" || buyers != "seckill:v03:{item:42}:buyers" {
		t.Fatalf("unexpected keys: %q %q", state, buyers)
	}
}

func TestGateRejectsInvalidInput(t *testing.T) {
	gate, err := newWithRunner(&fakeRunner{}, time.Second)
	if err != nil {
		t.Fatalf("newWithRunner() error = %v", err)
	}
	if _, err := gate.Reserve(context.Background(), seckill.ReservationInput{}); !errors.Is(err, seckill.ErrInvalidArgument) {
		t.Fatalf("Reserve() error = %v", err)
	}
}

func TestGateRealRedisAtomicReservation(t *testing.T) {
	client := integrationClient(t)
	itemID := uint64(time.Now().UnixNano())
	stateKey, buyersKey := StateKey(itemID), BuyersKey(itemID)
	t.Cleanup(func() { _ = client.Del(context.Background(), stateKey, buyersKey).Err() })

	now := time.Now().UTC()
	expiresAt := now.Add(2 * time.Minute)
	if err := client.HSet(context.Background(), stateKey, map[string]any{
		"ready":        1,
		"status":       1,
		"start_at_ms":  now.Add(-time.Minute).UnixMilli(),
		"end_at_ms":    now.Add(time.Minute).UnixMilli(),
		"stock":        25,
		"expire_at_ms": expiresAt.UnixMilli(),
	}).Err(); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	if err := client.PExpireAt(context.Background(), stateKey, expiresAt).Err(); err != nil {
		t.Fatalf("expire state: %v", err)
	}

	gate, err := New(client, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	var successes atomic.Int64
	var wg sync.WaitGroup
	for userID := uint64(1); userID <= 100; userID++ {
		wg.Add(1)
		go func(userID uint64) {
			defer wg.Done()
			_, reserveErr := gate.Reserve(context.Background(), seckill.ReservationInput{
				UserID: userID, ItemID: itemID, OrderNo: fmt.Sprintf("order-%d", userID), Now: now,
			})
			switch {
			case reserveErr == nil:
				successes.Add(1)
			case errors.Is(reserveErr, seckill.ErrOutOfStock):
			default:
				t.Errorf("Reserve(%d) error = %v", userID, reserveErr)
			}
		}(userID)
	}
	wg.Wait()

	stock, err := client.HGet(context.Background(), stateKey, "stock").Int64()
	if err != nil {
		t.Fatalf("read stock: %v", err)
	}
	buyers, err := client.HLen(context.Background(), buyersKey).Result()
	if err != nil {
		t.Fatalf("read buyers: %v", err)
	}
	if successes.Load() != 25 || stock != 0 || buyers != 25 {
		t.Fatalf("success=%d stock=%d buyers=%d, want 25/0/25", successes.Load(), stock, buyers)
	}

	first, err := gate.Reserve(context.Background(), seckill.ReservationInput{
		UserID: 1, ItemID: itemID, OrderNo: "different-order", Now: now,
	})
	if err != nil {
		t.Fatalf("replay Reserve() error = %v", err)
	}
	if !first.Replayed || first.OrderNo != "order-1" {
		t.Fatalf("replay = %+v, want original order", first)
	}
}

func TestPublishActivityReportsPartialFailure(t *testing.T) {
	runner := &fakeRunner{evalErrors: []error{nil, errors.New("write failed")}}
	gate, err := newWithRunner(runner, time.Second)
	if err != nil {
		t.Fatalf("newWithRunner() error = %v", err)
	}
	snapshot, now := validPreheatSnapshot()
	result, err := gate.PublishActivity(context.Background(), snapshot, now)
	if !errors.Is(err, seckill.ErrAdmissionFailure) {
		t.Fatalf("PublishActivity() error = %v", err)
	}
	if result.ItemCount != 1 {
		t.Fatalf("ItemCount = %d, want 1 completed item", result.ItemCount)
	}
}

func TestItemExpiryIsDeterministicAndBounded(t *testing.T) {
	end := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	first := itemExpiry(end, 7)
	if first != itemExpiry(end, 7) {
		t.Fatal("itemExpiry is not deterministic")
	}
	if first.Before(end.Add(preheatGrace)) || first.After(end.Add(preheatGrace+preheatJitterRange)) {
		t.Fatalf("expiry %v is outside bounded jitter", first)
	}
}

func TestInvalidateItemsStopsOnFailure(t *testing.T) {
	runner := &fakeRunner{delErrors: []error{nil, errors.New("delete failed")}}
	gate, err := newWithRunner(runner, time.Second)
	if err != nil {
		t.Fatalf("newWithRunner() error = %v", err)
	}
	if err := gate.InvalidateItems(context.Background(), []uint64{1, 2, 3}); !errors.Is(err, seckill.ErrAdmissionFailure) {
		t.Fatalf("InvalidateItems() error = %v", err)
	}
	if runner.delCalls != 2 {
		t.Fatalf("Del calls = %d, want stop at 2", runner.delCalls)
	}
}

func TestPublishActivityRealRedisIsIdempotent(t *testing.T) {
	client := integrationClient(t)
	snapshot, now := validPreheatSnapshot()
	base := uint64(time.Now().UnixNano())
	for i := range snapshot.Items {
		snapshot.Items[i].ID = base + uint64(i)
	}
	gate, err := New(client, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for _, item := range snapshot.Items {
		state, buyers := StateKey(item.ID), BuyersKey(item.ID)
		t.Cleanup(func() { _ = client.Del(context.Background(), state, buyers).Err() })
		if err := client.HSet(context.Background(), buyers, "dirty-user", "dirty-order").Err(); err != nil {
			t.Fatalf("seed dirty buyer: %v", err)
		}
	}

	first, err := gate.PublishActivity(context.Background(), snapshot, now)
	if err != nil {
		t.Fatalf("first PublishActivity() error = %v", err)
	}
	second, err := gate.PublishActivity(context.Background(), snapshot, now)
	if err != nil {
		t.Fatalf("second PublishActivity() error = %v", err)
	}
	if first != second || first.ItemCount != len(snapshot.Items) {
		t.Fatalf("results first=%+v second=%+v", first, second)
	}
	for _, item := range snapshot.Items {
		state, buyers := StateKey(item.ID), BuyersKey(item.ID)
		stock, err := client.HGet(context.Background(), state, "stock").Int64()
		if err != nil {
			t.Fatalf("read stock: %v", err)
		}
		buyerCount, err := client.HLen(context.Background(), buyers).Result()
		if err != nil {
			t.Fatalf("read buyers: %v", err)
		}
		if stock != item.AvailableStock || buyerCount != 0 {
			t.Fatalf("item %d stock=%d buyers=%d", item.ID, stock, buyerCount)
		}
	}
	if err := gate.InvalidateItems(context.Background(), []uint64{snapshot.Items[0].ID, snapshot.Items[1].ID}); err != nil {
		t.Fatalf("InvalidateItems() error = %v", err)
	}
}

func TestInspectItemRealRedis(t *testing.T) {
	client := integrationClient(t)
	snapshot, now := validPreheatSnapshot()
	snapshot.Items = snapshot.Items[:1]
	snapshot.Items[0].ID = uint64(time.Now().UnixNano())
	item := snapshot.Items[0]
	stateKey, buyersKey := StateKey(item.ID), BuyersKey(item.ID)
	t.Cleanup(func() { _ = client.Del(context.Background(), stateKey, buyersKey).Err() })
	gate, err := New(client, time.Second)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := gate.PublishActivity(context.Background(), snapshot, now); err != nil {
		t.Fatalf("PublishActivity() error = %v", err)
	}
	state, err := gate.InspectItem(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("InspectItem() error = %v", err)
	}
	if !state.Exists || state.Stock != item.AvailableStock || state.BuyerCount != 0 || state.Generation == "" || state.TTL <= 0 {
		t.Fatalf("InspectItem() = %+v", state)
	}
}

func validReservationInput() seckill.ReservationInput {
	return seckill.ReservationInput{UserID: 1, ItemID: 2, OrderNo: "order-1", Now: time.Now().UTC()}
}

func validPreheatSnapshot() (seckill.PreheatSnapshot, time.Time) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	activity := seckill.Activity{
		ID: 10, Name: "preheat", Status: seckill.StatusEnabled,
		StartAt: now.Add(time.Minute), EndAt: now.Add(time.Hour),
	}
	return seckill.PreheatSnapshot{
		Activity: activity,
		Items: []seckill.Item{
			{ID: 11, ActivityID: 10, SKUID: 101, InitialStock: 5, AvailableStock: 5, Version: 1},
			{ID: 12, ActivityID: 10, SKUID: 102, InitialStock: 7, AvailableStock: 6, Version: 2},
		},
	}, now
}

func integrationClient(t *testing.T) *redis.Client {
	t.Helper()
	address := os.Getenv("TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("set TEST_REDIS_ADDR to run real Redis tests")
	}
	db, err := strconv.Atoi(os.Getenv("TEST_REDIS_DB"))
	if err != nil && os.Getenv("TEST_REDIS_DB") != "" {
		t.Fatalf("invalid TEST_REDIS_DB: %v", err)
	}
	client := redis.NewClient(&redis.Options{
		Addr: address, Password: os.Getenv("TEST_REDIS_PASSWORD"), DB: db,
		DialTimeout: 500 * time.Millisecond, ReadTimeout: time.Second, WriteTimeout: time.Second,
	})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("connect Redis: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}
