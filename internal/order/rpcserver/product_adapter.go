package rpcserver

import (
	"context"
	"fmt"

	productv1 "service_rpc/api/gen/product/v1"
	"service_rpc/internal/order"
	productclient "service_rpc/internal/product/rpcclient"
)

type ProductSnapshotAdapter struct{ client *productclient.Client }

func NewProductSnapshotAdapter(client *productclient.Client) *ProductSnapshotAdapter {
	return &ProductSnapshotAdapter{client: client}
}

func (a *ProductSnapshotAdapter) GetOrderItemSnapshot(ctx context.Context, skuID uint64) (order.ItemInput, error) {
	if a == nil || a.client == nil || skuID == 0 {
		return order.ItemInput{}, order.ErrInvalidOrder
	}
	snapshot, err := a.client.GetSkuSnapshot(ctx, skuID)
	if err != nil {
		return order.ItemInput{}, err
	}
	if snapshot == nil || snapshot.GetSkuId() != skuID || snapshot.GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_ENABLED {
		return order.ItemInput{}, fmt.Errorf("product-rpc returned invalid active SKU snapshot: %w", order.ErrInvalidOrder)
	}
	return order.ItemInput{
		SKUID: snapshot.GetSkuId(), ProductName: snapshot.GetProductName(), SKUCode: snapshot.GetSkuCode(),
		SKUName: snapshot.GetSkuName(), UnitPriceCent: snapshot.GetUnitPriceCent(),
	}, nil
}

var _ order.ProductSnapshotReader = (*ProductSnapshotAdapter)(nil)
