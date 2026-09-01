package main

import (
	"context"
	"time"

	commonv1 "service_rpc/api/gen/common/v1"
	inventoryv1 "service_rpc/api/gen/inventory/v1"
	notificationv1 "service_rpc/api/gen/notification/v1"
	orderv1 "service_rpc/api/gen/order/v1"
	productv1 "service_rpc/api/gen/product/v1"
	seckillv1 "service_rpc/api/gen/seckill/v1"
	userv1 "service_rpc/api/gen/user/v1"
	"service_rpc/internal/notification"
	notificationclient "service_rpc/internal/notification/rpcclient"
	"service_rpc/internal/order"
	orderclient "service_rpc/internal/order/rpcclient"
	"service_rpc/internal/product"
	productclient "service_rpc/internal/product/rpcclient"
	"service_rpc/internal/seckill"
	"service_rpc/internal/seckill/inventoryclient"
	seckillclient "service_rpc/internal/seckill/seckillclient"
	"service_rpc/internal/user"
	userclient "service_rpc/internal/user/rpcclient"
)

type userAdapter struct{ client *userclient.Client }

type notificationAdapter struct{ client *notificationclient.Client }

func (a notificationAdapter) List(ctx context.Context, userID uint64, page, pageSize int) (notification.Page, error) {
	response, err := a.client.ListNotifications(ctx, &notificationv1.ListNotificationsRequest{
		UserId: userID, Page: &commonv1.PageRequest{Page: uint32(page), PageSize: uint32(pageSize)},
	})
	if err != nil {
		return notification.Page{}, err
	}
	items := make([]notification.Notification, 0, len(response.GetNotifications()))
	for _, item := range response.GetNotifications() {
		value := notification.Notification{
			ID: item.GetId(), UserID: item.GetUserId(), BusinessType: item.GetBusinessType(),
			Title: item.GetTitle(), Body: item.GetBody(), OrderNo: item.GetOrderNo(),
			CreatedAt: time.UnixMilli(item.GetCreatedAtMs()).UTC(),
		}
		if item.GetReadAtMs() > 0 {
			readAt := time.UnixMilli(item.GetReadAtMs()).UTC()
			value.ReadAt = &readAt
		}
		items = append(items, value)
	}
	return notification.Page{
		Items: items, Page: int(response.GetPage().GetPage()), PageSize: int(response.GetPage().GetPageSize()), Total: int64(response.GetPage().GetTotal()),
	}, nil
}

func (a notificationAdapter) MarkRead(ctx context.Context, userID, notificationID uint64) error {
	return a.client.MarkRead(ctx, &notificationv1.MarkReadRequest{UserId: userID, NotificationId: notificationID})
}

func (a userAdapter) Register(ctx context.Context, email, password string) (user.User, error) {
	response, err := a.client.Register(ctx, &userv1.RegisterRequest{Email: email, Password: password})
	if err != nil {
		return user.User{}, err
	}
	return fromProtoUser(response.GetUser()), nil
}
func (a userAdapter) Authenticate(ctx context.Context, email, password string) (user.User, []string, error) {
	response, err := a.client.Authenticate(ctx, &userv1.AuthenticateRequest{Email: email, Password: password})
	if err != nil {
		return user.User{}, nil, err
	}
	return fromProtoUser(response.GetUser()), response.GetRoleCodes(), nil
}
func (a userAdapter) GetUser(ctx context.Context, userID uint64) (user.User, error) {
	response, err := a.client.GetUser(ctx, &userv1.GetUserRequest{UserId: userID})
	if err != nil {
		return user.User{}, err
	}
	return fromProtoUser(response.GetUser()), nil
}
func (a userAdapter) GetUserRoles(ctx context.Context, userID uint64) ([]string, error) {
	response, err := a.client.GetUserRoles(ctx, &userv1.GetUserRolesRequest{UserId: userID})
	if err != nil {
		return nil, err
	}
	return response.GetRoleCodes(), nil
}
func (a userAdapter) HasPermission(ctx context.Context, userID uint64, permission string) (bool, error) {
	response, err := a.client.HasPermission(ctx, &userv1.HasPermissionRequest{UserId: userID, PermissionCode: permission})
	return response != nil && response.GetAllowed(), err
}
func (a userAdapter) ReplaceUserRoles(ctx context.Context, userID uint64, roles []string) error {
	_, err := a.client.ReplaceUserRoles(ctx, &userv1.ReplaceUserRolesRequest{UserId: userID, RoleCodes: roles})
	return err
}
func fromProtoUser(value *userv1.User) user.User {
	if value == nil {
		return user.User{}
	}
	return user.User{ID: value.GetId(), Email: value.GetEmail(), CreatedAt: time.UnixMilli(value.GetCreatedAtMs()).UTC()}
}

type productAdapter struct{ client *productclient.Client }

func (a productAdapter) List(ctx context.Context, page, pageSize int) (product.Page, error) {
	response, err := a.client.Service().ListProducts(ctx, &productv1.ListProductsRequest{Page: &commonv1.PageRequest{Page: uint32(page), PageSize: uint32(pageSize)}})
	if err != nil {
		return product.Page{}, err
	}
	items := make([]product.Product, 0, len(response.GetProducts()))
	for _, item := range response.GetProducts() {
		items = append(items, fromProtoProduct(item))
	}
	return product.Page{Items: items, Page: int(response.GetPage().GetPage()), PageSize: int(response.GetPage().GetPageSize()), Total: int64(response.GetPage().GetTotal())}, nil
}
func (a productAdapter) Get(ctx context.Context, productID uint64) (product.Product, error) {
	response, err := a.client.Service().GetProduct(ctx, &productv1.GetProductRequest{ProductId: productID})
	if err != nil {
		return product.Product{}, err
	}
	return fromProtoProduct(response.GetProduct()), nil
}
func (a productAdapter) Create(ctx context.Context, input product.CreateInput) (product.Product, error) {
	skus := make([]*productv1.SkuInput, 0, len(input.SKUs))
	for _, sku := range input.SKUs {
		skus = append(skus, &productv1.SkuInput{Code: sku.Code, Name: sku.Name, UnitPriceCent: sku.PriceCent})
	}
	response, err := a.client.Service().CreateProduct(ctx, &productv1.CreateProductRequest{Name: input.Name, Description: input.Description, Skus: skus})
	if err != nil {
		return product.Product{}, err
	}
	return fromProtoProduct(response.GetProduct()), nil
}
func (a productAdapter) Update(ctx context.Context, productID uint64, name, description string) error {
	_, err := a.client.Service().UpdateProduct(ctx, &productv1.UpdateProductRequest{ProductId: productID, Name: name, Description: description})
	return err
}
func (a productAdapter) SetStatus(ctx context.Context, productID uint64, value uint8) error {
	_, err := a.client.Service().UpdateProductStatus(ctx, &productv1.UpdateProductStatusRequest{ProductId: productID, Status: toProtoProductStatus(value)})
	return err
}
func fromProtoProduct(value *productv1.Product) product.Product {
	if value == nil {
		return product.Product{}
	}
	skus := make([]product.SKU, 0, len(value.GetSkus()))
	for _, sku := range value.GetSkus() {
		skus = append(skus, product.SKU{ID: sku.GetSkuId(), Code: sku.GetSkuCode(), Name: sku.GetSkuName(), PriceCent: sku.GetUnitPriceCent(), Status: fromProtoProductStatus(sku.GetStatus())})
	}
	return product.Product{ID: value.GetId(), Name: value.GetName(), Description: value.GetDescription(), Status: fromProtoProductStatus(value.GetStatus()), SKUs: skus, CreatedAt: time.UnixMilli(value.GetCreatedAtMs()).UTC()}
}
func fromProtoProductStatus(value productv1.ProductStatus) uint8 {
	if value == productv1.ProductStatus_PRODUCT_STATUS_ENABLED {
		return product.StatusActive
	}
	return product.StatusInactive
}
func toProtoProductStatus(value uint8) productv1.ProductStatus {
	if value == product.StatusActive {
		return productv1.ProductStatus_PRODUCT_STATUS_ENABLED
	}
	if value == product.StatusInactive {
		return productv1.ProductStatus_PRODUCT_STATUS_DISABLED
	}
	return productv1.ProductStatus_PRODUCT_STATUS_UNSPECIFIED
}

type orderAdapter struct{ client *orderclient.Client }

func (a orderAdapter) Create(ctx context.Context, userID uint64, items []order.ItemInput) (order.Order, error) {
	requested := make([]*orderv1.OrderItemInput, 0, len(items))
	for _, item := range items {
		requested = append(requested, &orderv1.OrderItemInput{SkuId: item.SKUID, Quantity: item.Quantity})
	}
	response, err := a.client.CreateOrder(ctx, &orderv1.CreateOrderRequest{UserId: userID, Items: requested})
	if err != nil {
		return order.Order{}, err
	}
	return fromProtoOrder(response.GetOrder()), nil
}
func (a orderAdapter) Get(ctx context.Context, userID, orderID uint64) (order.Order, error) {
	response, err := a.client.GetOrder(ctx, userID, orderID)
	if err != nil {
		return order.Order{}, err
	}
	return fromProtoOrder(response.GetOrder()), nil
}
func (a orderAdapter) FindSeckill(ctx context.Context, userID uint64, orderNo string) (order.Order, error) {
	response, err := a.client.FindSeckillOrder(ctx, userID, orderNo)
	if err != nil {
		return order.Order{}, err
	}
	return fromProtoOrder(response.GetOrder()), nil
}
func fromProtoOrder(value *orderv1.Order) order.Order {
	if value == nil {
		return order.Order{}
	}
	items := make([]order.Item, 0, len(value.GetItems()))
	for _, item := range value.GetItems() {
		snapshot := item.GetSnapshot()
		items = append(items, order.Item{ID: item.GetId(), SKUID: snapshot.GetSkuId(), ProductName: snapshot.GetProductName(), SKUCode: snapshot.GetSkuCode(), SKUName: snapshot.GetSkuName(), UnitPriceCent: snapshot.GetUnitPriceCent(), Quantity: snapshot.GetQuantity(), SubtotalCent: item.GetSubtotalCent()})
	}
	return order.Order{ID: value.GetId(), OrderNo: value.GetOrderNo(), UserID: value.GetUserId(), Status: uint8(value.GetStatus()), TotalAmountCent: value.GetTotalAmountCent(), Items: items, CreatedAt: time.UnixMilli(value.GetCreatedAtMs()).UTC()}
}

type inventoryAdapter struct{ client *inventoryclient.Client }

func (a inventoryAdapter) CreateSeckillItem(ctx context.Context, activityID, skuID uint64, stock int64) (seckill.Item, error) {
	response, err := a.client.CreateSeckillItem(ctx, &inventoryv1.CreateSeckillItemRequest{ActivityId: activityID, SkuId: skuID, Stock: stock})
	if err != nil {
		return seckill.Item{}, err
	}
	item := response.GetItem()
	return seckill.Item{ID: item.GetId(), ActivityID: item.GetActivityId(), SKUID: item.GetSku().GetSkuId(), InitialStock: item.GetInitialStock(), AvailableStock: item.GetAvailableStock(), Version: item.GetVersion(), CreatedAt: time.UnixMilli(item.GetCreatedAtMs()).UTC()}, nil
}

type seckillAdapter struct{ client *seckillclient.Client }

func (a seckillAdapter) CreateActivity(ctx context.Context, input seckill.CreateActivityInput) (seckill.Activity, error) {
	response, err := a.client.CreateActivity(ctx, &seckillv1.CreateActivityRequest{Name: input.Name, StartAtMs: input.StartAt.UTC().UnixMilli(), EndAtMs: input.EndAt.UTC().UnixMilli()})
	if err != nil {
		return seckill.Activity{}, err
	}
	return fromProtoActivity(response.GetActivity()), nil
}
func (a seckillAdapter) SetActivityStatus(ctx context.Context, activityID uint64, value uint8) error {
	return a.client.UpdateActivityStatus(ctx, &seckillv1.UpdateActivityStatusRequest{ActivityId: activityID, Status: toProtoActivityStatus(value)})
}
func (a seckillAdapter) PreheatActivity(ctx context.Context, activityID uint64) (seckill.PreheatResult, error) {
	response, err := a.client.PreheatActivity(ctx, activityID)
	if err != nil {
		return seckill.PreheatResult{}, err
	}
	return seckill.PreheatResult{ActivityID: response.GetActivityId(), ItemCount: int(response.GetItemCount()), EarliestExpireAt: time.UnixMilli(response.GetEarliestExpireAtMs()).UTC(), LatestExpireAt: time.UnixMilli(response.GetLatestExpireAtMs()).UTC()}, nil
}
func (a seckillAdapter) Enqueue(ctx context.Context, userID, itemID uint64) (seckill.AsyncSubmission, error) {
	response, err := a.client.Enqueue(ctx, userID, itemID)
	if err != nil {
		return seckill.AsyncSubmission{}, err
	}
	return seckill.AsyncSubmission{OrderNo: response.GetOrderNo(), Replayed: response.GetReplayed()}, nil
}
func (a seckillAdapter) GetProjectedResult(ctx context.Context, userID uint64, orderNo string) (seckill.AsyncResultStatus, error) {
	response, err := a.client.GetResult(ctx, userID, orderNo)
	if err != nil {
		return "", err
	}
	switch response.GetStatus() {
	case seckillv1.ResultStatus_RESULT_STATUS_QUEUED:
		return seckill.AsyncResultQueued, nil
	case seckillv1.ResultStatus_RESULT_STATUS_SUCCEEDED:
		return seckill.AsyncResultSucceeded, nil
	case seckillv1.ResultStatus_RESULT_STATUS_FAILED:
		return seckill.AsyncResultFailed, nil
	default:
		return "", nil
	}
}
func fromProtoActivity(value *seckillv1.Activity) seckill.Activity {
	if value == nil {
		return seckill.Activity{}
	}
	return seckill.Activity{ID: value.GetId(), Name: value.GetName(), StartAt: time.UnixMilli(value.GetStartAtMs()).UTC(), EndAt: time.UnixMilli(value.GetEndAtMs()).UTC(), Status: fromProtoActivityStatus(value.GetStatus()), CreatedAt: time.UnixMilli(value.GetCreatedAtMs()).UTC()}
}
func fromProtoActivityStatus(value seckillv1.ActivityStatus) uint8 {
	if value == seckillv1.ActivityStatus_ACTIVITY_STATUS_ENABLED {
		return seckill.StatusEnabled
	}
	return seckill.StatusDisabled
}
func toProtoActivityStatus(value uint8) seckillv1.ActivityStatus {
	if value == seckill.StatusEnabled {
		return seckillv1.ActivityStatus_ACTIVITY_STATUS_ENABLED
	}
	if value == seckill.StatusDisabled {
		return seckillv1.ActivityStatus_ACTIVITY_STATUS_DISABLED
	}
	return seckillv1.ActivityStatus_ACTIVITY_STATUS_UNSPECIFIED
}
