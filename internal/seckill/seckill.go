// Package seckill 定义 v0.2 单机秒杀的领域规则和应用服务边界。
//
// 这里故意不依赖 MySQL 或 go-zero：库存锁策略属于基础设施实现，HTTP 参数解析属于传输层。
// 面试中常问“领域层为什么不直接写 SQL”，核心原因是业务不变量应能脱离具体存储被单元测试。
package seckill

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"service_rpc/internal/order"
)

const (
	StatusEnabled  uint8 = 1
	StatusDisabled uint8 = 2
)

var (
	ErrInvalidArgument     = errors.New("invalid seckill argument")
	ErrActivityNotFound    = errors.New("seckill activity not found")
	ErrItemNotFound        = errors.New("seckill item not found")
	ErrUnavailable         = errors.New("seckill unavailable")
	ErrOutOfStock          = errors.New("seckill item out of stock")
	ErrInventoryBusy       = errors.New("seckill inventory contention exceeded retry limit")
	ErrConflict            = errors.New("seckill conflict")
	ErrCacheNotReady       = errors.New("seckill cache not ready")
	ErrAdmissionFailure    = errors.New("seckill admission infrastructure unavailable")
	ErrNoItems             = errors.New("seckill activity has no items")
	ErrAsyncResultNotFound = errors.New("seckill async order result not found")
	ErrQueueUnavailable    = errors.New("seckill async queue unavailable")
)

type AsyncSubmission struct {
	OrderNo  string
	Replayed bool
}

type AsyncResultStatus string

const (
	AsyncResultQueued    AsyncResultStatus = "QUEUED"
	AsyncResultSucceeded AsyncResultStatus = "SUCCEEDED"
	AsyncResultFailed    AsyncResultStatus = "FAILED"
)

type AsyncResult struct {
	OrderNo string
	Status  AsyncResultStatus
	Order   order.Order
}

// ReservationInput 是 Redis 准入层需要的最小业务信息。
// 候选 OrderNo 在进入 Redis 前生成；如果请求重放，Redis 会返回第一次保存的订单号，
// 这样 MySQL 提交结果未知时仍能用同一幂等标识恢复，而不是创建第二个“候选订单”。
type ReservationInput struct {
	UserID  uint64
	ItemID  uint64
	OrderNo string
	Now     time.Time
}

type Reservation struct {
	OrderNo  string
	Replayed bool
}

// AdmissionGate 只回答请求是否获得进入 MySQL 事务的资格，不负责创建订单。
// 这种边界保留了 MySQL 作为事实源的地位，也让 Redis 故障不会偷偷改变库存事务模型。
type AdmissionGate interface {
	Reserve(ctx context.Context, input ReservationInput) (Reservation, error)
}

// StreamAdmissionGate 是 v0.4.2 热路径的原子边界。它和 AdmissionGate 分开，避免让
// sync 调用方意外把一次普通 Reserve 变成入队。具体实现必须在同一 Lua 中
// 完成资格预扣与 XADD；若拆成两个 Go 方法，进程会再次暴露“库存已扣、任务未写”的窗口。
type StreamAdmissionGate interface {
	ReserveAndEnqueue(ctx context.Context, input ReservationInput) (Reservation, error)
}

// StreamResultReader 只返回 Redis 中的短期排队终态。正式订单仍由 AsyncOrderReader
// 从 MySQL 读取并优先返回，Redis 状态不能反过来覆盖已经提交的订单事实。
type StreamResultReader interface {
	FindStreamResult(ctx context.Context, userID uint64, orderNo string) (AsyncResultStatus, error)
}

type AsyncOrderReader interface {
	FindOrderOwned(ctx context.Context, userID uint64, orderNo string) (order.Order, error)
}

type Activity struct {
	ID        uint64
	Name      string
	StartAt   time.Time
	EndAt     time.Time
	Status    uint8
	CreatedAt time.Time
}

type Item struct {
	ID             uint64
	ActivityID     uint64
	SKUID          uint64
	InitialStock   int64
	AvailableStock int64
	Version        uint64
	CreatedAt      time.Time
}

type PreheatSnapshot struct {
	Activity Activity
	Items    []Item
}

type PreheatResult struct {
	ActivityID       uint64
	ItemCount        int
	EarliestExpireAt time.Time
	LatestExpireAt   time.Time
}

type CreateActivityInput struct {
	Name    string
	StartAt time.Time
	EndAt   time.Time
}

type AddItemInput struct {
	ActivityID uint64
	SKUID      uint64
	Stock      int64
}

type PurchaseResult struct {
	Order    order.Order
	Replayed bool
}

// Repository 是当前单体内的明确持久化边界。
// Purchase 必须把扣库存、写订单和写抢购记录放在同一事务；Service 不持有 *sql.Tx，避免把数据库细节泄漏到领域层。
type Repository interface {
	CreateActivity(ctx context.Context, input CreateActivityInput) (Activity, error)
	AddItem(ctx context.Context, input AddItemInput) (Item, error)
	SetActivityStatus(ctx context.Context, activityID uint64, status uint8) error
	Purchase(ctx context.Context, userID, itemID uint64, orderNo string, now time.Time) (PurchaseResult, error)
}

// PreheatSnapshotReader 单独表达 Redis 预热所需的只读能力。
// 不把它塞进 Purchase 事务接口，可以避免未来缓存逻辑扩大订单写事务的职责。
type PreheatSnapshotReader interface {
	LoadPreheatSnapshot(ctx context.Context, activityID uint64) (PreheatSnapshot, error)
}

// ActivityCache 发布和关闭活动准入快照。它不提供“回补库存”接口是有意设计：
// Redis 命令超时后无法判断服务端是否执行，暴露通用 INCR/删除 buyer 很容易制造重复资格。
type ActivityCache interface {
	PublishActivity(ctx context.Context, snapshot PreheatSnapshot, now time.Time) (PreheatResult, error)
	InvalidateItems(ctx context.Context, itemIDs []uint64) error
}

type Service struct {
	repository     Repository
	snapshotReader PreheatSnapshotReader
	activityCache  ActivityCache
	admissionGate  AdmissionGate
	streamGate     StreamAdmissionGate
	streamResults  StreamResultReader
	orderReader    AsyncOrderReader
	now            func() time.Time
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, now: time.Now}
}

func NewServiceWithCache(repository Repository, reader PreheatSnapshotReader, cache ActivityCache) (*Service, error) {
	if repository == nil || reader == nil || cache == nil {
		return nil, fmt.Errorf("seckill repository, snapshot reader, and activity cache are required")
	}
	return &Service{repository: repository, snapshotReader: reader, activityCache: cache, now: time.Now}, nil
}

func NewServiceWithAdmission(repository Repository, reader PreheatSnapshotReader, cache ActivityCache, gate AdmissionGate) (*Service, error) {
	service, err := NewServiceWithCache(repository, reader, cache)
	if err != nil {
		return nil, err
	}
	if gate == nil {
		return nil, fmt.Errorf("seckill admission gate is required")
	}
	service.admissionGate = gate
	return service, nil
}

func NewServiceWithStreamAdmission(repository Repository, reader PreheatSnapshotReader, cache ActivityCache, gate AdmissionGate, streamGate StreamAdmissionGate, results StreamResultReader, orders AsyncOrderReader) (*Service, error) {
	service, err := NewServiceWithAdmission(repository, reader, cache, gate)
	if err != nil {
		return nil, err
	}
	if streamGate == nil || results == nil || orders == nil {
		return nil, fmt.Errorf("seckill stream gate, result reader, and order reader are required")
	}
	service.streamGate = streamGate
	service.streamResults = results
	service.orderReader = orders
	return service, nil
}

func (s *Service) AsyncEnabled() bool {
	return s != nil && s.StreamAsyncEnabled()
}

func (s *Service) StreamAsyncEnabled() bool {
	return s != nil && s.streamGate != nil && s.streamResults != nil && s.orderReader != nil
}

func (s *Service) CreateActivity(ctx context.Context, input CreateActivityInput) (Activity, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.StartAt = input.StartAt.UTC()
	input.EndAt = input.EndAt.UTC()
	if input.Name == "" || input.StartAt.IsZero() || input.EndAt.IsZero() || !input.EndAt.After(input.StartAt) {
		return Activity{}, ErrInvalidArgument
	}
	return s.repository.CreateActivity(ctx, input)
}

func (s *Service) AddItem(ctx context.Context, input AddItemInput) (Item, error) {
	if input.ActivityID == 0 || input.SKUID == 0 || input.Stock <= 0 {
		return Item{}, ErrInvalidArgument
	}
	return s.repository.AddItem(ctx, input)
}

func (s *Service) SetActivityStatus(ctx context.Context, activityID uint64, status uint8) error {
	if activityID == 0 || (status != StatusEnabled && status != StatusDisabled) {
		return ErrInvalidArgument
	}
	if s.activityCache == nil {
		return s.repository.SetActivityStatus(ctx, activityID, status)
	}

	snapshot, err := s.snapshotReader.LoadPreheatSnapshot(ctx, activityID)
	if err != nil {
		return err
	}
	itemIDs := make([]uint64, 0, len(snapshot.Items))
	for _, item := range snapshot.Items {
		itemIDs = append(itemIDs, item.ID)
	}
	if status == StatusEnabled {
		// 重新启用前先关闭旧快照，保证用户必须显式预热新 generation。
		// 如果先启用 MySQL 再失效 Redis，旧 ready key 可能短暂放行，重新制造数据库热点。
		if err := s.activityCache.InvalidateItems(ctx, itemIDs); err != nil {
			return err
		}
		return s.repository.SetActivityStatus(ctx, activityID, status)
	}

	// 停用顺序反过来：先让事实源拒单，再清 Redis。即使清理失败，旧缓存最多让请求
	// 到达 MySQL 并被拒绝，不能创建错误订单。调用方会收到错误并可幂等重试清理。
	if err := s.repository.SetActivityStatus(ctx, activityID, status); err != nil {
		return err
	}
	return s.activityCache.InvalidateItems(ctx, itemIDs)
}

func (s *Service) PreheatActivity(ctx context.Context, activityID uint64) (PreheatResult, error) {
	if activityID == 0 {
		return PreheatResult{}, ErrInvalidArgument
	}
	if s.snapshotReader == nil || s.activityCache == nil {
		return PreheatResult{}, ErrAdmissionFailure
	}
	snapshot, err := s.snapshotReader.LoadPreheatSnapshot(ctx, activityID)
	if err != nil {
		return PreheatResult{}, err
	}
	now := s.now().UTC()
	// 活动开始后重建 buyers 会覆盖已经获得资格的用户。没有 MQ/对账时无法从 Redis
	// 单独恢复这段历史，因此 v0.3 选择 fail closed，而不是在线“猜一个库存”。
	if snapshot.Activity.Status != StatusEnabled || !now.Before(snapshot.Activity.StartAt.UTC()) {
		return PreheatResult{}, ErrUnavailable
	}
	return s.activityCache.PublishActivity(ctx, snapshot, now)
}

func (s *Service) Purchase(ctx context.Context, userID, itemID uint64) (PurchaseResult, error) {
	orderNo, gateReplayed, now, err := s.reserve(ctx, userID, itemID)
	if err != nil {
		return PurchaseResult{}, err
	}
	result, err := s.repository.Purchase(ctx, userID, itemID, orderNo, now)
	if err != nil {
		// 不提供“失败就归还 Redis”的通用补偿：MySQL Commit 报错时事务可能已经提交，
		// 盲目回补会放出第二份资格。保留 buyer/orderNo 后，同一用户重试可由唯一索引恢复。
		return PurchaseResult{}, err
	}
	result.Replayed = result.Replayed || gateReplayed
	return result, nil
}

func (s *Service) Enqueue(ctx context.Context, userID, itemID uint64) (AsyncSubmission, error) {
	if s == nil || !s.StreamAsyncEnabled() {
		return AsyncSubmission{}, ErrQueueUnavailable
	}
	return s.enqueueStream(ctx, userID, itemID)
}

func (s *Service) enqueueStream(ctx context.Context, userID, itemID uint64) (AsyncSubmission, error) {
	if userID == 0 || itemID == 0 {
		return AsyncSubmission{}, ErrInvalidArgument
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	orderNo, err := newStreamOrderNo(now, itemID)
	if err != nil {
		return AsyncSubmission{}, fmt.Errorf("generate stream seckill order number: %w", err)
	}
	reservation, err := s.streamGate.ReserveAndEnqueue(ctx, ReservationInput{
		UserID: userID, ItemID: itemID, OrderNo: orderNo, Now: now,
	})
	if err != nil {
		// Lua 是单 shard 原子操作：返回错误时客户端仍无法判断响应是否丢失，但同一用户
		// 重试会先命中 buyer 并返回原 order_no，不会重复扣库存或重复 XADD。
		return AsyncSubmission{}, err
	}
	// HTTP 202 的持久化边界是 Redis Stream。若部署未启用 AOF/副本确认，整机故障
	// 仍可能丢任务，不能宣传成永久日志。
	return AsyncSubmission{OrderNo: reservation.OrderNo, Replayed: reservation.Replayed}, nil
}

// ProcessStreamTask 只执行正式订单事务。Stream runtime 只有在
// 此方法成功后才 ACK；若 MySQL 已提交而 ACK 前宕机，重投会由 order_no 与 claim 唯一索引
// 读回原订单，所以数据库约束仍是最终幂等防线。
func (s *Service) ProcessStreamTask(ctx context.Context, userID, itemID uint64, orderNo string, reservedAt time.Time) (PurchaseResult, error) {
	if s == nil || s.repository == nil || userID == 0 || itemID == 0 || strings.TrimSpace(orderNo) == "" || reservedAt.IsZero() {
		return PurchaseResult{}, ErrInvalidArgument
	}
	return s.repository.Purchase(ctx, userID, itemID, strings.TrimSpace(orderNo), reservedAt.UTC())
}

func (s *Service) GetAsyncResult(ctx context.Context, userID uint64, orderNo string) (AsyncResult, error) {
	orderNo = strings.TrimSpace(orderNo)
	if s == nil || userID == 0 || orderNo == "" {
		return AsyncResult{}, ErrAsyncResultNotFound
	}
	if s.orderReader == nil || s.streamResults == nil {
		return AsyncResult{}, ErrAsyncResultNotFound
	}
	created, err := s.orderReader.FindOrderOwned(ctx, userID, orderNo)
	if err == nil {
		// 订单表是事实源。consumer 可能已提交订单，却在 ACK 或刷新 Redis 结果前宕机；
		// 此时返回 QUEUED 会误导用户重复等待，所以订单存在必须优先于缓存状态。
		return AsyncResult{OrderNo: orderNo, Status: AsyncResultSucceeded, Order: created}, nil
	}
	if !errors.Is(err, ErrAsyncResultNotFound) {
		return AsyncResult{}, err
	}
	status, resultErr := s.streamResults.FindStreamResult(ctx, userID, orderNo)
	if resultErr != nil {
		return AsyncResult{}, resultErr
	}
	return AsyncResult{OrderNo: orderNo, Status: status}, nil
}

func (s *Service) reserve(ctx context.Context, userID, itemID uint64) (orderNo string, replayed bool, now time.Time, err error) {
	if userID == 0 || itemID == 0 {
		return "", false, time.Time{}, ErrInvalidArgument
	}
	// 订单号和 reserved_at 必须来自同一次时钟读取。Redis 并发重放只保存赢家订单号，
	// 输家需要从它恢复首次毫秒时间，才能确定性重建同一个业务请求。
	// 订单号只编码毫秒，因此首次请求也必须收敛到毫秒；若保留额外微秒，重放从 ID
	// 恢复出的时间会与首次保留值不同。精度选择是消息契约的一部分。
	now = s.now().UTC().Truncate(time.Millisecond)
	candidateOrderNo, err := newOrderNo(now)
	if err != nil {
		return "", false, time.Time{}, fmt.Errorf("generate seckill order number: %w", err)
	}
	// now 只读取一次。async consumer 必须继续使用这个 reservedAt，而不是消费时刻；
	// backlog 可能跨过活动结束时间，使用消费时刻会错误拒绝活动窗口内已获得的资格。
	orderNo = candidateOrderNo
	if s.admissionGate == nil {
		return orderNo, false, now, nil
	}
	reservation, err := s.admissionGate.Reserve(ctx, ReservationInput{
		UserID: userID, ItemID: itemID, OrderNo: candidateOrderNo, Now: now,
	})
	if err != nil {
		// Redis 超时不能证明 Lua 没执行，所以这里既不重试脚本，也不回退 MySQL。
		return "", false, time.Time{}, err
	}
	if reservation.Replayed {
		if firstReservedAt, ok := reservedAtFromOrderNo(reservation.OrderNo); ok {
			// 只把服务端生成 ID 中的时间用于恢复幂等载荷，不把它当授权或库存事实。
			// Redis Lua 已验证当前用户拥有这个 order_no，外部请求不能直接注入它。
			now = firstReservedAt
		}
	}
	return reservation.OrderNo, reservation.Replayed, now, nil
}

func newOrderNo(now time.Time) (string, error) {
	if now.IsZero() {
		return "", ErrInvalidArgument
	}
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return fmt.Sprintf("S%d%s", now.UTC().UnixMilli(), hex.EncodeToString(random)), nil
}

func newStreamOrderNo(now time.Time, itemID uint64) (string, error) {
	if now.IsZero() || itemID == 0 {
		return "", ErrInvalidArgument
	}
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	// itemID 放进服务端生成的 order_no，使 GET 结果接口无需相信客户端额外传入的 itemID，
	// 又能定位 `{item:<id>}` 同 slot results。长度仍远小于 orders.order_no VARCHAR(64)。
	return fmt.Sprintf("T%d-%d-%s", now.UTC().UnixMilli(), itemID, hex.EncodeToString(random)), nil
}

// StreamOrderItemID 只解析服务端格式，不把 order_no 当授权。调用方随后仍必须比较 results
// 中的 userID；否则知道别人的订单号就能越权查询状态。
func StreamOrderItemID(orderNo string) (uint64, bool) {
	parts := strings.Split(strings.TrimSpace(orderNo), "-")
	if len(parts) != 3 || len(parts[0]) < 2 || parts[0][0] != 'T' || len(parts[2]) != 16 {
		return 0, false
	}
	if _, err := strconv.ParseInt(parts[0][1:], 10, 64); err != nil {
		return 0, false
	}
	itemID, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil || itemID == 0 {
		return 0, false
	}
	if _, err := hex.DecodeString(parts[2]); err != nil {
		return 0, false
	}
	return itemID, true
}

func reservedAtFromOrderNo(orderNo string) (time.Time, bool) {
	const randomHexLength = 16
	if len(orderNo) <= 1+randomHexLength || orderNo[0] != 'S' {
		return time.Time{}, false
	}
	timestampText := orderNo[1 : len(orderNo)-randomHexLength]
	milliseconds, err := strconv.ParseInt(timestampText, 10, 64)
	if err != nil || milliseconds <= 0 {
		return time.Time{}, false
	}
	if _, err := hex.DecodeString(orderNo[len(orderNo)-randomHexLength:]); err != nil {
		return time.Time{}, false
	}
	return time.UnixMilli(milliseconds).UTC(), true
}
