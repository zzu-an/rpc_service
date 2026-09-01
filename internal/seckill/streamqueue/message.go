package streamqueue

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"
)

const (
	streamSchemaV1 = "1"
	streamEventV1  = "seckill.order.requested"
)

var (
	ErrInvalidStreamMessage     = errors.New("invalid seckill stream message")
	ErrUnsupportedStreamMessage = errors.New("unsupported seckill stream message")
)

type Task struct {
	MessageID  string
	OrderNo    string
	UserID     uint64
	ActivityID uint64
	ItemID     uint64
	ReservedAt time.Time
}

func decodeTask(expectedItemID uint64, message redis.XMessage) (Task, error) {
	schema, ok := valueString(message.Values["schema_version"])
	if !ok || schema != streamSchemaV1 {
		return Task{MessageID: message.ID}, ErrUnsupportedStreamMessage
	}
	eventType, ok := valueString(message.Values["event_type"])
	if !ok || eventType != streamEventV1 {
		return Task{MessageID: message.ID}, ErrUnsupportedStreamMessage
	}
	orderNo, orderOK := valueString(message.Values["order_no"])
	userText, userOK := valueString(message.Values["user_id"])
	activityText, activityOK := valueString(message.Values["activity_id"])
	itemText, itemOK := valueString(message.Values["item_id"])
	reservedText, reservedOK := valueString(message.Values["reserved_at_ms"])
	userID, userErr := strconv.ParseUint(userText, 10, 64)
	activityID, activityErr := strconv.ParseUint(activityText, 10, 64)
	itemID, itemErr := strconv.ParseUint(itemText, 10, 64)
	reservedMS, reservedErr := strconv.ParseInt(reservedText, 10, 64)
	if message.ID == "" || !orderOK || !userOK || !activityOK || !itemOK || !reservedOK || strings.TrimSpace(orderNo) == "" || userErr != nil || activityErr != nil || itemErr != nil || reservedErr != nil || userID == 0 || activityID == 0 || itemID == 0 || reservedMS <= 0 {
		return Task{MessageID: message.ID, OrderNo: strings.TrimSpace(orderNo), UserID: userID}, ErrInvalidStreamMessage
	}
	if itemID != expectedItemID {
		// Stream key 本身属于 item。消息字段与 key 不一致通常意味着伪造或代码 bug，不能
		// 相信 payload 去扣另一个 item 的库存，否则队列就变成越权入口。
		return Task{MessageID: message.ID, OrderNo: strings.TrimSpace(orderNo), UserID: userID, ItemID: itemID}, ErrInvalidStreamMessage
	}
	return Task{
		MessageID: message.ID, OrderNo: strings.TrimSpace(orderNo), UserID: userID,
		ActivityID: activityID, ItemID: itemID, ReservedAt: time.UnixMilli(reservedMS).UTC(),
	}, nil
}

func valueString(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case []byte:
		return string(typed), true
	case int64:
		return strconv.FormatInt(typed, 10), true
	case uint64:
		return strconv.FormatUint(typed, 10), true
	default:
		return fmt.Sprint(value), value != nil
	}
}
