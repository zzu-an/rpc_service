package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"service_rpc/internal/auth"
	"service_rpc/internal/notification"
	"service_rpc/internal/order"
	"service_rpc/internal/product"
	"service_rpc/internal/seckill"
	"service_rpc/internal/user"

	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/rest/httpx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Gateway 的 handler 只依赖用例级小接口，不接触生成 client 的构造、服务发现或连接池。
// 这些接口也明确了身份边界：userID 只从 JWT context 取得，不从请求体透传原始 token。
type GatewayUserClient interface {
	Register(ctx context.Context, email, password string) (user.User, error)
	Authenticate(ctx context.Context, email, password string) (user.User, []string, error)
	GetUser(ctx context.Context, userID uint64) (user.User, error)
	GetUserRoles(ctx context.Context, userID uint64) ([]string, error)
	HasPermission(ctx context.Context, userID uint64, permission string) (bool, error)
	ReplaceUserRoles(ctx context.Context, userID uint64, roles []string) error
}

type GatewayProductClient interface {
	List(ctx context.Context, page, pageSize int) (product.Page, error)
	Get(ctx context.Context, productID uint64) (product.Product, error)
	Create(ctx context.Context, input product.CreateInput) (product.Product, error)
	Update(ctx context.Context, productID uint64, name, description string) error
	SetStatus(ctx context.Context, productID uint64, value uint8) error
}

type GatewayOrderClient interface {
	Create(ctx context.Context, userID uint64, items []order.ItemInput) (order.Order, error)
	Get(ctx context.Context, userID, orderID uint64) (order.Order, error)
	FindSeckill(ctx context.Context, userID uint64, orderNo string) (order.Order, error)
}

type GatewayInventoryClient interface {
	CreateSeckillItem(ctx context.Context, activityID, skuID uint64, stock int64) (seckill.Item, error)
}

type GatewaySeckillClient interface {
	CreateActivity(ctx context.Context, input seckill.CreateActivityInput) (seckill.Activity, error)
	SetActivityStatus(ctx context.Context, activityID uint64, value uint8) error
	PreheatActivity(ctx context.Context, activityID uint64) (seckill.PreheatResult, error)
	Enqueue(ctx context.Context, userID, itemID uint64) (seckill.AsyncSubmission, error)
	GetProjectedResult(ctx context.Context, userID uint64, orderNo string) (seckill.AsyncResultStatus, error)
}

type GatewayNotificationClient interface {
	List(ctx context.Context, userID uint64, page, pageSize int) (notification.Page, error)
	MarkRead(ctx context.Context, userID, notificationID uint64) error
}

type GatewayDependencies struct {
	Users         GatewayUserClient
	Products      GatewayProductClient
	Orders        GatewayOrderClient
	Inventory     GatewayInventoryClient
	Seckill       GatewaySeckillClient
	Notifications GatewayNotificationClient
}

// RegisterGatewayRPCRoutes 保持 v0.4.2 的公开 URL/JSON/JWT 契约，只替换后端调用方式。
func RegisterGatewayRPCRoutes(server *rest.Server, tokens *auth.TokenManager, dependencies GatewayDependencies) {
	server.AddRoutes([]rest.Route{
		{Method: http.MethodPost, Path: "/v1/auth/register", Handler: gatewayRegister(dependencies.Users)},
		{Method: http.MethodPost, Path: "/v1/auth/login", Handler: gatewayLogin(dependencies.Users, tokens)},
		{Method: http.MethodGet, Path: "/v1/users/me", Handler: authenticate(tokens)(gatewayCurrentUser(dependencies.Users))},
		{Method: http.MethodGet, Path: "/v1/products", Handler: gatewayListProducts(dependencies.Products)},
		{Method: http.MethodGet, Path: "/v1/products/:productId", Handler: gatewayGetProduct(dependencies.Products)},
		{Method: http.MethodPost, Path: "/v1/orders", Handler: authenticate(tokens)(gatewayCreateOrder(dependencies.Orders))},
		{Method: http.MethodGet, Path: "/v1/orders/:orderId", Handler: authenticate(tokens)(gatewayGetOrder(dependencies.Orders))},
		{Method: http.MethodPost, Path: "/v1/seckill/items/:itemId/orders", Handler: authenticate(tokens)(gatewayEnqueue(dependencies.Seckill))},
		{Method: http.MethodGet, Path: "/v1/seckill/orders/:orderNo/result", Handler: authenticate(tokens)(gatewaySeckillResult(dependencies.Orders, dependencies.Seckill))},
		{Method: http.MethodGet, Path: "/v1/notifications", Handler: authenticate(tokens)(gatewayListNotifications(dependencies.Notifications))},
		{Method: http.MethodPut, Path: "/v1/notifications/:notificationId/read", Handler: authenticate(tokens)(gatewayMarkNotificationRead(dependencies.Notifications))},
	})
	permission := func(code string, next http.HandlerFunc) http.HandlerFunc {
		return authenticate(tokens)(gatewayRequirePermission(dependencies.Users, code)(next))
	}
	server.AddRoutes([]rest.Route{
		{Method: http.MethodPut, Path: "/v1/admin/users/:userId/roles", Handler: permission("rbac:manage", gatewayReplaceRoles(dependencies.Users))},
		{Method: http.MethodPost, Path: "/v1/admin/products", Handler: permission("product:write", gatewayCreateProduct(dependencies.Products))},
		{Method: http.MethodPut, Path: "/v1/admin/products/:productId", Handler: permission("product:write", gatewayUpdateProduct(dependencies.Products))},
		{Method: http.MethodPut, Path: "/v1/admin/products/:productId/status", Handler: permission("product:write", gatewayUpdateProductStatus(dependencies.Products))},
		{Method: http.MethodPost, Path: "/v1/admin/seckill/activities", Handler: permission("seckill:write", gatewayCreateActivity(dependencies.Seckill))},
		{Method: http.MethodPost, Path: "/v1/admin/seckill/activities/:activityId/items", Handler: permission("seckill:write", gatewayCreateSeckillItem(dependencies.Inventory))},
		{Method: http.MethodPut, Path: "/v1/admin/seckill/activities/:activityId/status", Handler: permission("seckill:write", gatewayUpdateActivityStatus(dependencies.Seckill))},
		{Method: http.MethodPost, Path: "/v1/admin/seckill/activities/:activityId/preheat", Handler: permission("seckill:write", gatewayPreheatActivity(dependencies.Seckill))},
	})
}

func gatewayRegister(client GatewayUserClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request registerRequest
		if err := httpx.Parse(r, &request); err != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid request body")
			return
		}
		created, err := client.Register(r.Context(), request.Email, request.Password)
		if err != nil {
			if status.Code(err) == codes.AlreadyExists {
				writeError(r, w, http.StatusConflict, "USER_ALREADY_EXISTS", "user already exists")
				return
			}
			writeRPCError(r, w, err, "invalid email or password")
			return
		}
		httpx.OkJsonCtx(r.Context(), w, registerResponse{Code: "OK", Data: registeredUserData{ID: created.ID, Email: created.Email, CreatedAt: created.CreatedAt.UTC().Format(time.RFC3339Nano)}})
	}
}

func gatewayLogin(client GatewayUserClient, tokens *auth.TokenManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request loginRequest
		if err := httpx.Parse(r, &request); err != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid request body")
			return
		}
		authenticated, _, err := client.Authenticate(r.Context(), request.Email, request.Password)
		if err != nil {
			if status.Code(err) == codes.Unauthenticated {
				writeError(r, w, http.StatusUnauthorized, "UNAUTHENTICATED", "invalid credentials")
				return
			}
			writeRPCError(r, w, err, "invalid credentials")
			return
		}
		tokenText, err := tokens.Issue(authenticated.ID)
		if err != nil {
			writeError(r, w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			return
		}
		httpx.OkJsonCtx(r.Context(), w, loginResponse{Code: "OK", Data: loginData{AccessToken: tokenText, ExpiresInSecond: tokens.ExpiresInSeconds()}})
	}
}

func gatewayCurrentUser(client GatewayUserClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := gatewayUserID(r)
		if !ok {
			writeError(r, w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
			return
		}
		current, err := client.GetUser(r.Context(), userID)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				writeError(r, w, http.StatusUnauthorized, "UNAUTHENTICATED", "user is not active")
				return
			}
			writeRPCError(r, w, err, "invalid user")
			return
		}
		roles, err := client.GetUserRoles(r.Context(), userID)
		if err != nil {
			writeRPCError(r, w, err, "invalid user")
			return
		}
		httpx.OkJsonCtx(r.Context(), w, currentUserResponse{Code: "OK", Data: currentUserData{ID: current.ID, Email: current.Email, Roles: roles}})
	}
}

func gatewayRequirePermission(client GatewayUserClient, permission string) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			userID, ok := gatewayUserID(r)
			if !ok {
				writeError(r, w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
				return
			}
			allowed, err := client.HasPermission(r.Context(), userID, permission)
			if err != nil {
				writeRPCError(r, w, err, "permission check failed")
				return
			}
			if !allowed {
				writeError(r, w, http.StatusForbidden, "PERMISSION_DENIED", "permission denied")
				return
			}
			next(w, r)
		}
	}
}

func gatewayReplaceRoles(client GatewayUserClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request replaceUserRolesRequest
		if err := httpx.Parse(r, &request); err != nil || request.UserID == 0 {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid role assignment")
			return
		}
		if err := client.ReplaceUserRoles(r.Context(), request.UserID, request.Roles); err != nil {
			writeRPCError(r, w, err, "invalid role assignment")
			return
		}
		roles, err := client.GetUserRoles(r.Context(), request.UserID)
		if err != nil {
			writeRPCError(r, w, err, "invalid role assignment")
			return
		}
		httpx.OkJsonCtx(r.Context(), w, userRolesResponse{Code: "OK", Data: userRolesData{UserID: request.UserID, Roles: roles}})
	}
}

func gatewayListProducts(client GatewayProductClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request productListRequest
		if err := httpx.ParseForm(r, &request); err != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid pagination")
			return
		}
		// 保持旧 product.Service 的分页默认值，避免 int 转 uint32 时负数绕成超大页码。
		if request.Page <= 0 {
			request.Page = 1
		}
		if request.PageSize <= 0 {
			request.PageSize = 20
		}
		if request.PageSize > 100 {
			request.PageSize = 100
		}
		page, err := client.List(r.Context(), request.Page, request.PageSize)
		if err != nil {
			writeRPCError(r, w, err, "invalid pagination")
			return
		}
		items := make([]productPayload, 0, len(page.Items))
		for _, item := range page.Items {
			items = append(items, toProductPayload(item))
		}
		httpx.OkJsonCtx(r.Context(), w, productListResponse{Code: "OK", Data: productListData{Items: items, Page: page.Page, PageSize: page.PageSize, Total: page.Total}})
	}
}

func gatewayGetProduct(client GatewayProductClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request productPath
		if err := httpx.ParsePath(r, &request); err != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid product ID")
			return
		}
		found, err := client.Get(r.Context(), request.ProductID)
		if err != nil {
			writeRPCError(r, w, err, "invalid product")
			return
		}
		httpx.OkJsonCtx(r.Context(), w, productResponse{Code: "OK", Data: toProductPayload(found)})
	}
}

func gatewayCreateProduct(client GatewayProductClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request createProductRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid product")
			return
		}
		skus := make([]product.SKU, 0, len(request.SKUs))
		for _, sku := range request.SKUs {
			skus = append(skus, product.SKU{Code: sku.Code, Name: sku.Name, PriceCent: sku.PriceCent})
		}
		created, err := client.Create(r.Context(), product.CreateInput{Name: request.Name, Description: request.Description, SKUs: skus})
		if err != nil {
			writeRPCError(r, w, err, "invalid product")
			return
		}
		httpx.OkJsonCtx(r.Context(), w, productResponse{Code: "OK", Data: toProductPayload(created)})
	}
}

func gatewayUpdateProduct(client GatewayProductClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request updateProductRequest
		if err := httpx.ParsePath(r, &request); err != nil || json.NewDecoder(r.Body).Decode(&request) != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid product")
			return
		}
		if err := client.Update(r.Context(), request.ProductID, request.Name, request.Description); err != nil {
			writeRPCError(r, w, err, "invalid product")
			return
		}
		httpx.OkJsonCtx(r.Context(), w, productResponse{Code: "OK", Data: productPayload{ID: request.ProductID, Name: request.Name, Description: request.Description}})
	}
}

func gatewayUpdateProductStatus(client GatewayProductClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request updateProductStatusRequest
		if err := httpx.ParsePath(r, &request); err != nil || json.NewDecoder(r.Body).Decode(&request) != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid product status")
			return
		}
		if err := client.SetStatus(r.Context(), request.ProductID, request.Status); err != nil {
			writeRPCError(r, w, err, "invalid product status")
			return
		}
		httpx.OkJsonCtx(r.Context(), w, productResponse{Code: "OK", Data: productPayload{ID: request.ProductID, Status: request.Status}})
	}
}

func gatewayCreateOrder(client GatewayOrderClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := gatewayUserID(r)
		if !ok {
			writeError(r, w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
			return
		}
		var request createOrderRequest
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid order")
			return
		}
		items := make([]order.ItemInput, 0, len(request.Items))
		for _, item := range request.Items {
			items = append(items, order.ItemInput{SKUID: item.SKUID, Quantity: item.Quantity})
		}
		created, err := client.Create(r.Context(), userID, items)
		if err != nil {
			writeRPCError(r, w, err, "invalid order")
			return
		}
		httpx.OkJsonCtx(r.Context(), w, orderResponse{Code: "OK", Data: toOrderPayload(created)})
	}
}

func gatewayGetOrder(client GatewayOrderClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := gatewayUserID(r)
		if !ok {
			writeError(r, w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
			return
		}
		var request orderPath
		if err := httpx.ParsePath(r, &request); err != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid order ID")
			return
		}
		found, err := client.Get(r.Context(), userID, request.OrderID)
		if err != nil {
			writeRPCError(r, w, err, "invalid order")
			return
		}
		httpx.OkJsonCtx(r.Context(), w, orderResponse{Code: "OK", Data: toOrderPayload(found)})
	}
}

func gatewayCreateActivity(client GatewaySeckillClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request createSeckillActivityRequest
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid seckill activity")
			return
		}
		start, startErr := time.Parse(time.RFC3339Nano, request.StartAt)
		end, endErr := time.Parse(time.RFC3339Nano, request.EndAt)
		if startErr != nil || endErr != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid seckill activity time")
			return
		}
		created, err := client.CreateActivity(r.Context(), seckill.CreateActivityInput{Name: request.Name, StartAt: start, EndAt: end})
		if err != nil {
			writeRPCError(r, w, err, "invalid seckill configuration")
			return
		}
		httpx.OkJsonCtx(r.Context(), w, seckillActivityResponse{Code: "OK", Data: toSeckillActivityPayload(created)})
	}
}

func gatewayCreateSeckillItem(client GatewayInventoryClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request addSeckillItemRequest
		if err := httpx.ParsePath(r, &request); err != nil || json.NewDecoder(r.Body).Decode(&request) != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid seckill item")
			return
		}
		created, err := client.CreateSeckillItem(r.Context(), request.ActivityID, request.SKUID, request.Stock)
		if err != nil {
			writeRPCError(r, w, err, "invalid seckill item")
			return
		}
		httpx.OkJsonCtx(r.Context(), w, seckillItemResponse{Code: "OK", Data: toSeckillItemPayload(created)})
	}
}

func gatewayUpdateActivityStatus(client GatewaySeckillClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request updateSeckillActivityStatusRequest
		if err := httpx.ParsePath(r, &request); err != nil || json.NewDecoder(r.Body).Decode(&request) != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid seckill activity status")
			return
		}
		if err := client.SetActivityStatus(r.Context(), request.ActivityID, request.Status); err != nil {
			writeRPCError(r, w, err, "invalid seckill configuration")
			return
		}
		httpx.OkJsonCtx(r.Context(), w, seckillActivityResponse{Code: "OK", Data: seckillActivityPayload{ID: request.ActivityID, Status: request.Status}})
	}
}

func gatewayPreheatActivity(client GatewaySeckillClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request seckillActivityPath
		if err := httpx.ParsePath(r, &request); err != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid seckill activity ID")
			return
		}
		result, err := client.PreheatActivity(r.Context(), request.ActivityID)
		if err != nil {
			writeRPCError(r, w, err, "invalid seckill configuration")
			return
		}
		httpx.OkJsonCtx(r.Context(), w, seckillPreheatResponse{Code: "OK", Data: seckillPreheatPayload{
			ActivityID: result.ActivityID, ItemCount: result.ItemCount,
			EarliestExpireAt: result.EarliestExpireAt.UTC().Format(time.RFC3339Nano), LatestExpireAt: result.LatestExpireAt.UTC().Format(time.RFC3339Nano),
		}})
	}
}

func gatewayEnqueue(client GatewaySeckillClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := gatewayUserID(r)
		if !ok {
			writeError(r, w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
			return
		}
		var request seckillItemPath
		if err := httpx.ParsePath(r, &request); err != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid seckill item ID")
			return
		}
		result, err := client.Enqueue(r.Context(), userID, request.ItemID)
		if err != nil {
			writeRPCError(r, w, err, "invalid seckill purchase")
			return
		}
		// 202 的唯一含义是 seckill-rpc 已完成 Lua + XADD；绝不等待 inventory/order/Kafka。
		httpx.WriteJsonCtx(r.Context(), w, http.StatusAccepted, queuedSeckillOrderResponse{Code: "OK", Data: queuedSeckillOrderData{OrderNo: result.OrderNo, Status: "QUEUED", Replayed: result.Replayed}})
	}
}

func gatewaySeckillResult(orders GatewayOrderClient, queue GatewaySeckillClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := gatewayUserID(r)
		if !ok {
			writeError(r, w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
			return
		}
		var request seckillOrderResultPath
		if err := httpx.ParsePath(r, &request); err != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid seckill order number")
			return
		}
		created, err := orders.FindSeckill(r.Context(), userID, request.OrderNo)
		if err == nil {
			payload := toOrderPayload(created)
			httpx.OkJsonCtx(r.Context(), w, seckillOrderResultResponse{Code: "OK", Data: seckillOrderResultData{OrderNo: request.OrderNo, Status: string(seckill.AsyncResultSucceeded), Order: &payload}})
			return
		}
		if status.Code(err) != codes.NotFound {
			writeRPCError(r, w, err, "seckill result unavailable")
			return
		}
		// 订单事实不存在时才读取短期 Redis 投影。反过来会把“订单已提交、投影未刷新”误报 QUEUED。
		projection, err := queue.GetProjectedResult(r.Context(), userID, request.OrderNo)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				writeError(r, w, http.StatusNotFound, "SECKILL_ORDER_NOT_FOUND", "seckill order request not found")
				return
			}
			writeRPCError(r, w, err, "seckill result unavailable")
			return
		}
		httpx.OkJsonCtx(r.Context(), w, seckillOrderResultResponse{Code: "OK", Data: seckillOrderResultData{OrderNo: request.OrderNo, Status: string(projection)}})
	}
}

func gatewayUserID(r *http.Request) (uint64, bool) {
	value, ok := r.Context().Value(authenticatedUserIDKey{}).(uint64)
	return value, ok && value != 0
}

// writeRPCError 是 gateway 唯一的依赖故障映射门。Deadline/Unavailable 必须分别稳定为
// 504/503，且绝不回退本地 repository；回退会绕过服务所有权并把局部故障放大成数据库洪峰。
func writeRPCError(r *http.Request, w http.ResponseWriter, err error, invalidMessage string) {
	switch status.Code(err) {
	case codes.InvalidArgument:
		writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", invalidMessage)
	case codes.NotFound:
		writeError(r, w, http.StatusNotFound, "NOT_FOUND", "resource not found")
	case codes.AlreadyExists, codes.FailedPrecondition:
		writeError(r, w, http.StatusConflict, "CONFLICT", "request conflict")
	case codes.Unauthenticated:
		writeError(r, w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication failed")
	case codes.PermissionDenied:
		writeError(r, w, http.StatusForbidden, "PERMISSION_DENIED", "permission denied")
	case codes.ResourceExhausted:
		writeError(r, w, http.StatusTooManyRequests, "RESOURCE_EXHAUSTED", "too many requests")
	case codes.DeadlineExceeded:
		writeError(r, w, http.StatusGatewayTimeout, "UPSTREAM_TIMEOUT", "upstream service timeout")
	case codes.Unavailable:
		writeError(r, w, http.StatusServiceUnavailable, "UPSTREAM_UNAVAILABLE", "upstream service unavailable")
	default:
		writeError(r, w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
	}
}
