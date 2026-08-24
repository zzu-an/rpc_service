package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/rest/httpx"

	"service_rpc/internal/auth"
	"service_rpc/internal/product"
	"service_rpc/internal/rbac"
)

type skuPayload struct {
	ID        uint64 `json:"id,omitempty"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	PriceCent int64  `json:"price_cent"`
}

type productPayload struct {
	ID          uint64       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Status      uint8        `json:"status"`
	SKUs        []skuPayload `json:"skus,omitempty"`
	CreatedAt   string       `json:"created_at,omitempty"`
}

type productResponse struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Data      productPayload `json:"data"`
	RequestID string         `json:"request_id"`
}

type productListData struct {
	Items    []productPayload `json:"items"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
	Total    int64            `json:"total"`
}

type productListResponse struct {
	Code      string          `json:"code"`
	Message   string          `json:"message"`
	Data      productListData `json:"data"`
	RequestID string          `json:"request_id"`
}

type productListRequest struct {
	Page     int `form:"page"`
	PageSize int `form:"page_size"`
}

type productPath struct {
	ProductID uint64 `path:"productId"`
}

type createProductRequest struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	SKUs        []skuPayload `json:"skus"`
}

type updateProductRequest struct {
	ProductID   uint64 `path:"productId"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type updateProductStatusRequest struct {
	ProductID uint64 `path:"productId"`
	Status    uint8  `json:"status"`
}

// RegisterProductRoutes exposes public catalog reads and protects all writes
// with the existing product:write permission.
func RegisterProductRoutes(server *rest.Server, tokens *auth.TokenManager, rbacService *rbac.Service, service *product.Service) {
	server.AddRoutes([]rest.Route{
		{Method: http.MethodGet, Path: "/v1/products", Handler: listProductsHandler(service)},
		{Method: http.MethodGet, Path: "/v1/products/:productId", Handler: getProductHandler(service)},
	})
	protectWrite := func(next http.HandlerFunc) http.HandlerFunc {
		return authenticate(tokens)(requirePermission(rbacService, "product:write")(next))
	}
	server.AddRoutes([]rest.Route{
		{Method: http.MethodPost, Path: "/v1/admin/products", Handler: protectWrite(createProductHandler(service))},
		{Method: http.MethodPut, Path: "/v1/admin/products/:productId", Handler: protectWrite(updateProductHandler(service))},
		{Method: http.MethodPut, Path: "/v1/admin/products/:productId/status", Handler: protectWrite(updateProductStatusHandler(service))},
	})
}

func listProductsHandler(service *product.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request productListRequest
		if err := httpx.ParseForm(r, &request); err != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid pagination")
			return
		}
		page, err := service.ListPublic(r.Context(), request.Page, request.PageSize)
		if err != nil {
			writeError(r, w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			return
		}
		items := make([]productPayload, 0, len(page.Items))
		for _, item := range page.Items {
			items = append(items, toProductPayload(item))
		}
		httpx.OkJsonCtx(r.Context(), w, productListResponse{Code: "OK", Data: productListData{Items: items, Page: page.Page, PageSize: page.PageSize, Total: page.Total}})
	}
}

func getProductHandler(service *product.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request productPath
		if err := httpx.ParsePath(r, &request); err != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid product ID")
			return
		}
		item, err := service.GetPublic(r.Context(), request.ProductID)
		if err != nil {
			writeProductError(r, w, err)
			return
		}
		httpx.OkJsonCtx(r.Context(), w, productResponse{Code: "OK", Data: toProductPayload(item)})
	}
}

func createProductHandler(service *product.Service) http.HandlerFunc {
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
		created, err := service.Create(r.Context(), product.CreateInput{Name: request.Name, Description: request.Description, SKUs: skus})
		if err != nil {
			writeProductError(r, w, err)
			return
		}
		httpx.OkJsonCtx(r.Context(), w, productResponse{Code: "OK", Data: toProductPayload(created)})
	}
}

func updateProductHandler(service *product.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request updateProductRequest
		if err := httpx.ParsePath(r, &request); err != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid product")
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid product")
			return
		}
		if err := service.Update(r.Context(), request.ProductID, request.Name, request.Description); err != nil {
			writeProductError(r, w, err)
			return
		}
		httpx.OkJsonCtx(r.Context(), w, productResponse{Code: "OK", Data: productPayload{ID: request.ProductID, Name: request.Name, Description: request.Description}})
	}
}

func updateProductStatusHandler(service *product.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request updateProductStatusRequest
		if err := httpx.ParsePath(r, &request); err != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid product status")
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid product status")
			return
		}
		if err := service.SetStatus(r.Context(), request.ProductID, request.Status); err != nil {
			writeProductError(r, w, err)
			return
		}
		httpx.OkJsonCtx(r.Context(), w, productResponse{Code: "OK", Data: productPayload{ID: request.ProductID, Status: request.Status}})
	}
}

func writeProductError(r *http.Request, w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, product.ErrInvalidProduct):
		writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid product")
	case errors.Is(err, product.ErrProductNotFound):
		writeError(r, w, http.StatusNotFound, "PRODUCT_NOT_FOUND", "product not found")
	case errors.Is(err, product.ErrProductConflict):
		writeError(r, w, http.StatusConflict, "PRODUCT_CONFLICT", "product conflict")
	default:
		writeError(r, w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
	}
}

func toProductPayload(item product.Product) productPayload {
	skus := make([]skuPayload, 0, len(item.SKUs))
	for _, sku := range item.SKUs {
		skus = append(skus, skuPayload{ID: sku.ID, Code: sku.Code, Name: sku.Name, PriceCent: sku.PriceCent})
	}
	createdAt := ""
	if !item.CreatedAt.IsZero() {
		createdAt = item.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	return productPayload{ID: item.ID, Name: item.Name, Description: item.Description, Status: item.Status, SKUs: skus, CreatedAt: createdAt}
}
