package seckill

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// QueueService 是 seckill-rpc 的热路径应用服务，只认识 Redis 准入和结果投影。
// 它没有 order repository：订单事实优先查询由 gateway 组合 order-rpc 完成，避免
// 秒杀服务为了一个 GET 又获得订单库权限。
type QueueService struct {
	gate    StreamAdmissionGate
	results StreamResultReader
	now     func() time.Time
}

func NewQueueService(gate StreamAdmissionGate, results StreamResultReader) (*QueueService, error) {
	if gate == nil || results == nil {
		return nil, fmt.Errorf("stream admission gate and result reader are required")
	}
	return &QueueService{gate: gate, results: results, now: time.Now}, nil
}

func (s *QueueService) Enqueue(ctx context.Context, userID, itemID uint64) (AsyncSubmission, error) {
	if s == nil || userID == 0 || itemID == 0 {
		return AsyncSubmission{}, ErrInvalidArgument
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	orderNo, err := newStreamOrderNo(now, itemID)
	if err != nil {
		return AsyncSubmission{}, fmt.Errorf("generate stream seckill order number: %w", err)
	}
	reservation, err := s.gate.ReserveAndEnqueue(ctx, ReservationInput{
		UserID: userID, ItemID: itemID, OrderNo: orderNo, Now: now,
	})
	if err != nil {
		// 绝不在 Redis 超时后回退 MySQL：客户端不知道 Lua 是否已执行，回退会绕过
		// 100/1000 削峰边界。相同用户重试会由 buyers 返回第一次 order_no。
		return AsyncSubmission{}, err
	}
	// RPC 成功对应 HTTP 202：资格预扣和 XADD 已在一个 Lua 中完成；此时 Kafka、
	// inventory-rpc、order-rpc 都还没执行，不能把 QUEUED 表述成“订单已创建”。
	return AsyncSubmission{OrderNo: reservation.OrderNo, Replayed: reservation.Replayed}, nil
}

func (s *QueueService) GetProjectedResult(ctx context.Context, userID uint64, orderNo string) (AsyncResult, error) {
	orderNo = strings.TrimSpace(orderNo)
	if s == nil || userID == 0 || orderNo == "" {
		return AsyncResult{}, ErrAsyncResultNotFound
	}
	status, err := s.results.FindStreamResult(ctx, userID, orderNo)
	if err != nil {
		return AsyncResult{}, err
	}
	return AsyncResult{OrderNo: orderNo, Status: status}, nil
}
