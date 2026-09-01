package mq

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

type KafkaConfig struct {
	Brokers          []string
	ClientID         string
	GroupID          string
	Topics           []string
	OperationTimeout time.Duration
}

func (c KafkaConfig) ValidatePublisher() error {
	if len(c.Brokers) == 0 || strings.TrimSpace(c.ClientID) == "" || c.OperationTimeout <= 0 {
		return fmt.Errorf("Kafka brokers, client ID, and operation timeout are required")
	}
	return nil
}

func (c KafkaConfig) ValidateConsumer() error {
	if err := c.ValidatePublisher(); err != nil {
		return err
	}
	if strings.TrimSpace(c.GroupID) == "" || len(c.Topics) == 0 {
		return fmt.Errorf("Kafka consumer group and topics are required")
	}
	return nil
}

type KafkaClient struct {
	client  *kgo.Client
	timeout time.Duration
}

func NewPublisher(config KafkaConfig) (*KafkaClient, error) {
	if err := config.ValidatePublisher(); err != nil {
		return nil, err
	}
	client, err := kgo.NewClient(
		kgo.SeedBrokers(config.Brokers...), kgo.ClientID(config.ClientID),
		// AllISR ack + franz-go 默认幂等 producer：broker ack 前 Publish 不返回成功。
		// 这不能消除 ack 后进程崩溃的重复，只能与稳定 event_id 一起实现可靠 at-least-once。
		kgo.RequiredAcks(kgo.AllISRAcks()),
	)
	if err != nil {
		return nil, fmt.Errorf("create Kafka publisher: %w", err)
	}
	return &KafkaClient{client: client, timeout: config.OperationTimeout}, nil
}

func NewGroupClient(config KafkaConfig) (*KafkaClient, error) {
	if err := config.ValidateConsumer(); err != nil {
		return nil, err
	}
	client, err := kgo.NewClient(
		kgo.SeedBrokers(config.Brokers...), kgo.ClientID(config.ClientID),
		kgo.ConsumerGroup(config.GroupID), kgo.ConsumeTopics(config.Topics...),
		kgo.DisableAutoCommit(),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		// 同 group 内 partition 只分给一个实例；不同 group 各自维护 offset，因此都会收到完整事件流。
		// 单 partition 内按序处理，扩实例前 topic 必须有足够 partition，否则只增加空闲进程。
		kgo.Balancers(kgo.CooperativeStickyBalancer()),
	)
	if err != nil {
		return nil, fmt.Errorf("create Kafka group client: %w", err)
	}
	return &KafkaClient{client: client, timeout: config.OperationTimeout}, nil
}

func (c *KafkaClient) Publish(ctx context.Context, message Message) error {
	if c == nil || c.client == nil || strings.TrimSpace(message.Topic) == "" || len(message.Key) == 0 || len(message.Value) == 0 {
		return fmt.Errorf("invalid Kafka publish message")
	}
	commandContext, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	record := &kgo.Record{Topic: message.Topic, Key: message.Key, Value: message.Value}
	for key, value := range message.Headers {
		record.Headers = append(record.Headers, kgo.RecordHeader{Key: key, Value: value})
	}
	if err := c.client.ProduceSync(commandContext, record).FirstErr(); err != nil {
		return fmt.Errorf("publish Kafka message: %w", err)
	}
	return nil
}

func (c *KafkaClient) Fetch(ctx context.Context) (Message, error) {
	fetches := c.client.PollRecords(ctx, 1)
	records := fetches.Records()
	if len(records) == 0 {
		if fetchErrors := fetches.Errors(); len(fetchErrors) > 0 {
			return Message{}, fmt.Errorf("fetch Kafka message: %w", fetchErrors[0].Err)
		}
		if err := ctx.Err(); err != nil {
			return Message{}, err
		}
		return Message{}, fmt.Errorf("Kafka poll returned no record")
	}
	record := records[0]
	headers := make(map[string][]byte, len(record.Headers))
	for _, header := range record.Headers {
		headers[header.Key] = append([]byte(nil), header.Value...)
	}
	return Message{Topic: record.Topic, Key: record.Key, Value: record.Value, Headers: headers, Partition: record.Partition, Offset: record.Offset}, nil
}

func (c *KafkaClient) Commit(ctx context.Context, message Message) error {
	commandContext, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	record := &kgo.Record{Topic: message.Topic, Partition: message.Partition, Offset: message.Offset}
	if err := c.client.CommitRecords(commandContext, record); err != nil {
		return fmt.Errorf("commit Kafka offset: %w", err)
	}
	return nil
}

func (c *KafkaClient) Ping(ctx context.Context) error {
	commandContext, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.client.Ping(commandContext)
}

func (c *KafkaClient) Close() { c.client.Close() }

var _ Publisher = (*KafkaClient)(nil)
var _ Source = (*KafkaClient)(nil)
