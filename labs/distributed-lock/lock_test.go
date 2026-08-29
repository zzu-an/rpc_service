package main

import (
	"context"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
)

func TestConcurrentAcquireHasOneImmediateWinner(t *testing.T) {
	client := lockRedisClient(t)
	key := lockTestKey(t, client)
	var winners atomic.Int64
	var winnerMu sync.Mutex
	var winner *Lease
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lease, ok, err := Acquire(context.Background(), client, key, time.Second)
			if err != nil {
				t.Errorf("Acquire() error = %v", err)
				return
			}
			if ok {
				winners.Add(1)
				winnerMu.Lock()
				winner = lease
				winnerMu.Unlock()
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("winners=%d, want 1", winners.Load())
	}
	if released, err := winner.Release(context.Background()); err != nil || !released {
		t.Fatalf("Release() = %v, %v", released, err)
	}
}

func TestOldLeaseCannotDeleteNewOwner(t *testing.T) {
	client := lockRedisClient(t)
	key := lockTestKey(t, client)
	oldLease, ok, err := Acquire(context.Background(), client, key, 40*time.Millisecond)
	if err != nil || !ok {
		t.Fatalf("old Acquire() = %v, %v", ok, err)
	}
	// 模拟 GC pause/长业务：旧持有者不知道 TTL 已经过期。
	time.Sleep(80 * time.Millisecond)
	newLease, ok, err := Acquire(context.Background(), client, key, time.Second)
	if err != nil || !ok {
		t.Fatalf("new Acquire() = %v, %v", ok, err)
	}
	if released, err := oldLease.Release(context.Background()); err != nil || released {
		t.Fatalf("old Release() = %v, %v; must not delete new owner", released, err)
	}
	value, err := client.Get(context.Background(), key).Result()
	if err != nil || value != newLease.token {
		t.Fatalf("current token=%q error=%v, want new owner", value, err)
	}
	_, _ = newLease.Release(context.Background())
}

func TestTTLExpiryAllowsTwoLogicalHolders(t *testing.T) {
	client := lockRedisClient(t)
	key := lockTestKey(t, client)
	first, ok, err := Acquire(context.Background(), client, key, 40*time.Millisecond)
	if err != nil || !ok {
		t.Fatalf("first Acquire() = %v, %v", ok, err)
	}
	time.Sleep(80 * time.Millisecond)
	second, ok, err := Acquire(context.Background(), client, key, time.Second)
	if err != nil || !ok {
		t.Fatalf("second Acquire() = %v, %v", ok, err)
	}
	if first.token == second.token {
		t.Fatal("two acquisitions reused a token")
	}
	// 此刻 first 的业务可能仍在运行，同时 second 已获得 Redis 锁。
	// 这证明 lease 本身不能阻止过期旧请求写数据库；真正安全的方案需要资源端拒绝旧 fencing token。
	if released, err := first.Release(context.Background()); err != nil || released {
		t.Fatalf("expired first Release() = %v, %v", released, err)
	}
	_, _ = second.Release(context.Background())
}

func lockRedisClient(t *testing.T) *redis.Client {
	t.Helper()
	address := os.Getenv("TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("set TEST_REDIS_ADDR to run distributed lock lab")
	}
	db, err := strconv.Atoi(os.Getenv("TEST_REDIS_DB"))
	if err != nil && os.Getenv("TEST_REDIS_DB") != "" {
		t.Fatalf("invalid TEST_REDIS_DB: %v", err)
	}
	client := redis.NewClient(&redis.Options{
		Addr: address, Password: os.Getenv("TEST_REDIS_PASSWORD"), DB: db,
		DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second,
	})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("connect Redis: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func lockTestKey(t *testing.T, client *redis.Client) string {
	t.Helper()
	key := "lab:v03:lock:" + strconv.FormatInt(time.Now().UnixNano(), 10)
	t.Cleanup(func() { _ = client.Del(context.Background(), key).Err() })
	return key
}
