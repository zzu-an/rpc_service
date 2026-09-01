// Package rpcserver 把商品领域用例适配为 product.v1 RPC。
package rpcserver

import (
	"context"
	"errors"

	commonv1 "service_rpc/api/gen/common/v1"
	productv1 "service_rpc/api/gen/product/v1"
	platformrpc "service_rpc/internal/platform/rpc"
	"service_rpc/internal/product"

	"google.golang.org/grpc/codes"
)

type Server struct {
	productv1.UnimplementedProductServiceServer
	service *product.Service
}

func New(service *product.Service) *Server { return &Server{service: service} }

func (s *Server) ListProducts(ctx context.Context, request *productv1.ListProductsRequest) (*productv1.ListProductsResponse, error) {
	pageRequest := request.GetPage()
	page, err := s.service.ListPublic(ctx, int(pageRequest.GetPage()), int(pageRequest.GetPageSize()))
	if err != nil {
		return nil, mapError(err)
	}
	items := make([]*productv1.Product, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, toProtoProduct(item))
	}
	return &productv1.ListProductsResponse{Products: items, Page: &commonv1.PageResponse{
		Total: uint64(page.Total), Page: uint32(page.Page), PageSize: uint32(page.PageSize),
	}}, nil
}

func (s *Server) GetProduct(ctx context.Context, request *productv1.GetProductRequest) (*productv1.GetProductResponse, error) {
	found, err := s.service.GetPublic(ctx, request.GetProductId())
	if err != nil {
		return nil, mapError(err)
	}
	return &productv1.GetProductResponse{Product: toProtoProduct(found)}, nil
}

func (s *Server) CreateProduct(ctx context.Context, request *productv1.CreateProductRequest) (*productv1.CreateProductResponse, error) {
	skus := make([]product.SKU, 0, len(request.GetSkus()))
	for _, sku := range request.GetSkus() {
		skus = append(skus, product.SKU{Code: sku.GetCode(), Name: sku.GetName(), PriceCent: sku.GetUnitPriceCent()})
	}
	// deprecated status 有意忽略：创建固定为 inactive，启用必须走独立写 RPC，便于审计权限和失败重试。
	created, err := s.service.Create(ctx, product.CreateInput{Name: request.GetName(), Description: request.GetDescription(), SKUs: skus})
	if err != nil {
		return nil, mapError(err)
	}
	return &productv1.CreateProductResponse{Product: toProtoProduct(created)}, nil
}

func (s *Server) UpdateProduct(ctx context.Context, request *productv1.UpdateProductRequest) (*productv1.UpdateProductResponse, error) {
	if err := s.service.Update(ctx, request.GetProductId(), request.GetName(), request.GetDescription()); err != nil {
		return nil, mapError(err)
	}
	return &productv1.UpdateProductResponse{Product: &productv1.Product{
		Id: request.GetProductId(), Name: request.GetName(), Description: request.GetDescription(),
	}}, nil
}

func (s *Server) UpdateProductStatus(ctx context.Context, request *productv1.UpdateProductStatusRequest) (*productv1.UpdateProductStatusResponse, error) {
	if err := s.service.SetStatus(ctx, request.GetProductId(), fromProtoStatus(request.GetStatus())); err != nil {
		return nil, mapError(err)
	}
	return &productv1.UpdateProductStatusResponse{}, nil
}

func (s *Server) GetSkuSnapshot(ctx context.Context, request *productv1.GetSkuSnapshotRequest) (*productv1.GetSkuSnapshotResponse, error) {
	snapshot, err := s.service.GetActiveSKUSnapshot(ctx, request.GetSkuId())
	if err != nil {
		return nil, mapError(err)
	}
	// Snapshot 是值复制契约：inventory 会冻结它，绝不能拿 product_id 去跨库 JOIN 或在落单时重新查价格。
	return &productv1.GetSkuSnapshotResponse{Snapshot: &productv1.SkuSnapshot{
		SkuId: snapshot.SKUID, ProductId: snapshot.ProductID, ProductName: snapshot.ProductName,
		SkuCode: snapshot.SKUCode, SkuName: snapshot.SKUName, UnitPriceCent: snapshot.PriceCent,
		Status: toProtoStatus(snapshot.SKUStatus),
	}}, nil
}

func toProtoProduct(value product.Product) *productv1.Product {
	skus := make([]*productv1.SkuSnapshot, 0, len(value.SKUs))
	for _, sku := range value.SKUs {
		skus = append(skus, &productv1.SkuSnapshot{
			SkuId: sku.ID, ProductId: value.ID, ProductName: value.Name, SkuCode: sku.Code,
			SkuName: sku.Name, UnitPriceCent: sku.PriceCent, Status: toProtoStatus(sku.Status),
		})
	}
	return &productv1.Product{Id: value.ID, Name: value.Name, Description: value.Description,
		Status: toProtoStatus(value.Status), Skus: skus, CreatedAtMs: value.CreatedAt.UTC().UnixMilli()}
}

func toProtoStatus(value uint8) productv1.ProductStatus {
	switch value {
	case product.StatusActive:
		return productv1.ProductStatus_PRODUCT_STATUS_ENABLED
	case product.StatusInactive:
		return productv1.ProductStatus_PRODUCT_STATUS_DISABLED
	default:
		return productv1.ProductStatus_PRODUCT_STATUS_UNSPECIFIED
	}
}

func fromProtoStatus(value productv1.ProductStatus) uint8 {
	switch value {
	case productv1.ProductStatus_PRODUCT_STATUS_ENABLED:
		return product.StatusActive
	case productv1.ProductStatus_PRODUCT_STATUS_DISABLED:
		return product.StatusInactive
	default:
		return 0
	}
}

func mapError(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.Canceled, commonv1.ErrorCode_ERROR_CODE_TEMPORARILY_UNAVAILABLE, "request canceled", true, err))
	case errors.Is(err, context.DeadlineExceeded):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.DeadlineExceeded, commonv1.ErrorCode_ERROR_CODE_TEMPORARILY_UNAVAILABLE, "product service timeout", true, err))
	case errors.Is(err, product.ErrInvalidProduct):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.InvalidArgument, commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid product", false, err))
	case errors.Is(err, product.ErrProductNotFound), errors.Is(err, product.ErrSKUNotFound):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.NotFound, commonv1.ErrorCode_ERROR_CODE_NOT_FOUND, "product not found", false, err))
	case errors.Is(err, product.ErrProductConflict):
		return platformrpc.StatusError(platformrpc.NewPublicError(codes.AlreadyExists, commonv1.ErrorCode_ERROR_CODE_CONFLICT, "product conflict", false, err))
	default:
		return platformrpc.StatusError(err)
	}
}
