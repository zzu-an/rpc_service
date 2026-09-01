package seckillrpc

import (
	"context"
	"fmt"
	"time"

	"service_rpc/internal/seckill"
	"service_rpc/internal/seckill/inventoryclient"
)

type InventoryItemAdapter struct{ client *inventoryclient.Client }

func NewInventoryItemAdapter(client *inventoryclient.Client) *InventoryItemAdapter {
	return &InventoryItemAdapter{client: client}
}

func (a *InventoryItemAdapter) ListActivityItems(ctx context.Context, activityID uint64) ([]seckill.Item, error) {
	if a == nil || a.client == nil {
		return nil, fmt.Errorf("inventory-rpc client is required")
	}
	items, err := a.client.ListActivityItems(ctx, activityID)
	if err != nil {
		return nil, err
	}
	result := make([]seckill.Item, 0, len(items))
	for _, item := range items {
		if item == nil || item.GetSku() == nil {
			return nil, fmt.Errorf("inventory-rpc returned incomplete item snapshot")
		}
		result = append(result, seckill.Item{
			ID: item.GetId(), ActivityID: item.GetActivityId(), SKUID: item.GetSku().GetSkuId(),
			InitialStock: item.GetInitialStock(), AvailableStock: item.GetAvailableStock(),
			Version: item.GetVersion(), CreatedAt: time.UnixMilli(item.GetCreatedAtMs()).UTC(),
		})
	}
	return result, nil
}

var _ seckill.ActivityItemReader = (*InventoryItemAdapter)(nil)
