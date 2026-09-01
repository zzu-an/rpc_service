package seckill

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ActivityRepository 只拥有 seckill_activities。库存已经迁入 inventory-rpc，
// 因此这里刻意没有 ListItems；接口本身就是防止未来“同库方便查询”破坏服务边界的护栏。
type ActivityRepository interface {
	CreateActivity(ctx context.Context, input CreateActivityInput) (Activity, error)
	FindActivity(ctx context.Context, activityID uint64) (Activity, error)
	SetActivityStatus(ctx context.Context, activityID uint64, status uint8) error
	ListActiveActivityIDs(ctx context.Context, now time.Time) ([]uint64, error)
}

func (s *ActivityService) ListStreamItemIDs(ctx context.Context) ([]uint64, error) {
	activityIDs, err := s.repository.ListActiveActivityIDs(ctx, s.now().UTC())
	if err != nil {
		return nil, err
	}
	ids := make([]uint64, 0)
	for _, activityID := range activityIDs {
		items, err := s.items.ListActivityItems(ctx, activityID)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			ids = append(ids, item.ID)
		}
	}
	// 面试/优化点：这是管理面 discovery，不在下单热路径。活动数很大时应给 inventory-rpc
	// 增加批量 activity IDs 契约；当前不并发扇出，避免一次 discovery 放大 RPC/DB 压力。
	return ids, nil
}

// ActivityItemReader 从 inventory-rpc 获取活动商品快照。跨服务读取无法和本地活动表
// 组成数据库事务；预热发生在活动开始前、可重复执行且默认 fail closed，因此这里选择
// 有界 RPC + 幂等覆盖，而不是引入分布式事务。
type ActivityItemReader interface {
	ListActivityItems(ctx context.Context, activityID uint64) ([]Item, error)
}

type ActivityService struct {
	repository ActivityRepository
	items      ActivityItemReader
	cache      ActivityCache
	now        func() time.Time
}

func NewActivityService(repository ActivityRepository, items ActivityItemReader, cache ActivityCache) (*ActivityService, error) {
	if repository == nil || items == nil || cache == nil {
		return nil, fmt.Errorf("activity repository, inventory reader, and cache are required")
	}
	return &ActivityService{repository: repository, items: items, cache: cache, now: time.Now}, nil
}

func (s *ActivityService) CreateActivity(ctx context.Context, input CreateActivityInput) (Activity, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.StartAt = input.StartAt.UTC()
	input.EndAt = input.EndAt.UTC()
	if input.Name == "" || input.StartAt.IsZero() || input.EndAt.IsZero() || !input.EndAt.After(input.StartAt) {
		return Activity{}, ErrInvalidArgument
	}
	return s.repository.CreateActivity(ctx, input)
}

func (s *ActivityService) SetActivityStatus(ctx context.Context, activityID uint64, status uint8) error {
	if activityID == 0 || (status != StatusEnabled && status != StatusDisabled) {
		return ErrInvalidArgument
	}

	if status == StatusDisabled {
		// 停用先写事实源：即使后续 inventory-rpc 或 Redis 故障，活动也已经被关闭。
		// Redis 旧快照可能短暂放行，但下游库存 RPC 会以 reservation 幂等约束兜底；
		// 调用方收到错误后可再次停用，继续清理缓存。
		if err := s.repository.SetActivityStatus(ctx, activityID, status); err != nil {
			return err
		}
		items, err := s.items.ListActivityItems(ctx, activityID)
		if err != nil {
			return err
		}
		return s.cache.InvalidateItems(ctx, itemIDs(items))
	}

	// 启用顺序相反：先拿到 inventory 快照并删除旧 generation，再启用活动。
	// 若清缓存失败，活动仍是 disabled，不能拿陈旧库存接流量。
	items, err := s.items.ListActivityItems(ctx, activityID)
	if err != nil {
		return err
	}
	if err := s.cache.InvalidateItems(ctx, itemIDs(items)); err != nil {
		return err
	}
	return s.repository.SetActivityStatus(ctx, activityID, status)
}

func (s *ActivityService) PreheatActivity(ctx context.Context, activityID uint64) (PreheatResult, error) {
	if activityID == 0 {
		return PreheatResult{}, ErrInvalidArgument
	}
	activity, err := s.repository.FindActivity(ctx, activityID)
	if err != nil {
		return PreheatResult{}, err
	}
	items, err := s.items.ListActivityItems(ctx, activityID)
	if err != nil {
		return PreheatResult{}, err
	}
	if len(items) == 0 {
		return PreheatResult{}, ErrNoItems
	}
	now := s.now().UTC()
	// 活动开始后重建 buyers 会遗失已抢到资格的用户。这里宁可拒绝在线重建，
	// 也不拿 inventory-rpc 当前库存“猜测”Redis 已经扣过多少。
	if activity.Status != StatusEnabled || !now.Before(activity.StartAt.UTC()) {
		return PreheatResult{}, ErrUnavailable
	}
	return s.cache.PublishActivity(ctx, PreheatSnapshot{Activity: activity, Items: items}, now)
}

func itemIDs(items []Item) []uint64 {
	ids := make([]uint64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}
