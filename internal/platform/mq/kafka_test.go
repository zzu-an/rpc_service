package mq

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"service_rpc/internal/config"
)

func testKafkaConfig(broker string) config.KafkaConfig {
	return config.KafkaConfig{
		Brokers: []string{broker}, MainTopic: "test-main", RetryTopic: "test-retry", DLQTopic: "test-dlq",
		ConsumerGroup: "test-group", AllowAutoTopicCreation: true, OperationTimeoutMilliseconds: 3000,
		TopicPartitions: 1, ConsumerConcurrency: 1, MaxConsumeAttempts: 3, RelayIntervalMilliseconds: 10, ShutdownTimeoutMilliseconds: 1000,
	}
}

func TestOpenKafkaProducerRejectsInvalidConfig(t *testing.T) {
	if _, err := OpenKafkaProducer(context.Background(), config.KafkaConfig{}, "topic"); err == nil {
		t.Fatal("OpenKafkaProducer() error = nil")
	}
}

func TestOpenKafkaProducerConnectionFailure(t *testing.T) {
	cfg := testKafkaConfig("127.0.0.1:1")
	cfg.OperationTimeoutMilliseconds = 20
	_, err := OpenKafkaProducer(context.Background(), cfg, cfg.MainTopic)
	if err == nil || !strings.Contains(err.Error(), "connect kafka broker") {
		t.Fatalf("OpenKafkaProducer() error = %v", err)
	}
}

func TestKafkaProducerIntegration(t *testing.T) {
	broker := os.Getenv("TEST_KAFKA_BROKERS")
	if broker == "" {
		t.Skip("set TEST_KAFKA_BROKERS to run the real Kafka integration test")
	}
	// TASK-035 只验证 broker ack 和 Close；消费、lag 和 topic 清理由后续阶段测试覆盖。
	cfg := testKafkaConfig(strings.Split(broker, ",")[0])
	cfg.Brokers = strings.Split(broker, ",")
	suffix := time.Now().UnixNano()
	cfg.MainTopic = fmt.Sprintf("service-rpc-v04-runtime-main-%d", suffix)
	cfg.RetryTopic = fmt.Sprintf("service-rpc-v04-runtime-retry-%d", suffix)
	cfg.DLQTopic = fmt.Sprintf("service-rpc-v04-runtime-dlq-%d", suffix)
	producer, err := OpenKafkaProducer(context.Background(), cfg, cfg.MainTopic)
	if err != nil {
		t.Fatalf("OpenKafkaProducer() error = %v", err)
	}
	if err := producer.Publish(context.Background(), "runtime-check", []byte(`{"stage":"v0.4"}`)); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := producer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	// 同一组 topic 再开 producer 必须成功。一个 worker 会依次打开 main/retry/DLQ，
	// 第二次 ensure 收到 TopicAlreadyExists 是正常幂等路径。
	second, err := OpenKafkaProducer(context.Background(), cfg, cfg.RetryTopic)
	if err != nil {
		t.Fatalf("second OpenKafkaProducer() error = %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}
