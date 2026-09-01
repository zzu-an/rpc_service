package main

import (
	"context"
	"fmt"
	"time"

	inventoryv1 "service_rpc/api/gen/inventory/v1"
	orderv1 "service_rpc/api/gen/order/v1"
	orderclient "service_rpc/internal/order/rpcclient"
	platformrpc "service_rpc/internal/platform/rpc"
	"service_rpc/internal/seckill/inventoryclient"
	seckillclient "service_rpc/internal/seckill/seckillclient"
	"service_rpc/internal/seckill/streamqueue"
)

type streamItemSource struct{ client *seckillclient.Client }

func (s streamItemSource) ListStreamItemIDs(ctx context.Context) ([]uint64, error) {
	return s.client.ListStreamItemIDs(ctx)
}

type inventoryRPCAdapter struct {
	client *inventoryclient.Client
	policy *platformrpc.Policy
}

func (a inventoryRPCAdapter) Reserve(ctx context.Context, userID, activityID, itemID uint64, orderNo string, reservedAt time.Time) (streamqueue.ReservationResult, error) {
	var response *inventoryv1.ReserveSeckillStockResponse
	err := a.policy.Do(ctx, true, func(callContext context.Context) error {
		var callErr error
		response, callErr = a.client.ReserveSeckillStock(callContext, &inventoryv1.ReserveSeckillStockRequest{
			OrderNo: orderNo, ActivityId: activityID, ItemId: itemID, UserId: userID, ReservedAtMs: reservedAt.UTC().UnixMilli(),
		})
		return callErr
	})
	if err != nil {
		return streamqueue.ReservationResult{}, err
	}
	snapshot := response.GetSku()
	if snapshot == nil {
		return streamqueue.ReservationResult{}, fmt.Errorf("inventory-rpc returned empty reservation snapshot")
	}
	return streamqueue.ReservationResult{Snapshot: streamqueue.ReservedSnapshot{
		SKUID: snapshot.GetSkuId(), ProductName: snapshot.GetProductName(), SKUCode: snapshot.GetSkuCode(),
		SKUName: snapshot.GetSkuName(), UnitPriceCent: snapshot.GetUnitPriceCent(),
	}, Replayed: response.GetReplayed()}, nil
}

type orderRPCAdapter struct {
	client *orderclient.Client
	policy *platformrpc.Policy
}

func (a orderRPCAdapter) CreateSeckill(ctx context.Context, userID, activityID, itemID uint64, orderNo string, reservedAt time.Time, snapshot streamqueue.ReservedSnapshot) (bool, error) {
	var response *orderv1.CreateSeckillOrderResponse
	err := a.policy.Do(ctx, true, func(callContext context.Context) error {
		var callErr error
		response, callErr = a.client.CreateSeckillOrder(callContext, &orderv1.CreateSeckillOrderRequest{
			OrderNo: orderNo, UserId: userID, ActivityId: activityID, ItemId: itemID, ReservedAtMs: reservedAt.UTC().UnixMilli(),
			Item: &orderv1.FrozenOrderItem{SkuId: snapshot.SKUID, ProductName: snapshot.ProductName, SkuCode: snapshot.SKUCode, SkuName: snapshot.SKUName, UnitPriceCent: snapshot.UnitPriceCent, Quantity: 1},
		})
		return callErr
	})
	if err != nil {
		return false, err
	}
	return response.GetReplayed(), nil
}

var _ streamqueue.ItemSource = streamItemSource{}
var _ streamqueue.InventoryRPC = inventoryRPCAdapter{}
var _ streamqueue.OrderRPC = orderRPCAdapter{}

func newRPCPolicy(c retryConfig) (*platformrpc.Policy, error) {
	return platformrpc.NewPolicy(platformrpc.PolicyConfig{
		MaxAttempts:       c.MaxAttempts,
		BackoffBase:       time.Duration(c.BackoffBaseMilliseconds) * time.Millisecond,
		BackoffMax:        time.Duration(c.BackoffMaxMilliseconds) * time.Millisecond,
		JitterRatio:       c.JitterRatio,
		BreakerFailures:   c.BreakerFailures,
		BreakerOpenPeriod: time.Duration(c.BreakerOpenMilliseconds) * time.Millisecond,
	})
}
