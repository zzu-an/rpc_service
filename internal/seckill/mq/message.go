// Package mq 定义秒杀异步消息契约和处理器。
// 它依赖领域类型但不依赖 HTTP；Kafka 运输细节通过接口注入，避免消息格式和客户端库绑死。
package mq

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	OrderRequestedEventType = "seckill.order.requested"
	OrderRequestedSchemaV1  = 1
)

var (
	ErrInvalidMessage     = errors.New("invalid seckill message")
	ErrUnsupportedMessage = errors.New("unsupported seckill message")
)

// JobFactory 是领域 Service 的消息工厂实现。它只冻结事件，不发送 Kafka。
type JobFactory struct{}

func NewJobFactory() *JobFactory { return &JobFactory{} }

func (f *JobFactory) Build(orderNo string, userID, itemID uint64, reservedAt time.Time) (string, []byte, error) {
	if f == nil {
		return "", nil, ErrInvalidMessage
	}
	// occurred_at 必须对同一幂等请求保持稳定。Redis 重放会返回第一次冻结的 reserved_at；
	// 若这里使用每次 HTTP 重试的 wall clock，相同 event_id 会生成不同 payload，持久化层
	// 只能把合法重试误判为消息漂移。当前事件就是“获得资格”，因此两个时间语义相同。
	// 面试/优化点：幂等不只是复用业务键，键对应的不可变载荷也必须可确定性重建。
	event, err := NewOrderRequestedV1(orderNo, userID, itemID, reservedAt, reservedAt)
	if err != nil {
		return "", nil, err
	}
	payload, err := EncodeOrderRequestedV1(event)
	if err != nil {
		return "", nil, err
	}
	return event.EventID, payload, nil
}

type OrderRequestedV1 struct {
	EventID       string `json:"event_id"`
	EventType     string `json:"event_type"`
	SchemaVersion int    `json:"schema_version"`
	OrderNo       string `json:"order_no"`
	UserID        uint64 `json:"user_id"`
	ItemID        uint64 `json:"item_id"`
	ReservedAtMS  int64  `json:"reserved_at_ms"`
	OccurredAtMS  int64  `json:"occurred_at_ms"`
	Attempt       int    `json:"attempt"`
}

func NewOrderRequestedV1(orderNo string, userID, itemID uint64, reservedAt, occurredAt time.Time) (OrderRequestedV1, error) {
	event := OrderRequestedV1{
		EventID:       EventID(orderNo),
		EventType:     OrderRequestedEventType,
		SchemaVersion: OrderRequestedSchemaV1,
		OrderNo:       strings.TrimSpace(orderNo),
		UserID:        userID,
		ItemID:        itemID,
		ReservedAtMS:  reservedAt.UTC().UnixMilli(),
		OccurredAtMS:  occurredAt.UTC().UnixMilli(),
	}
	if err := event.Validate(); err != nil {
		return OrderRequestedV1{}, err
	}
	return event, nil
}

func EventID(orderNo string) string {
	return "seckill-order-requested:" + strings.TrimSpace(orderNo)
}

// MessageKey 使用 order_no，而不是 item_id。
// 同一订单的重复消息会稳定落到相同 partition；不同订单可跨 partition 并行。当前 MySQL
// 条件扣减不依赖 item 内严格顺序，若按热点 item 分区，单个爆款会被人为限制为单 partition
// 吞吐。面试常见误区是“需要顺序就按业务 ID 分区”，但应先证明业务真的依赖该顺序。
func (e OrderRequestedV1) MessageKey() string { return e.OrderNo }

func (e OrderRequestedV1) ReservedAt() time.Time {
	return time.UnixMilli(e.ReservedAtMS).UTC()
}

func (e OrderRequestedV1) Validate() error {
	if e.SchemaVersion != OrderRequestedSchemaV1 || e.EventType != OrderRequestedEventType {
		return ErrUnsupportedMessage
	}
	if strings.TrimSpace(e.OrderNo) == "" || e.EventID != EventID(e.OrderNo) || e.UserID == 0 || e.ItemID == 0 || e.ReservedAtMS <= 0 || e.OccurredAtMS <= 0 || e.Attempt < 0 {
		return ErrInvalidMessage
	}
	return nil
}

func EncodeOrderRequestedV1(event OrderRequestedV1) ([]byte, error) {
	if err := event.Validate(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("encode order requested message: %w", err)
	}
	return payload, nil
}

func DecodeOrderRequestedV1(payload []byte) (OrderRequestedV1, error) {
	if len(payload) == 0 || !json.Valid(payload) {
		return OrderRequestedV1{}, ErrInvalidMessage
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	// 不启用 DisallowUnknownFields：v1 允许生产者向后兼容地增加可选字段；消费者只根据
	// schema_version 判断能否处理。反过来，未知版本必须拒绝，不能“尽量猜”导致错误落单。
	var event OrderRequestedV1
	if err := decoder.Decode(&event); err != nil {
		return OrderRequestedV1{}, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	if err := event.Validate(); err != nil {
		return OrderRequestedV1{}, err
	}
	return event, nil
}
