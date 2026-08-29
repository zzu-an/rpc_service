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

func TestKafkaConfigValidate(t *testing.T) {
	valid := KafkaConfig{
		Brokers: []string{"192.168.0.107:9092"}, MainTopic: "seckill-main",
		RetryTopic: "seckill-retry", DLQTopic: "seckill-dlq", ConsumerGroup: "seckill-worker",
		TopicPartitions: 4, OperationTimeoutMilliseconds: 1000, ConsumerConcurrency: 4, MaxConsumeAttempts: 3,
		RelayIntervalMilliseconds: 100, ShutdownTimeoutMilliseconds: 5000,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if valid.OperationTimeout() != time.Second || valid.RelayInterval() != 100*time.Millisecond || valid.ShutdownTimeout() != 5*time.Second {
		t.Fatalf("unexpected Kafka durations")
	}

	tests := []KafkaConfig{
		{},
		{Brokers: []string{""}},
		{Brokers: []string{"kafka:9092"}, MainTopic: "same", RetryTopic: "same", DLQTopic: "dlq", ConsumerGroup: "g", TopicPartitions: 1, OperationTimeoutMilliseconds: 1, ConsumerConcurrency: 1, MaxConsumeAttempts: 1, RelayIntervalMilliseconds: 1, ShutdownTimeoutMilliseconds: 1},
	}
	for i, cfg := range tests {
		if err := cfg.Validate(); err == nil {
			t.Fatalf("case %d Validate() error = nil", i)
		}
	}
}

func TestParseOrderMode(t *testing.T) {
	for _, test := range []struct {
		value string
		want  OrderMode
	}{{"", OrderModeSync}, {" sync ", OrderModeSync}, {"ASYNC", OrderModeAsync}} {
		got, err := ParseOrderMode(test.value)
		if err != nil || got != test.want || got.String() == "" {
			t.Fatalf("ParseOrderMode(%q) = %v, %v", test.value, got, err)
		}
	}
	if _, err := ParseOrderMode("fallback"); err == nil {
		t.Fatal("ParseOrderMode(fallback) error = nil")
	}
}
