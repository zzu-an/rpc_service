package inventoryrpc

import (
	"context"
	"fmt"

	productv1 "service_rpc/api/gen/product/v1"
	productrpc "service_rpc/internal/product/rpcclient"
	"service_rpc/internal/seckill"
)

type ProductSnapshotAdapter struct{ client *productrpc.Client }

func NewProductSnapshotAdapter(client *productrpc.Client) *ProductSnapshotAdapter {
	return &ProductSnapshotAdapter{client: client}
}

func (a *ProductSnapshotAdapter) GetActiveSKUSnapshot(ctx context.Context, skuID uint64) (seckill.FrozenSKUSnapshot, error) {
	// 原样传递 inventory RPC 的 context；product-rpc 不可用或超时必须有界失败，
	// 不能构造 0 元/空名称的假 snapshot 写库，否则错误会永久进入订单事实。
	snapshot, err := a.client.GetSkuSnapshot(ctx, skuID)
	if err != nil {
		return seckill.FrozenSKUSnapshot{}, err
	}
	if snapshot.GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_ENABLED {
		return seckill.FrozenSKUSnapshot{}, fmt.Errorf("product SKU is not active")
	}
	return seckill.FrozenSKUSnapshot{
		SKUID: snapshot.GetSkuId(), ProductName: snapshot.GetProductName(), SKUCode: snapshot.GetSkuCode(),
		SKUName: snapshot.GetSkuName(), UnitPriceCent: snapshot.GetUnitPriceCent(),
	}, nil
}
