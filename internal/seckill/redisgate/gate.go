// Package redisgate 实现 v0.3 秒杀 Redis 准入层。
// 它只预留进入 MySQL 的资格；订单是否成功仍以 MySQL 本地事务为准。
package redisgate

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	redis "github.com/redis/go-redis/v9"

	"service_rpc/internal/seckill"
)

const (
	decisionReserved    int64 = 1
	decisionReplayed    int64 = 2
	decisionNotReady    int64 = -1
	decisionUnavailable int64 = -2
	decisionSoldOut     int64 = -3
)

// reserveScript 故意保持 O(1)，只访问两个同 slot Hash。
//
// 面试常问“Lua 为什么原子”：Redis 在同一 shard 上串行执行脚本，脚本执行期间不会插入
// 其他命令；但脚本也会阻塞该 shard，所以绝不能在这里扫描 buyers、调用 KEYS 或写循环。
// 重复检查必须早于库存检查，否则库存变成 0 后，已经成功的用户无法取回第一次 orderNo。
const reserveScript = `
local ready = redis.call('HGET', KEYS[1], 'ready')
if ready ~= '1' then
  return {-1, ''}
end

local status = redis.call('HGET', KEYS[1], 'status')
local start_at = tonumber(redis.call('HGET', KEYS[1], 'start_at_ms'))
local end_at = tonumber(redis.call('HGET', KEYS[1], 'end_at_ms'))
local now = tonumber(ARGV[2])
if status ~= '1' or start_at == nil or end_at == nil or now < start_at or now >= end_at then
  return {-2, ''}
end

local existing = redis.call('HGET', KEYS[2], ARGV[1])
if existing then
  return {2, existing}
end

local stock = tonumber(redis.call('HGET', KEYS[1], 'stock'))
local expire_at = tonumber(redis.call('HGET', KEYS[1], 'expire_at_ms'))
if stock == nil or expire_at == nil then
  return {-1, ''}
end
if stock <= 0 then
  return {-3, ''}
end

redis.call('HINCRBY', KEYS[1], 'stock', -1)
redis.call('HSET', KEYS[2], ARGV[1], ARGV[3])
-- Redis 不保存空 Hash，因此 buyers 在第一次成功预留时才真正出现。
-- 使用 state 中的绝对过期时间，避免它和 state 因网络往返产生不同生命周期。
redis.call('PEXPIREAT', KEYS[2], expire_at)
return {1, ARGV[3]}
`

// reserveAndEnqueueScript 把资格判断、库存扣减、buyer 幂等标记和 XADD 放在一次
// Redis 执行中。四个 key 都含相同 `{item:<id>}`，因此未来 Cluster 模式不会 CROSSSLOT。
// 原子性只覆盖这个 Redis shard，不覆盖后续 MySQL；Stream consumer 仍必须接受重复处理。
const reserveAndEnqueueScript = `
local ready = redis.call('HGET', KEYS[1], 'ready')
if ready ~= '1' then
  return {-1, ''}
end

local existing = redis.call('HGET', KEYS[2], ARGV[1])
if existing then
  return {2, existing}
end

local status = redis.call('HGET', KEYS[1], 'status')
local start_at = tonumber(redis.call('HGET', KEYS[1], 'start_at_ms'))
local end_at = tonumber(redis.call('HGET', KEYS[1], 'end_at_ms'))
local now = tonumber(ARGV[2])
if status ~= '1' or start_at == nil or end_at == nil or now < start_at or now >= end_at then
  return {-2, ''}
end

local stock = tonumber(redis.call('HGET', KEYS[1], 'stock'))
local expire_at = tonumber(redis.call('HGET', KEYS[1], 'expire_at_ms'))
local activity_id = redis.call('HGET', KEYS[1], 'activity_id')
if stock == nil or expire_at == nil or activity_id == false then
  return {-1, ''}
end
if stock <= 0 then
  return {-3, ''}
end

redis.call('HINCRBY', KEYS[1], 'stock', -1)
redis.call('HSET', KEYS[2], ARGV[1], ARGV[3])
redis.call('HSET', KEYS[4], ARGV[3], ARGV[1] .. '|QUEUED')
redis.call('XADD', KEYS[3], '*',
  'schema_version', '1',
  'event_type', 'seckill.order.requested',
  'order_no', ARGV[3],
  'user_id', ARGV[1],
  'activity_id', activity_id,
  'item_id', ARGV[4],
  'reserved_at_ms', ARGV[2])

redis.call('PEXPIREAT', KEYS[2], expire_at)
local queue_expire_at = expire_at + tonumber(ARGV[5])
redis.call('PEXPIREAT', KEYS[3], queue_expire_at)
redis.call('PEXPIREAT', KEYS[4], queue_expire_at)
return {1, ARGV[3]}
`

const publishScript = `
redis.call('DEL', KEYS[1], KEYS[2])
redis.call('HSET', KEYS[1],
  'activity_id', ARGV[1],
  'sku_id', ARGV[2],
  'status', ARGV[3],
  'start_at_ms', ARGV[4],
  'end_at_ms', ARGV[5],
  'stock', ARGV[6],
  'generation', ARGV[7],
  'expire_at_ms', ARGV[8],
  'ready', '1')
redis.call('PEXPIREAT', KEYS[1], ARGV[8])
return 1
`

const (
	preheatGrace       = 5 * time.Minute
	preheatJitterRange = 30 * time.Second
)

type scriptRunner interface {
	ScriptLoad(ctx context.Context, script string) (string, error)
	EvalSha(ctx context.Context, sha string, keys []string, args ...any) (any, error)
	Eval(ctx context.Context, script string, keys []string, args ...any) (any, error)
	Del(ctx context.Context, keys ...string) (int64, error)
}

type redisScriptRunner struct {
	client *redis.Client
}

// ScriptLoad - 把 Lua 脚本上传到 Redis，返回 SHA1 哈希
func (r redisScriptRunner) ScriptLoad(ctx context.Context, script string) (string, error) {
	return r.client.ScriptLoad(ctx, script).Result()
}

// EvalSha - 用 SHA1 执行已缓存的脚本
func (r redisScriptRunner) EvalSha(ctx context.Context, sha string, keys []string, args ...any) (any, error) {
	return r.client.EvalSha(ctx, sha, keys, args...).Result()
}

// Eval 直接发送完整脚本并执行
func (r redisScriptRunner) Eval(ctx context.Context, script string, keys []string, args ...any) (any, error) {
	return r.client.Eval(ctx, script, keys, args...).Result()
}

// Del - 删除 key
func (r redisScriptRunner) Del(ctx context.Context, keys ...string) (int64, error) {
	return r.client.Del(ctx, keys...).Result()
}

type Gate struct {
	runner           scriptRunner
	client           *redis.Client
	operationTimeout time.Duration
	loadMu           sync.Mutex
	sha              string
	streamSHA        string
	streamRetention  time.Duration
}

var _ seckill.AdmissionGate = (*Gate)(nil)
var _ seckill.StreamAdmissionGate = (*Gate)(nil)
var _ seckill.StreamResultReader = (*Gate)(nil)
var _ seckill.ActivityCache = (*Gate)(nil)

const defaultStreamRetention = 24 * time.Hour

func New(client *redis.Client, operationTimeout time.Duration) (*Gate, error) {
	if client == nil {
		return nil, fmt.Errorf("redis gate client is required")
	}
	gate, err := newWithRunner(redisScriptRunner{client: client}, operationTimeout)
	if err != nil {
		return nil, err
	}
	gate.client = client
	return gate, nil
}

func newWithRunner(runner scriptRunner, operationTimeout time.Duration) (*Gate, error) {
	if runner == nil {
		return nil, fmt.Errorf("redis gate runner is required")
	}
	if operationTimeout <= 0 {
		return nil, fmt.Errorf("redis gate operation timeout must be positive")
	}
	return &Gate{runner: runner, operationTimeout: operationTimeout, streamRetention: defaultStreamRetention}, nil
}

func StateKey(itemID uint64) string {
	return fmt.Sprintf("seckill:v03:{item:%d}:state", itemID)
}

func BuyersKey(itemID uint64) string {
	return fmt.Sprintf("seckill:v03:{item:%d}:buyers", itemID)
}

func StreamKey(itemID uint64) string {
	return fmt.Sprintf("seckill:v042:{item:%d}:orders", itemID)
}

func StreamResultsKey(itemID uint64) string {
	return fmt.Sprintf("seckill:v042:{item:%d}:results", itemID)
}

func StreamRetriesKey(itemID uint64) string {
	return fmt.Sprintf("seckill:v042:{item:%d}:retries", itemID)
}

func StreamDLQKey(itemID uint64) string {
	return fmt.Sprintf("seckill:v042:{item:%d}:dlq", itemID)
}

func (g *Gate) SetStreamRetention(retention time.Duration) error {
	if g == nil || retention <= 0 {
		return seckill.ErrInvalidArgument
	}
	g.streamRetention = retention
	return nil
}

func (g *Gate) Reserve(ctx context.Context, input seckill.ReservationInput) (seckill.Reservation, error) {
	if input.UserID == 0 || input.ItemID == 0 || strings.TrimSpace(input.OrderNo) == "" || input.Now.IsZero() {
		return seckill.Reservation{}, seckill.ErrInvalidArgument
	}

	commandCtx, cancel := context.WithTimeout(ctx, g.operationTimeout)
	defer cancel()
	sha, err := g.scriptSHA(commandCtx)
	if err != nil {
		return seckill.Reservation{}, admissionFailure("load reserve script", err)
	}
	keys := []string{StateKey(input.ItemID), BuyersKey(input.ItemID)}
	args := []any{strconv.FormatUint(input.UserID, 10), input.Now.UTC().UnixMilli(), input.OrderNo}
	reply, err := g.runner.EvalSha(commandCtx, sha, keys, args...)
	if err != nil && isNoScript(err) {
		// Redis 重启或 SCRIPT FLUSH 会清空服务端缓存。只允许重载并重试一次；
		// 普通超时不能照此重试，因为客户端不知道第一次 Lua 是否已经完成，盲目重试会放大故障。
		sha, err = g.reloadScript(commandCtx)
		if err == nil {
			reply, err = g.runner.EvalSha(commandCtx, sha, keys, args...)
		}
	}
	if err != nil {
		return seckill.Reservation{}, admissionFailure("execute reserve script", err)
	}
	return parseReservation(reply)
}

func (g *Gate) ReserveAndEnqueue(ctx context.Context, input seckill.ReservationInput) (seckill.Reservation, error) {
	if input.UserID == 0 || input.ItemID == 0 || strings.TrimSpace(input.OrderNo) == "" || input.Now.IsZero() || g.streamRetention <= 0 {
		return seckill.Reservation{}, seckill.ErrInvalidArgument
	}
	commandCtx, cancel := context.WithTimeout(ctx, g.operationTimeout)
	defer cancel()
	sha, err := g.streamScriptSHA(commandCtx)
	if err != nil {
		return seckill.Reservation{}, admissionFailure("load stream reserve script", err)
	}
	keys := []string{StateKey(input.ItemID), BuyersKey(input.ItemID), StreamKey(input.ItemID), StreamResultsKey(input.ItemID)}
	args := []any{
		strconv.FormatUint(input.UserID, 10), input.Now.UTC().UnixMilli(), input.OrderNo,
		input.ItemID, g.streamRetention.Milliseconds(),
	}
	reply, err := g.runner.EvalSha(commandCtx, sha, keys, args...)
	if err != nil && isNoScript(err) {
		sha, err = g.reloadStreamScript(commandCtx)
		if err == nil {
			reply, err = g.runner.EvalSha(commandCtx, sha, keys, args...)
		}
	}
	if err != nil {
		// 超时后不能改用普通 Reserve 或单独 XADD：第一次脚本可能已经成功，只是响应丢失。
		// 正确恢复方式是同一用户重试，Lua 先读 buyer 并返回原 order_no。
		return seckill.Reservation{}, admissionFailure("execute stream reserve script", err)
	}
	return parseReservation(reply)
}

func (g *Gate) FindStreamResult(ctx context.Context, userID uint64, orderNo string) (seckill.AsyncResultStatus, error) {
	itemID, ok := seckill.StreamOrderItemID(orderNo)
	if g == nil || g.client == nil || userID == 0 || !ok {
		return "", seckill.ErrAsyncResultNotFound
	}
	commandCtx, cancel := context.WithTimeout(ctx, g.operationTimeout)
	defer cancel()
	value, err := g.client.HGet(commandCtx, StreamResultsKey(itemID), strings.TrimSpace(orderNo)).Result()
	if errors.Is(err, redis.Nil) {
		return "", seckill.ErrAsyncResultNotFound
	}
	if err != nil {
		return "", admissionFailure("read stream result", err)
	}
	parts := strings.Split(value, "|")
	if len(parts) != 2 || parts[0] != strconv.FormatUint(userID, 10) {
		// 不存在和所有者不匹配必须统一返回 not found，避免 order_no 枚举泄漏。
		return "", seckill.ErrAsyncResultNotFound
	}
	status := seckill.AsyncResultStatus(parts[1])
	// TASK-067B 的 projector 会把同一个 field 从 QUEUED 更新为 SUCCEEDED/FAILED。
	// seckill-rpc 只读投影，不据此断言订单事实；gateway 仍需先查 order-rpc。
	if status != seckill.AsyncResultQueued && status != seckill.AsyncResultSucceeded && status != seckill.AsyncResultFailed {
		return "", admissionFailure("parse stream result", fmt.Errorf("unknown status %q", parts[1]))
	}
	return status, nil
}

func (g *Gate) PublishActivity(ctx context.Context, snapshot seckill.PreheatSnapshot, now time.Time) (seckill.PreheatResult, error) {
	if snapshot.Activity.ID == 0 || snapshot.Activity.Status != seckill.StatusEnabled || len(snapshot.Items) == 0 || now.IsZero() || !now.Before(snapshot.Activity.StartAt.UTC()) {
		return seckill.PreheatResult{}, seckill.ErrInvalidArgument
	}
	result := seckill.PreheatResult{ActivityID: snapshot.Activity.ID}
	for _, item := range snapshot.Items {
		if item.ID == 0 || item.ActivityID != snapshot.Activity.ID || item.SKUID == 0 || item.InitialStock < 0 || item.AvailableStock < 0 || item.AvailableStock > item.InitialStock {
			return result, seckill.ErrInvalidArgument
		}
		expiresAt := itemExpiry(snapshot.Activity.EndAt.UTC(), item.ID)
		generation := fmt.Sprintf("%d:%d:%d", snapshot.Activity.ID, item.Version, snapshot.Activity.EndAt.UTC().UnixMicro())
		args := []any{
			snapshot.Activity.ID,
			item.SKUID,
			snapshot.Activity.Status,
			snapshot.Activity.StartAt.UTC().UnixMilli(),
			snapshot.Activity.EndAt.UTC().UnixMilli(),
			item.AvailableStock,
			generation,
			expiresAt.UnixMilli(),
		}
		commandCtx, cancel := context.WithTimeout(ctx, g.operationTimeout)
		_, err := g.runner.Eval(commandCtx, publishScript, []string{StateKey(item.ID), BuyersKey(item.ID)}, args...)
		cancel()
		if err != nil {
			// 已完成 item 保持 ready，未完成 item 保持 fail closed；调用方重试会按精确 key
			// 幂等覆盖。跨 item 的全局原子发布需要更复杂的版本指针，不属于当前单能力 TASK。
			return result, admissionFailure("publish preheat item", err)
		}
		result.ItemCount++
		if result.EarliestExpireAt.IsZero() || expiresAt.Before(result.EarliestExpireAt) {
			result.EarliestExpireAt = expiresAt
		}
		if expiresAt.After(result.LatestExpireAt) {
			result.LatestExpireAt = expiresAt
		}
	}
	return result, nil
}

func (g *Gate) InvalidateItems(ctx context.Context, itemIDs []uint64) error {
	for _, itemID := range itemIDs {
		if itemID == 0 {
			return seckill.ErrInvalidArgument
		}
		commandCtx, cancel := context.WithTimeout(ctx, g.operationTimeout)
		// DEL 本身就是单条原子命令，可以一次删除同 item 的 state/buyers；
		// 无需为了“看起来高级”再包 Lua，更不能用 KEYS 扫描整个 Redis。
		_, err := g.runner.Del(commandCtx, StateKey(itemID), BuyersKey(itemID))
		cancel()
		if err != nil {
			return admissionFailure("invalidate preheat item", err)
		}
	}
	return nil
}

func itemExpiry(endAt time.Time, itemID uint64) time.Time {
	// 确定性 jitter 让同一个 item 重试得到相同 TTL，同时把不同商品的过期时间错开。
	// 这只能缓解跨商品雪崩，不能解决单个超热 key；完整热点治理留给 v0.7。
	jitterSlots := uint64(preheatJitterRange/time.Second) + 1
	return endAt.Add(preheatGrace + time.Duration(itemID%jitterSlots)*time.Second)
}

type ItemConsistencyState struct {
	Exists     bool
	Stock      int64
	BuyerCount int64
	Generation string
	TTL        time.Duration
}

func (g *Gate) InspectItem(ctx context.Context, itemID uint64) (ItemConsistencyState, error) {
	if itemID == 0 {
		return ItemConsistencyState{}, seckill.ErrInvalidArgument
	}
	if g.client == nil {
		return ItemConsistencyState{}, admissionFailure("inspect Redis item", errors.New("client unavailable"))
	}
	commandCtx, cancel := context.WithTimeout(ctx, g.operationTimeout)
	defer cancel()
	stateKey, buyersKey := StateKey(itemID), BuyersKey(itemID)
	fields, err := g.client.HGetAll(commandCtx, stateKey).Result()
	if err != nil {
		return ItemConsistencyState{}, admissionFailure("inspect Redis state", err)
	}
	buyers, err := g.client.HLen(commandCtx, buyersKey).Result()
	if err != nil {
		return ItemConsistencyState{}, admissionFailure("inspect Redis buyers", err)
	}
	state := ItemConsistencyState{Exists: len(fields) > 0, BuyerCount: buyers, Generation: fields["generation"]}
	if !state.Exists {
		return state, nil
	}
	state.Stock, err = strconv.ParseInt(fields["stock"], 10, 64)
	if err != nil {
		return ItemConsistencyState{}, admissionFailure("inspect Redis stock", err)
	}
	state.TTL, err = g.client.PTTL(commandCtx, stateKey).Result()
	if err != nil {
		return ItemConsistencyState{}, admissionFailure("inspect Redis TTL", err)
	}
	return state, nil
}

func isNoScript(err error) bool {
	// go-redis 的真实协议错误可用 HasErrorPrefix；fake runner 和部分代理可能只保留文本。
	// 两种判断都只识别明确 NOSCRIPT，不能把 timeout/EOF 当成“脚本肯定没执行”。
	return redis.HasErrorPrefix(err, "NOSCRIPT") || strings.HasPrefix(err.Error(), "NOSCRIPT")
}

func (g *Gate) scriptSHA(ctx context.Context) (string, error) {
	g.loadMu.Lock()
	defer g.loadMu.Unlock()
	if g.sha != "" {
		return g.sha, nil
	}
	sha, err := g.runner.ScriptLoad(ctx, reserveScript)
	if err != nil {
		return "", err
	}
	g.sha = sha
	return sha, nil
}

func (g *Gate) reloadScript(ctx context.Context) (string, error) {
	g.loadMu.Lock()
	defer g.loadMu.Unlock()
	sha, err := g.runner.ScriptLoad(ctx, reserveScript)
	if err != nil {
		return "", err
	}
	g.sha = sha
	return sha, nil
}

func (g *Gate) streamScriptSHA(ctx context.Context) (string, error) {
	g.loadMu.Lock()
	defer g.loadMu.Unlock()
	if g.streamSHA != "" {
		return g.streamSHA, nil
	}
	sha, err := g.runner.ScriptLoad(ctx, reserveAndEnqueueScript)
	if err != nil {
		return "", err
	}
	g.streamSHA = sha
	return sha, nil
}

func (g *Gate) reloadStreamScript(ctx context.Context) (string, error) {
	g.loadMu.Lock()
	defer g.loadMu.Unlock()
	sha, err := g.runner.ScriptLoad(ctx, reserveAndEnqueueScript)
	if err != nil {
		return "", err
	}
	g.streamSHA = sha
	return sha, nil
}

func parseReservation(reply any) (seckill.Reservation, error) {
	values, ok := reply.([]any)
	if !ok || len(values) != 2 {
		return seckill.Reservation{}, admissionFailure("parse reserve reply", fmt.Errorf("unexpected reply type %T", reply))
	}
	decision, err := asInt64(values[0])
	if err != nil {
		return seckill.Reservation{}, admissionFailure("parse reserve decision", err)
	}
	orderNo, err := asString(values[1])
	if err != nil {
		return seckill.Reservation{}, admissionFailure("parse reserve order number", err)
	}
	switch decision {
	case decisionReserved:
		if orderNo == "" {
			return seckill.Reservation{}, admissionFailure("parse reserve order number", errors.New("empty order number"))
		}
		return seckill.Reservation{OrderNo: orderNo}, nil
	case decisionReplayed:
		if orderNo == "" {
			return seckill.Reservation{}, admissionFailure("parse replay order number", errors.New("empty order number"))
		}
		return seckill.Reservation{OrderNo: orderNo, Replayed: true}, nil
	case decisionNotReady:
		return seckill.Reservation{}, seckill.ErrCacheNotReady
	case decisionUnavailable:
		return seckill.Reservation{}, seckill.ErrUnavailable
	case decisionSoldOut:
		return seckill.Reservation{}, seckill.ErrOutOfStock
	default:
		return seckill.Reservation{}, admissionFailure("parse reserve decision", fmt.Errorf("unknown decision %d", decision))
	}
}

func asInt64(value any) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case string:
		return strconv.ParseInt(typed, 10, 64)
	case []byte:
		return strconv.ParseInt(string(typed), 10, 64)
	default:
		return 0, fmt.Errorf("expected integer, got %T", value)
	}
}

func asString(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case []byte:
		return string(typed), nil
	case nil:
		return "", nil
	default:
		return "", fmt.Errorf("expected string, got %T", value)
	}
}

func admissionFailure(operation string, err error) error {
	return fmt.Errorf("%s: %w: %v", operation, seckill.ErrAdmissionFailure, err)
}
