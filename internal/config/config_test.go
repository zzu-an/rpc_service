package config

import (
	"strings"
	"testing"
	"time"
)

func TestParseAdmissionMode(t *testing.T) {
	tests := []struct {
		value string
		want  AdmissionMode
	}{
		{value: "", want: AdmissionModeMySQL},
		{value: " mysql ", want: AdmissionModeMySQL},
		{value: "REDIS", want: AdmissionModeRedis},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			got, err := ParseAdmissionMode(tt.value)
			if err != nil {
				t.Fatalf("ParseAdmissionMode() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("ParseAdmissionMode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseAdmissionModeRejectsUnknownWithoutLeakingFallback(t *testing.T) {
	if _, err := ParseAdmissionMode("automatic"); err == nil {
		t.Fatal("ParseAdmissionMode() error = nil, want unsupported mode")
	}
}

func TestRedisConfigValidate(t *testing.T) {
	valid := RedisConfig{
		Address:                      "127.0.0.1:6379",
		DB:                           1,
		DialTimeoutMilliseconds:      300,
		OperationTimeoutMilliseconds: 100,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if valid.DialTimeout() != 300*time.Millisecond || valid.OperationTimeout() != 100*time.Millisecond {
		t.Fatalf("unexpected durations: dial=%s operation=%s", valid.DialTimeout(), valid.OperationTimeout())
	}

	tests := []RedisConfig{
		{DialTimeoutMilliseconds: 1, OperationTimeoutMilliseconds: 1},
		{Address: "redis:6379", DB: -1, DialTimeoutMilliseconds: 1, OperationTimeoutMilliseconds: 1},
		{Address: "redis:6379", OperationTimeoutMilliseconds: 1},
		{Address: "redis:6379", DialTimeoutMilliseconds: 1},
	}
	for i, cfg := range tests {
		if err := cfg.Validate(); err == nil {
			t.Fatalf("case %d Validate() error = nil", i)
		}
	}
}

func TestRedisValidationErrorDoesNotContainPassword(t *testing.T) {
	cfg := RedisConfig{Password: "top-secret"}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil")
	}
	if strings.Contains(err.Error(), cfg.Password) {
		t.Fatalf("validation error leaked password: %v", err)
	}
}

func TestParseOrderMode(t *testing.T) {
	for _, test := range []struct {
		value string
		want  OrderMode
	}{{"", OrderModeSync}, {" sync ", OrderModeSync}, {"ASYNC", OrderModeAsyncStream}, {"ASYNC-STREAM", OrderModeAsyncStream}} {
		got, err := ParseOrderMode(test.value)
		if err != nil || got != test.want || got.String() == "" {
			t.Fatalf("ParseOrderMode(%q) = %v, %v", test.value, got, err)
		}
	}
	if _, err := ParseOrderMode("fallback"); err == nil {
		t.Fatal("ParseOrderMode(fallback) error = nil")
	}
	if _, err := ParseOrderMode("async-kafka"); err == nil {
		t.Fatal("Kafka mode must be unavailable on the Redis Stream branch")
	}
}

func TestRedisStreamConfigValidate(t *testing.T) {
	valid := RedisStreamConfig{
		ConsumerGroup: "seckill-stream", ConsumerPrefix: "worker", ConsumerConcurrency: 4,
		BatchSize: 10, BlockMilliseconds: 100, ClaimIdleMilliseconds: 500,
		DiscoveryIntervalMilliseconds: 1000, ShutdownTimeoutMilliseconds: 5000,
		MaxDeliveries: 3, RetentionSeconds: 86400,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if valid.Block() != 100*time.Millisecond || valid.Retention() != 24*time.Hour {
		t.Fatalf("unexpected durations block=%s retention=%s", valid.Block(), valid.Retention())
	}
	invalid := valid
	invalid.MaxDeliveries = 0
	if err := invalid.Validate(); err == nil {
		t.Fatal("zero max deliveries accepted")
	}
}
