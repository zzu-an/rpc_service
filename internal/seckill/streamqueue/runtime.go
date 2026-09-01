// Package streamqueue 实现 v0.4.2 Redis Stream 消费组运行时。
package streamqueue

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	redis "github.com/redis/go-redis/v9"

	"service_rpc/internal/seckill"
	"service_rpc/internal/seckill/redisgate"
)

const completeScript = `
redis.call('XACK', KEYS[1], ARGV[1], ARGV[2])
redis.call('XDEL', KEYS[1], ARGV[2])
redis.call('HDEL', KEYS[2], ARGV[2])
return 1
`

const failScript = `
local attempts = redis.call('HINCRBY', KEYS[2], ARGV[2], 1)
local terminal = ARGV[6] == '1' or attempts >= tonumber(ARGV[7])
local ttl = redis.call('PTTL', KEYS[3])
if ttl < 1 then
  ttl = tonumber(ARGV[8])
end
redis.call('PEXPIRE', KEYS[2], ttl)
if not terminal then
  return {attempts, 0}
end

redis.call('XADD', KEYS[4], '*',
  'schema_version', '1',
  'event_type', 'seckill.order.failed',
  'source_message_id', ARGV[2],
  'order_no', ARGV[3],
  'user_id', ARGV[4],
  'error_code', ARGV[5],
  'failed_at_ms', ARGV[9],
  'attempts', attempts)
if ARGV[3] ~= '' and ARGV[4] ~= '0' then
  redis.call('HSET', KEYS[3], ARGV[3], ARGV[4] .. '|FAILED')
end
redis.call('PEXPIRE', KEYS[4], ttl)
redis.call('XACK', KEYS[1], ARGV[1], ARGV[2])
redis.call('XDEL', KEYS[1], ARGV[2])
redis.call('HDEL', KEYS[2], ARGV[2])
return {attempts, 1}
`

type ItemSource interface {
	ListStreamItemIDs(context.Context) ([]uint64, error)
}

type TaskProcessor interface {
	ProcessStreamTask(context.Context, uint64, uint64, uint64, string, time.Time) (seckill.PurchaseResult, error)
}

type RuntimeConfig struct {
	ConsumerGroup       string
	ConsumerPrefix      string
	ConsumerConcurrency int
	BatchSize           int64
	Block               time.Duration
	ClaimIdle           time.Duration
	DiscoveryInterval   time.Duration
	ShutdownTimeout     time.Duration
	MaxDeliveries       int
	Retention           time.Duration
}

func (c RuntimeConfig) Validate() error {
	if strings.TrimSpace(c.ConsumerGroup) == "" || strings.TrimSpace(c.ConsumerPrefix) == "" {
		return fmt.Errorf("stream consumer group and prefix are required")
	}
	if c.ConsumerConcurrency <= 0 || c.BatchSize <= 0 || c.MaxDeliveries <= 0 {
		return fmt.Errorf("stream concurrency, batch size, and max deliveries must be positive")
	}
	if c.Block <= 0 || c.ClaimIdle <= 0 || c.DiscoveryInterval <= 0 || c.ShutdownTimeout <= 0 || c.Retention <= 0 {
		return fmt.Errorf("stream durations must be positive")
	}
	return nil
}

type Runtime struct {
	client    *redis.Client
	source    ItemSource
	processor TaskProcessor
	cfg       RuntimeConfig
	instance  string
	active    atomic.Int64
	peak      atomic.Int64
}

func NewRuntime(client *redis.Client, source ItemSource, processor TaskProcessor, cfg RuntimeConfig) (*Runtime, error) {
	if client == nil || source == nil || processor == nil {
		return nil, fmt.Errorf("stream client, item source, and processor are required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Runtime{client: client, source: source, processor: processor, cfg: cfg, instance: strconv.FormatInt(time.Now().UnixNano(), 36)}, nil
}

func (r *Runtime) PeakConcurrency() int64 {
	if r == nil {
		return 0
	}
	return r.peak.Load()
}

func (r *Runtime) Run(ctx context.Context) error {
	if r == nil {
		return fmt.Errorf("stream runtime is nil")
	}
	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	defer cancelWorkers()
	sem := make(chan struct{}, r.cfg.ConsumerConcurrency)
	started := make(map[uint64]struct{})
	var workers sync.WaitGroup
	discover := func() error {
		ids, err := r.source.ListStreamItemIDs(ctx)
		if err != nil {
			return err
		}
		for _, itemID := range ids {
			if itemID == 0 {
				continue
			}
			if _, ok := started[itemID]; ok {
				continue
			}
			started[itemID] = struct{}{}
			// 每个 item 启动 N 个阻塞 reader，真正进入 MySQL 的总并发仍由共享 semaphore
			// 限制。增加 goroutine 不会突破连接池预算；Cluster 下单 item 仍受单 shard 上限。
			for index := 0; index < r.cfg.ConsumerConcurrency; index++ {
				consumer := fmt.Sprintf("%s-%s-%d-%d", r.cfg.ConsumerPrefix, r.instance, itemID, index)
				workers.Add(1)
				go func(itemID uint64, consumer string) {
					defer workers.Done()
					r.consumeItem(workerCtx, itemID, consumer, sem)
				}(itemID, consumer)
			}
		}
		return nil
	}

	if err := discover(); err != nil {
		return fmt.Errorf("discover stream items: %w", err)
	}
	ticker := time.NewTicker(r.cfg.DiscoveryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			cancelWorkers()
			done := make(chan struct{})
			go func() { workers.Wait(); close(done) }()
			timer := time.NewTimer(r.cfg.ShutdownTimeout)
			defer timer.Stop()
			select {
			case <-done:
				return nil
			case <-timer.C:
				// Redis/MySQL 调用已收到 cancel；若驱动仍不返回，不能假装安全退出。
				return fmt.Errorf("stream worker shutdown timed out")
			}
		case <-ticker.C:
			if err := discover(); err != nil && ctx.Err() == nil {
				// 发现失败不杀死已有 item worker；短暂 MySQL 波动不应让已知 Stream 停摆。
				continue
			}
		}
	}
}

func (r *Runtime) consumeItem(ctx context.Context, itemID uint64, consumer string, sem chan struct{}) {
	streamKey := redisgate.StreamKey(itemID)
	for ctx.Err() == nil {
		if err := r.ensureGroup(ctx, streamKey); err != nil {
			if !wait(ctx, r.cfg.DiscoveryInterval) {
				return
			}
			continue
		}

		claimed, _, err := r.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
			Stream: streamKey, Group: r.cfg.ConsumerGroup, Consumer: consumer,
			MinIdle: r.cfg.ClaimIdle, Start: "0-0", Count: r.cfg.BatchSize,
		}).Result()
		if err == nil && len(claimed) > 0 {
			r.processBatch(ctx, itemID, claimed, sem)
			continue
		}
		if err != nil && !errors.Is(err, redis.Nil) && ctx.Err() == nil && !isNoGroup(err) {
			if !wait(ctx, 100*time.Millisecond) {
				return
			}
			continue
		}

		streams, err := r.client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group: r.cfg.ConsumerGroup, Consumer: consumer,
			Streams: []string{streamKey, ">"}, Count: r.cfg.BatchSize, Block: r.cfg.Block,
		}).Result()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if !wait(ctx, 100*time.Millisecond) {
				return
			}
			continue
		}
		for _, stream := range streams {
			r.processBatch(ctx, itemID, stream.Messages, sem)
		}
	}
}

func (r *Runtime) ensureGroup(ctx context.Context, streamKey string) error {
	err := r.client.XGroupCreate(ctx, streamKey, r.cfg.ConsumerGroup, "0").Err()
	if err == nil || redis.HasErrorPrefix(err, "BUSYGROUP") {
		return nil
	}
	// 不用 MKSTREAM：worker 先启动时创建的空 Stream 没有活动 TTL，会永久留下 key。
	// 第一条 Lua XADD 创建 key 后，下一轮从 0 建组即可读取入队早于建组的消息。
	return err
}

func (r *Runtime) processBatch(ctx context.Context, itemID uint64, messages []redis.XMessage, sem chan struct{}) {
	for _, message := range messages {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		r.trackActive(1)
		r.processOne(ctx, itemID, message)
		r.trackActive(-1)
		<-sem
	}
}

func (r *Runtime) processOne(ctx context.Context, itemID uint64, message redis.XMessage) {
	task, decodeErr := decodeTask(itemID, message)
	if decodeErr != nil {
		_ = r.recordFailure(ctx, itemID, task, stableErrorCode(decodeErr), true)
		return
	}
	_, err := r.processor.ProcessStreamTask(ctx, task.UserID, task.ActivityID, task.ItemID, task.OrderNo, task.ReservedAt)
	if err == nil {
		_ = r.complete(ctx, itemID, task.MessageID)
		return
	}
	_ = r.recordFailure(ctx, itemID, task, stableErrorCode(err), isTerminal(err))
}

func (r *Runtime) complete(ctx context.Context, itemID uint64, messageID string) error {
	return r.client.Eval(ctx, completeScript,
		[]string{redisgate.StreamKey(itemID), redisgate.StreamRetriesKey(itemID)},
		r.cfg.ConsumerGroup, messageID,
	).Err()
}

func (r *Runtime) recordFailure(ctx context.Context, itemID uint64, task Task, code string, terminal bool) error {
	terminalFlag := 0
	if terminal {
		terminalFlag = 1
	}
	return r.client.Eval(ctx, failScript, []string{
		redisgate.StreamKey(itemID), redisgate.StreamRetriesKey(itemID),
		redisgate.StreamResultsKey(itemID), redisgate.StreamDLQKey(itemID),
	}, r.cfg.ConsumerGroup, task.MessageID, task.OrderNo, task.UserID, code, terminalFlag,
		r.cfg.MaxDeliveries, r.cfg.Retention.Milliseconds(), time.Now().UTC().UnixMilli()).Err()
}

func (r *Runtime) trackActive(delta int64) {
	active := r.active.Add(delta)
	for active > r.peak.Load() && !r.peak.CompareAndSwap(r.peak.Load(), active) {
	}
}

func isTerminal(err error) bool {
	return errors.Is(err, seckill.ErrInvalidArgument) || errors.Is(err, seckill.ErrItemNotFound) ||
		errors.Is(err, seckill.ErrUnavailable) || errors.Is(err, seckill.ErrOutOfStock) ||
		errors.Is(err, ErrInvalidStreamMessage) || errors.Is(err, ErrUnsupportedStreamMessage) ||
		errors.Is(err, ErrRPCRequestRejected)
}

func stableErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrUnsupportedStreamMessage):
		return "unsupported_message"
	case errors.Is(err, ErrInvalidStreamMessage):
		return "invalid_message"
	case errors.Is(err, seckill.ErrItemNotFound):
		return "item_not_found"
	case errors.Is(err, seckill.ErrUnavailable):
		return "activity_unavailable"
	case errors.Is(err, seckill.ErrOutOfStock):
		return "mysql_out_of_stock"
	case errors.Is(err, context.DeadlineExceeded):
		return "dependency_timeout"
	case errors.Is(err, ErrReservedWithoutOrder):
		return "reserved_without_order"
	case errors.Is(err, ErrRPCDependencyUnavailable):
		return "dependency_unavailable"
	case errors.Is(err, ErrRPCRequestRejected):
		return "rpc_request_rejected"
	default:
		return "temporary_failure"
	}
}

func isNoGroup(err error) bool {
	return err != nil && (redis.HasErrorPrefix(err, "NOGROUP") || strings.Contains(err.Error(), "requires the key to exist"))
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
