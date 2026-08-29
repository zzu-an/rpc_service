// Package mq 管理 Kafka 连接和确认发布，不包含秒杀消息格式或重试策略。
// platform 层只提供运输能力，避免业务层直接依赖 go-queue/kafka-go 具体类型。
package mq

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/segmentio/kafka-go"

	"service_rpc/internal/config"
)

// Producer 是 relay/retry/DLQ 共用的最小确认发布边界。
type Producer interface {
	Publish(ctx context.Context, key string, value []byte) error
	Close() error
}

type KafkaProducer struct {
	topic   string
	timeout config.KafkaConfig
	writer  *kafka.Writer
}

// OpenKafkaProducer 在创建 pusher 前主动连接一个 broker，尽早暴露地址错误。
// Kafka writer 本身是惰性连接；如果省略探测，配置错误会拖到第一条业务 job 才出现，
// 使“启动错误”看起来像“消息偶发失败”。探测成功不代表集群永远可用，Publish 仍需处理错误。
func OpenKafkaProducer(ctx context.Context, cfg config.KafkaConfig, topic string) (*KafkaProducer, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(topic) == "" {
		return nil, fmt.Errorf("kafka producer topic is required")
	}

	probeCtx, cancel := context.WithTimeout(ctx, cfg.OperationTimeout())
	defer cancel()
	conn, err := kafka.DialContext(probeCtx, "tcp", cfg.Brokers[0])
	if err != nil {
		// 错误只包含 broker 地址，不格式化整个 cfg；未来即使配置加入认证字段也不会泄露。
		return nil, fmt.Errorf("connect kafka broker %s: %w", cfg.Brokers[0], err)
	}
	if err := conn.Close(); err != nil {
		return nil, fmt.Errorf("close kafka probe: %w", err)
	}
	if cfg.AllowAutoTopicCreation {
		if err := ensureTopics(probeCtx, cfg); err != nil {
			return nil, err
		}
	}

	// kq.WithSyncPush 只关闭客户端 ChunkExecutor，不会修改 kafka.Writer 默认的
	// RequiredAcks=RequireNone；那种配置即使返回 nil，也不能证明 broker 已持久化消息。
	// 可靠 outbox 必须显式 RequireAll。BatchSize=1 让单条同步写立即发出，避免每条 job
	// 等默认 batch timeout；Hash 保证同一 order_no 的重复事件进入同一 partition。
	writer := &kafka.Writer{
		Addr: kafka.TCP(cfg.Brokers...), Topic: topic, Balancer: &kafka.Hash{},
		RequiredAcks: kafka.RequireAll, Async: false, BatchSize: 1,
		AllowAutoTopicCreation: cfg.AllowAutoTopicCreation, WriteTimeout: cfg.OperationTimeout(),
	}
	return &KafkaProducer{topic: topic, timeout: cfg, writer: writer}, nil
}

func ensureTopics(ctx context.Context, cfg config.KafkaConfig) error {
	bootstrap, err := kafka.DialContext(ctx, "tcp", cfg.Brokers[0])
	if err != nil {
		return fmt.Errorf("connect kafka bootstrap for topic creation: %w", err)
	}
	controller, err := bootstrap.Controller()
	_ = bootstrap.Close()
	if err != nil {
		return fmt.Errorf("discover kafka controller: %w", err)
	}
	controllerAddr := net.JoinHostPort(controller.Host, strconv.Itoa(controller.Port))
	// 开发模式显式创建三个 topic，而不是依赖 broker 的 auto.create.topics.enable。
	// partition 数决定 consumer group 的并行上限；把它固化在配置中才能让压测结论可复现。
	topics := []kafka.TopicConfig{
		{Topic: cfg.MainTopic, NumPartitions: cfg.TopicPartitions, ReplicationFactor: 1},
		{Topic: cfg.RetryTopic, NumPartitions: cfg.TopicPartitions, ReplicationFactor: 1},
		{Topic: cfg.DLQTopic, NumPartitions: cfg.TopicPartitions, ReplicationFactor: 1},
	}
	client := &kafka.Client{Addr: kafka.TCP(controllerAddr), Timeout: cfg.OperationTimeout()}
	response, err := client.CreateTopics(ctx, &kafka.CreateTopicsRequest{Topics: topics})
	if err != nil {
		return fmt.Errorf("ensure kafka topics: %w", err)
	}
	for topic, topicErr := range response.Errors {
		// API 与 worker 会分别打开 main/retry/DLQ producer，每个 producer 都会执行
		// ensure。TopicAlreadyExists 是幂等成功，不应让第二个 producer 启动失败；权限、
		// 副本数等其他错误仍必须原样失败，不能把真正配置问题吞掉。
		if topicErr != nil && !errors.Is(topicErr, kafka.TopicAlreadyExists) {
			return fmt.Errorf("ensure kafka topic %s: %w", topic, topicErr)
		}
	}
	return nil
}

func (p *KafkaProducer) Publish(ctx context.Context, key string, value []byte) error {
	if p == nil || p.writer == nil {
		return fmt.Errorf("kafka producer is not initialized")
	}
	if strings.TrimSpace(key) == "" || len(value) == 0 {
		return fmt.Errorf("kafka key and value are required")
	}
	publishCtx, cancel := context.WithTimeout(ctx, p.timeout.OperationTimeout())
	defer cancel()

	// sync push 只有收到 broker ack 才返回 nil。若 context 超时，调用方仍不能据此断言
	// broker 一定没写入；正确恢复是让 relay 重发同一 event_id，并由消费者幂等吸收重复。
	if err := p.writer.WriteMessages(publishCtx, kafka.Message{Key: []byte(key), Value: value}); err != nil {
		return fmt.Errorf("publish kafka topic %s: %w", p.topic, err)
	}
	return nil
}

func (p *KafkaProducer) Close() error {
	if p == nil || p.writer == nil {
		return nil
	}
	// Writer 持有连接和后台资源；优雅退出不 Close 会让测试泄漏连接，也可能让进程退出时
	// 尚未完成的网络请求没有清晰边界。当前使用同步发送，不依赖 Close 才刷出业务消息。
	return p.writer.Close()
}
