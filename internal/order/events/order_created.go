// Package events defines versioned order domain events independent of a Kafka client.
package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"service_rpc/internal/order"
)

const (
	OrderCreatedEventType = "order.created"
	OrderCreatedVersion   = 1
	OrderSourceNormal     = "NORMAL"
	OrderSourceSeckill    = "SECKILL"
)

var (
	ErrInvalidEvent       = errors.New("invalid order event")
	ErrUnsupportedVersion = errors.New("unsupported order event schema version")
)

// OrderCreatedV1 只包含下游通知/结果投影所需的最小订单事实。
// 禁止加入密码、JWT、用户资料或订单明细；事件会被多个 group 长期保留，字段越多泄露面越大。
type OrderCreatedV1 struct {
	EventID         string `json:"event_id"`
	EventType       string `json:"event_type"`
	SchemaVersion   int    `json:"schema_version"`
	OrderNo         string `json:"order_no"`
	UserID          uint64 `json:"user_id"`
	OrderSource     string `json:"order_source"`
	TotalAmountCent int64  `json:"total_amount_cent"`
	CreatedAtMS     int64  `json:"created_at_ms"`
}

// StableEventID 对“一笔订单只产生一个 order.created”使用确定性业务 ID。
// relay 重试和 broker ack 丢失时必须复用它，消费者才能用唯一键幂等，而不是把 Kafka offset 当业务 ID。
func StableEventID(orderNo string) string {
	return OrderCreatedEventType + ".v1:" + strings.TrimSpace(orderNo)
}

func NewOrderCreatedV1(created order.Order, source string) (OrderCreatedV1, error) {
	event := OrderCreatedV1{
		EventID: StableEventID(created.OrderNo), EventType: OrderCreatedEventType, SchemaVersion: OrderCreatedVersion,
		OrderNo: strings.TrimSpace(created.OrderNo), UserID: created.UserID, OrderSource: strings.ToUpper(strings.TrimSpace(source)),
		TotalAmountCent: created.TotalAmountCent, CreatedAtMS: created.CreatedAt.UTC().UnixMilli(),
	}
	if err := event.Validate(); err != nil {
		return OrderCreatedV1{}, err
	}
	return event, nil
}

func (e OrderCreatedV1) Validate() error {
	if e.EventType != OrderCreatedEventType || e.SchemaVersion != OrderCreatedVersion {
		return ErrUnsupportedVersion
	}
	if strings.TrimSpace(e.EventID) == "" || strings.TrimSpace(e.OrderNo) == "" || e.EventID != StableEventID(e.OrderNo) || e.UserID == 0 || e.TotalAmountCent < 0 || e.CreatedAtMS <= 0 {
		return ErrInvalidEvent
	}
	if e.OrderSource != OrderSourceNormal && e.OrderSource != OrderSourceSeckill {
		return ErrInvalidEvent
	}
	return nil
}

func EncodeOrderCreatedV1(event OrderCreatedV1) ([]byte, error) {
	if err := event.Validate(); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("encode order.created.v1: %w", err)
	}
	return encoded, nil
}

func DecodeOrderCreatedV1(value []byte) (OrderCreatedV1, error) {
	// 先读取版本头，未知版本明确进入 DLQ，不能“尽量按 v1 猜”；猜错金额/身份比停止消费更危险。
	var header struct {
		EventType     string `json:"event_type"`
		SchemaVersion int    `json:"schema_version"`
	}
	if err := json.Unmarshal(value, &header); err != nil {
		return OrderCreatedV1{}, fmt.Errorf("decode order event header: %w", ErrInvalidEvent)
	}
	if header.EventType != OrderCreatedEventType || header.SchemaVersion != OrderCreatedVersion {
		return OrderCreatedV1{}, ErrUnsupportedVersion
	}
	var event OrderCreatedV1
	if err := json.Unmarshal(value, &event); err != nil {
		return OrderCreatedV1{}, fmt.Errorf("decode order.created.v1: %w", ErrInvalidEvent)
	}
	if err := event.Validate(); err != nil {
		return OrderCreatedV1{}, err
	}
	return event, nil
}

func CreatedAt(event OrderCreatedV1) time.Time { return time.UnixMilli(event.CreatedAtMS).UTC() }
