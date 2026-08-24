package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zeromicro/go-zero/rest/pathvar"

	"service_rpc/internal/product"
)

type productRepository struct{ created product.Product }

func (r *productRepository) Create(_ context.Context, input product.CreateInput) (product.Product, error) {
	r.created = product.Product{ID: 8, Name: input.Name, Status: product.StatusInactive, SKUs: input.SKUs}
	return r.created, nil
}
func (r *productRepository) Update(context.Context, uint64, string, string) error { return nil }
func (r *productRepository) SetStatus(context.Context, uint64, uint8) error       { return nil }
func (r *productRepository) ListActive(context.Context, int, int) ([]product.Product, int64, error) {
	return []product.Product{{ID: 8, Name: "Public", Status: product.StatusActive}}, 1, nil
}
func (r *productRepository) FindActive(context.Context, uint64) (product.Product, error) {
	return product.Product{ID: 8, Name: "Public", Status: product.StatusActive, SKUs: []product.SKU{{ID: 3, Code: "sku", Name: "Default", PriceCent: 1999}}}, nil
}

func TestProductHandlersUseIntegerMoney(t *testing.T) {
	service := product.NewService(&productRepository{})
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/products", bytes.NewBufferString(`{"name":"Product","skus":[{"code":"sku","name":"Default","price_cent":1999}]}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	createProductHandler(service)(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response productResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.Data.SKUs[0].PriceCent != 1999 {
		t.Fatalf("price_cent=%d", response.Data.SKUs[0].PriceCent)
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/products/8", nil)
	request = pathvar.WithVars(request, map[string]string{"productId": "8"})
	recorder = httptest.NewRecorder()
	getProductHandler(service)(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
