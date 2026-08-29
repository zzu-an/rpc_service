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
	ErrInvalidArgument  = errors.New("invalid seckill argument")
	ErrActivityNotFound = errors.New("seckill activity not found")
	ErrItemNotFound     = errors.New("seckill item not found")
	ErrUnavailable      = errors.New("seckill unavailable")
	ErrOutOfStock       = errors.New("seckill item out of stock")
	ErrInventoryBusy    = errors.New("seckill inventory contention exceeded retry limit")
	ErrConflict         = errors.New("seckill conflict")
	ErrCacheNotReady    = errors.New("seckill cache not ready")
	ErrAdmissionFailure = errors.New("seckill admission infrastructure unavailable")
	ErrNoItems          = errors.New("seckill activity has no items")
	ErrJobNotFound      = errors.New("seckill order job not found")
	ErrJobConflict      = errors.New("seckill order job identity conflict")
	ErrQueueUnavailable = errors.New("seckill async queue unavailable")
)

type JobStatus uint8

const (
	JobStatusPendingPublish JobStatus = iota + 1
	JobStatusPublished
	JobStatusSucceeded
	JobStatusFailed
)

type OrderJob struct {
	ID              uint64
	EventID         string
	OrderNo         string
	UserID          uint64
	ItemID          uint64
	ReservedAt      time.Time
	Payload         []byte
	Status          JobStatus
	PublishAttempts uint32
	ConsumeAttempts uint32
	NextPublishAt   time.Time
	LastErrorCode   string
	PublishedAt     time.Time
	CompletedAt     time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type EnsureJobInput struct {
	EventID    string
	OrderNo    string
	UserID     uint64
	ItemID     uint64
	ReservedAt time.Time
	Payload    []byte
}

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

// JobMessageFactory 把领域输入转换成冻结的消息正文。
// Service 不直接 import Kafka/JSON 包，避免领域层依赖基础设施；实现由 seckill/mq 提供。
type JobMessageFactory interface {
	Build(orderNo string, userID, itemID uint64, reservedAt time.Time) (eventID string, payload []byte, err error)
}

// JobRepository 表达异步请求的持久化生命周期，不把 Kafka offset 或 writer 类型带入领域层。
// job 是投递凭证，不是订单事实源；状态查询最终仍需优先检查 orders/claim。
type JobRepository interface {
	EnsureJob(ctx context.Context, input EnsureJobInput) (job OrderJob, replayed bool, err error)
	FindJobOwned(ctx context.Context, userID uint64, orderNo string) (OrderJob, error)
	FindOrderOwned(ctx context.Context, userID uint64, orderNo string) (order.Order, error)
	ListPendingJobs(ctx context.Context, now time.Time, limit int) ([]OrderJob, error)
	MarkJobPublished(ctx context.Context, jobID uint64, at time.Time) (bool, error)
	ScheduleJobPublishRetry(ctx context.Context, jobID uint64, next time.Time, errorCode string) (bool, error)
	MarkJobSucceeded(ctx context.Context, jobID uint64, at time.Time) (bool, error)
	MarkJobFailed(ctx context.Context, jobID uint64, at time.Time, errorCode string) (bool, error)
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
	jobRepository  JobRepository
	messageFactory JobMessageFactory
	now            func() time.Time
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, now: time.Now}
}

// NewWorkerService 只装配 Kafka consumer 所需的 Purchase 与 job 边界。
// worker 仍与 API 共享同一单体领域和数据库，不是 v0.5 的服务拆分，也不需要 RPC/etcd。
func NewWorkerService(repository Repository, jobs JobRepository) (*Service, error) {
	if repository == nil || jobs == nil {
		return nil, fmt.Errorf("seckill worker repository and jobs are required")
	}
	return &Service{repository: repository, jobRepository: jobs, now: time.Now}, nil
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

func NewServiceWithAsyncAdmission(repository Repository, reader PreheatSnapshotReader, cache ActivityCache, gate AdmissionGate, jobs JobRepository, factory JobMessageFactory) (*Service, error) {
	service, err := NewServiceWithAdmission(repository, reader, cache, gate)
	if err != nil {
		return nil, err
	}
	if jobs == nil || factory == nil {
		return nil, fmt.Errorf("seckill job repository and message factory are required")
	}
	service.jobRepository = jobs
	service.messageFactory = factory
	return service, nil
}

func (s *Service) AsyncEnabled() bool {
	return s != nil && s.jobRepository != nil && s.messageFactory != nil
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
	if !s.AsyncEnabled() {
		return AsyncSubmission{}, ErrQueueUnavailable
	}
	orderNo, gateReplayed, reservedAt, err := s.reserve(ctx, userID, itemID)
	if err != nil {
		return AsyncSubmission{}, err
	}
	eventID, payload, err := s.messageFactory.Build(orderNo, userID, itemID, reservedAt)
	if err != nil {
		return AsyncSubmission{}, fmt.Errorf("build seckill order job message: %w", err)
	}
	_, jobReplayed, err := s.jobRepository.EnsureJob(ctx, EnsureJobInput{
		EventID: eventID, OrderNo: orderNo, UserID: userID, ItemID: itemID,
		ReservedAt: reservedAt, Payload: payload,
	})
	if err != nil {
		// Redis 已预扣而 MySQL job 写入失败时，两个存储没有共同事务。这里返回 503 但保留
		// buyer/orderNo：同一用户重试可幂等补写。盲目删除 buyer 或 INCR 会在“写入结果未知”
		// 时重新放出资格并制造超卖窗口。若用户不重试仍可能少卖，自动对账留给 v0.6。
		// HTTP 层仍只把 ErrQueueUnavailable 映射成通用 503，但内部错误必须保留在 error
		// chain 中供日志和阶段门禁定位。只返回业务哨兵会把外键、连接池、Schema 等完全
		// 不同故障压成同一句话，线上只能看到“队列不可用”却无法修复根因。
		return AsyncSubmission{}, fmt.Errorf("%w: persist accepted job: %w", ErrQueueUnavailable, err)
	}
	// HTTP 202 的承诺只到这里：job 已持久化，尚不表示 Kafka 已发布或订单已创建。
	return AsyncSubmission{OrderNo: orderNo, Replayed: gateReplayed || jobReplayed}, nil
}

func (s *Service) ProcessQueuedJob(ctx context.Context, job OrderJob) (PurchaseResult, error) {
	if s == nil || s.jobRepository == nil || job.ID == 0 || job.UserID == 0 || job.ItemID == 0 || strings.TrimSpace(job.OrderNo) == "" || job.ReservedAt.IsZero() {
		return PurchaseResult{}, ErrInvalidArgument
	}
	// 这里不再调用 Redis Reserve：资格已经在 HTTP 阶段确定，重复调用 Lua 会把消费重试
	// 误当成新请求。Purchase 使用原 reserved_at，而不是当前消费时间；否则 backlog 跨过
	// end_at 后，活动窗口内合法获得的资格会永远无法落单。
	result, err := s.repository.Purchase(ctx, job.UserID, job.ItemID, job.OrderNo, job.ReservedAt.UTC())
	if err != nil {
		return PurchaseResult{}, err
	}
	updated, err := s.jobRepository.MarkJobSucceeded(ctx, job.ID, s.now().UTC())
	if err != nil {
		// 订单事务可能已经提交，但 job 状态写失败。返回错误让 Kafka 重投；下一次 Purchase
		// 由 orders.order_no/claim 唯一键读回原订单，再补写 SUCCEEDED。不能因为 offset 尚未
		// 提交就再扣一次库存，也不能用进程内 map 假装 exactly-once。
		return PurchaseResult{}, fmt.Errorf("mark consumed seckill job succeeded: %w", err)
	}
	if !updated && job.Status != JobStatusSucceeded {
		return PurchaseResult{}, fmt.Errorf("mark consumed seckill job succeeded: state changed")
	}
	return result, nil
}

func (s *Service) GetAsyncResult(ctx context.Context, userID uint64, orderNo string) (AsyncResult, error) {
	orderNo = strings.TrimSpace(orderNo)
	if s == nil || s.jobRepository == nil || userID == 0 || orderNo == "" {
		return AsyncResult{}, ErrJobNotFound
	}
	created, err := s.jobRepository.FindOrderOwned(ctx, userID, orderNo)
	if err == nil {
		// 订单表是事实源。consumer 可能已提交订单，却在更新 job=SUCCEEDED 前宕机；
		// 此时返回 QUEUED 会误导用户重复等待，所以订单存在必须优先于滞后的 job 状态。
		return AsyncResult{OrderNo: orderNo, Status: AsyncResultSucceeded, Order: created}, nil
	}
	if !errors.Is(err, ErrJobNotFound) {
		return AsyncResult{}, err
	}
	job, err := s.jobRepository.FindJobOwned(ctx, userID, orderNo)
	if err != nil {
		return AsyncResult{}, err
	}
	if job.Status == JobStatusFailed {
		return AsyncResult{OrderNo: orderNo, Status: AsyncResultFailed}, nil
	}
	// PENDING/PUBLISHED/retry 对外统一 QUEUED。Kafka offset 与 job 状态不是原子事务，
	// 暴露“正在第几步”只会制造虚假精度；调用方只需要知道是否已有最终订单或明确失败。
	return AsyncResult{OrderNo: orderNo, Status: AsyncResultQueued}, nil
}

func (s *Service) reserve(ctx context.Context, userID, itemID uint64) (orderNo string, replayed bool, now time.Time, err error) {
	if userID == 0 || itemID == 0 {
		return "", false, time.Time{}, ErrInvalidArgument
	}
	// 订单号和 reserved_at 必须来自同一次时钟读取。Redis 并发重放只保存赢家订单号，
	// 输家需要从它恢复首次毫秒时间，才能确定性重建同一个 job payload。
	// 订单号只编码毫秒，因此首次请求也必须收敛到毫秒；若保留额外微秒，重放从 ID
	// 恢复出的时间会与 job 表中的首次值不同。精度选择是消息契约的一部分。
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
