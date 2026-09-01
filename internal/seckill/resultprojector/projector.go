// Package resultprojector projects committed seckill orders into the short-lived Redis result view.
package resultprojector

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"

	"service_rpc/internal/order/events"
	"service_rpc/internal/platform/mq"
	"service_rpc/internal/seckill"
	"service_rpc/internal/seckill/redisgate"
)

var ErrProjectionConflict = errors.New("seckill result projection ownership conflict")

const projectSuccessScript = `
local projected = redis.call('HGET', KEYS[2], ARGV[1])
if projected then
  if projected == ARGV[2] then return 0 end
  return -3
end
local current = redis.call('HGET', KEYS[1], ARGV[2])
if not current then
  -- 结果已过保留期时无需重建缓存；order-rpc 仍是事实源。记录 ledger 避免重放做无用功。
  redis.call('HSET', KEYS[2], ARGV[1], ARGV[2])
  redis.call('PEXPIRE', KEYS[2], ARGV[4])
  return 2
end
local expectedPrefix = ARGV[3] .. '|'
if string.sub(current, 1, string.len(expectedPrefix)) ~= expectedPrefix then return -2 end
redis.call('HSET', KEYS[1], ARGV[2], ARGV[3] .. '|SUCCEEDED')
redis.call('HSET', KEYS[2], ARGV[1], ARGV[2])
local ttl = redis.call('PTTL', KEYS[1])
if ttl < 1 then ttl = tonumber(ARGV[4]) end
redis.call('PEXPIRE', KEYS[2], ttl)
return 1
`

type Projector struct {
	client    *redis.Client
	timeout   time.Duration
	retention time.Duration
}

func New(client *redis.Client, timeout, retention time.Duration) (*Projector, error) {
	if client == nil || timeout <= 0 || retention <= 0 {
		return nil, fmt.Errorf("projector Redis client, timeout, and retention are required")
	}
	return &Projector{client: client, timeout: timeout, retention: retention}, nil
}

func (p *Projector) Project(ctx context.Context, event events.OrderCreatedV1) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if event.OrderSource != events.OrderSourceSeckill {
		return nil
	}
	itemID, ok := seckill.StreamOrderItemID(event.OrderNo)
	if !ok {
		return ErrProjectionConflict
	}
	commandContext, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	result, err := p.client.Eval(commandContext, projectSuccessScript, []string{
		redisgate.StreamResultsKey(itemID), projectionLedgerKey(itemID),
	}, event.EventID, strings.TrimSpace(event.OrderNo), strconv.FormatUint(event.UserID, 10), p.retention.Milliseconds()).Int()
	if err != nil {
		return fmt.Errorf("project seckill result: %w", err)
	}
	if result < 0 {
		// 订单事件 user/order 与原 QUEUED 所有者不一致说明消息或缓存被污染；绝不能覆盖后重试。
		return ErrProjectionConflict
	}
	return nil
}

func projectionLedgerKey(itemID uint64) string {
	return fmt.Sprintf("seckill:v05:{item:%d}:projected-events", itemID)
}

func NewOrderCreatedHandler(projector *Projector) mq.Handler {
	return func(ctx context.Context, message mq.Message) error {
		event, err := events.DecodeOrderCreatedV1(message.Value)
		if err != nil {
			return mq.Permanent(err)
		}
		if err := projector.Project(ctx, event); err != nil {
			if errors.Is(err, ErrProjectionConflict) {
				return mq.Permanent(err)
			}
			return err
		}
		return nil
	}
}
