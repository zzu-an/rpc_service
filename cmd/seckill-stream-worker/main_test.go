package main

import (
	"testing"
	"time"

	"service_rpc/internal/config"
)

func TestRuntimeConfigPreservesStreamBudgets(t *testing.T) {
	cfg := config.RedisStreamConfig{
		ConsumerGroup: "g", ConsumerPrefix: "c", ConsumerConcurrency: 4, BatchSize: 8,
		BlockMilliseconds: 100, ClaimIdleMilliseconds: 200, DiscoveryIntervalMilliseconds: 300,
		ShutdownTimeoutMilliseconds: 400, MaxDeliveries: 5, RetentionSeconds: 60,
	}
	got := runtimeConfig(cfg)
	if got.ConsumerConcurrency != 4 || got.BatchSize != 8 || got.Block != 100*time.Millisecond || got.ClaimIdle != 200*time.Millisecond || got.Retention != time.Minute {
		t.Fatalf("runtimeConfig() = %+v", got)
	}
}
