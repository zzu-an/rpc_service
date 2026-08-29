package streamqueue

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"

	"service_rpc/internal/seckill"
	"service_rpc/internal/seckill/redisgate"
)

type itemSourceStub struct{ itemID uint64 }

func (s itemSourceStub) ListStreamItemIDs(context.Context) ([]uint64, error) {
	return []uint64{s.itemID}, nil
}

type processorStub struct {
	calls atomic.Int64
	err   error
}

func (p *processorStub) ProcessStreamTask(context.Context, uint64, uint64, string, time.Time) (seckill.PurchaseResult, error) {
	p.calls.Add(1)
	return seckill.PurchaseResult{}, p.err
}

func TestDecodeTaskRejectsItemDrift(t *testing.T) {
	message := redis.XMessage{ID: "1-0", Values: map[string]any{
		"schema_version": "1", "event_type": streamEventV1, "order_no": "T1-9-0000000000000001",
		"user_id": "7", "item_id": "9", "reserved_at_ms": "1",
	}}
	if _, err := decodeTask(8, message); !errors.Is(err, ErrInvalidStreamMessage) {
		t.Fatalf("decodeTask() error = %v", err)
	}
}

func TestRuntimeRealRedisProcessesAndDeletesMessage(t *testing.T) {
	client := streamIntegrationClient(t)
	itemID := uint64(time.Now().UnixNano())
	cleanupStreamKeys(t, client, itemID)
	addTask(t, client, itemID, 7, "T1-"+strconv.FormatUint(itemID, 10)+"-0000000000000001")
	processor := &processorStub{}
	runtime := newTestRuntime(t, client, itemID, processor, 3)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	waitFor(t, func() bool {
		return processor.calls.Load() >= 1 && client.XLen(context.Background(), redisgate.StreamKey(itemID)).Val() == 0
	})
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if runtime.PeakConcurrency() > 2 {
		t.Fatalf("peak concurrency = %d", runtime.PeakConcurrency())
	}
}

func TestRuntimeRealRedisReclaimsPendingEntry(t *testing.T) {
	client := streamIntegrationClient(t)
	itemID := uint64(time.Now().UnixNano())
	cleanupStreamKeys(t, client, itemID)
	streamKey := redisgate.StreamKey(itemID)
	addTask(t, client, itemID, 8, "T1-"+strconv.FormatUint(itemID, 10)+"-0000000000000002")
	if err := client.XGroupCreate(context.Background(), streamKey, "v042-test", "0").Err(); err != nil {
		t.Fatalf("create group: %v", err)
	}
	if _, err := client.XReadGroup(context.Background(), &redis.XReadGroupArgs{
		Group: "v042-test", Consumer: "crashed", Streams: []string{streamKey, ">"}, Count: 1,
	}).Result(); err != nil {
		t.Fatalf("seed pending: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	processor := &processorStub{}
	runtime := newTestRuntime(t, client, itemID, processor, 3)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	waitFor(t, func() bool {
		return processor.calls.Load() >= 1 && client.XLen(context.Background(), streamKey).Val() == 0
	})
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRuntimeRealRedisMovesTerminalFailureToDLQ(t *testing.T) {
	client := streamIntegrationClient(t)
	itemID := uint64(time.Now().UnixNano())
	cleanupStreamKeys(t, client, itemID)
	orderNo := "T1-" + strconv.FormatUint(itemID, 10) + "-0000000000000003"
	addTask(t, client, itemID, 9, orderNo)
	client.HSet(context.Background(), redisgate.StreamResultsKey(itemID), orderNo, "9|QUEUED")
	processor := &processorStub{err: seckill.ErrItemNotFound}
	runtime := newTestRuntime(t, client, itemID, processor, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	waitFor(t, func() bool { return client.XLen(context.Background(), redisgate.StreamDLQKey(itemID)).Val() == 1 })
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	status := client.HGet(context.Background(), redisgate.StreamResultsKey(itemID), orderNo).Val()
	if status != "9|FAILED" || client.XLen(context.Background(), redisgate.StreamKey(itemID)).Val() != 0 {
		t.Fatalf("status=%q source_len=%d", status, client.XLen(context.Background(), redisgate.StreamKey(itemID)).Val())
	}
}

func newTestRuntime(t *testing.T, client *redis.Client, itemID uint64, processor *processorStub, max int) *Runtime {
	t.Helper()
	runtime, err := NewRuntime(client, itemSourceStub{itemID: itemID}, processor, RuntimeConfig{
		ConsumerGroup: "v042-test", ConsumerPrefix: "test", ConsumerConcurrency: 2,
		BatchSize: 1, Block: 20 * time.Millisecond, ClaimIdle: 20 * time.Millisecond,
		DiscoveryInterval: 10 * time.Millisecond, ShutdownTimeout: time.Second,
		MaxDeliveries: max, Retention: time.Hour,
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	return runtime
}

func addTask(t *testing.T, client *redis.Client, itemID, userID uint64, orderNo string) {
	t.Helper()
	if err := client.XAdd(context.Background(), &redis.XAddArgs{Stream: redisgate.StreamKey(itemID), Values: map[string]any{
		"schema_version": 1, "event_type": streamEventV1, "order_no": orderNo,
		"user_id": userID, "item_id": itemID, "reserved_at_ms": time.Now().UTC().UnixMilli(),
	}}).Err(); err != nil {
		t.Fatalf("XAdd: %v", err)
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not reached before timeout")
}

func cleanupStreamKeys(t *testing.T, client *redis.Client, itemID uint64) {
	t.Helper()
	keys := []string{redisgate.StreamKey(itemID), redisgate.StreamResultsKey(itemID), redisgate.StreamRetriesKey(itemID), redisgate.StreamDLQKey(itemID)}
	_ = client.Del(context.Background(), keys...).Err()
	t.Cleanup(func() { _ = client.Del(context.Background(), keys...).Err() })
}

func streamIntegrationClient(t *testing.T) *redis.Client {
	t.Helper()
	address := os.Getenv("TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("set TEST_REDIS_ADDR to run real Redis Stream tests")
	}
	db, err := strconv.Atoi(os.Getenv("TEST_REDIS_DB"))
	if err != nil && os.Getenv("TEST_REDIS_DB") != "" {
		t.Fatalf("invalid TEST_REDIS_DB: %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: address, Password: os.Getenv("TEST_REDIS_PASSWORD"), DB: db, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("connect Redis: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func ExampleRuntime_failureBoundary() {
	fmt.Println("MySQL commit -> Redis XACK; crash between them causes safe duplicate delivery")
	// Output: MySQL commit -> Redis XACK; crash between them causes safe duplicate delivery
}
