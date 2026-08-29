package cache

import (
	"context"
	"os"
	"strings"
	"testing"
)

import "service_rpc/internal/config"

func TestOpenRedisRejectsInvalidConfig(t *testing.T) {
	_, err := OpenRedis(context.Background(), config.RedisConfig{})
	if err == nil {
		t.Fatal("OpenRedis() error = nil")
	}
}

func TestOpenRedisConnectionFailureDoesNotLeakPassword(t *testing.T) {
	cfg := config.RedisConfig{
		Address:                      "127.0.0.1:1",
		Password:                     "must-not-leak",
		DialTimeoutMilliseconds:      10,
		OperationTimeoutMilliseconds: 20,
	}
	_, err := OpenRedis(context.Background(), cfg)
	if err == nil {
		t.Fatal("OpenRedis() error = nil, want connection failure")
	}
	if strings.Contains(err.Error(), cfg.Password) {
		t.Fatalf("OpenRedis() leaked password: %v", err)
	}
}

func TestOpenRedisIntegrationAndClose(t *testing.T) {
	address := os.Getenv("TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("set TEST_REDIS_ADDR to run the real Redis integration test")
	}
	client, err := OpenRedis(context.Background(), config.RedisConfig{
		Address:                      address,
		Password:                     os.Getenv("TEST_REDIS_PASSWORD"),
		DB:                           0,
		DialTimeoutMilliseconds:      500,
		OperationTimeoutMilliseconds: 300,
	})
	if err != nil {
		t.Fatalf("OpenRedis() error = %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := client.Ping(context.Background()).Err(); err == nil {
		t.Fatal("Ping() after Close error = nil")
	}
}
