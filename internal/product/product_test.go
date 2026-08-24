package product

import (
	"context"
	"errors"
	"testing"
)

type recordingRepository struct {
	input      CreateInput
	offset     int
	limit      int
	createCall int
}

func (r *recordingRepository) Create(_ context.Context, input CreateInput) (Product, error) {
	r.input = input
	r.createCall++
	return Product{ID: 1, Name: input.Name, SKUs: input.SKUs}, nil
}
func (r *recordingRepository) Update(context.Context, uint64, string, string) error { return nil }
func (r *recordingRepository) SetStatus(context.Context, uint64, uint8) error       { return nil }
func (r *recordingRepository) ListActive(_ context.Context, offset, limit int) ([]Product, int64, error) {
	r.offset, r.limit = offset, limit
	return []Product{}, 0, nil
}
func (r *recordingRepository) FindActive(context.Context, uint64) (Product, error) {
	return Product{}, ErrProductNotFound
}

func TestServiceCreateValidatesSKUsAndIntegerMoney(t *testing.T) {
	repository := &recordingRepository{}
	service := NewService(repository)
	created, err := service.Create(context.Background(), CreateInput{
		Name: " Product ",
		SKUs: []SKU{{Code: "sku-1", Name: "Default", PriceCent: 1999}},
	})
	if err != nil || created.SKUs[0].PriceCent != 1999 || repository.input.Name != "Product" {
		t.Fatalf("Create()=%+v error=%v input=%+v", created, err, repository.input)
	}
	_, err = service.Create(context.Background(), CreateInput{Name: "Product", SKUs: []SKU{
		{Code: "same", Name: "A", PriceCent: 1}, {Code: "same", Name: "B", PriceCent: 2},
	}})
	if !errors.Is(err, ErrProductConflict) || repository.createCall != 1 {
		t.Fatalf("duplicate Create() error=%v calls=%d", err, repository.createCall)
	}
}

func TestServiceListPublicCapsPageSize(t *testing.T) {
	repository := &recordingRepository{}
	page, err := NewService(repository).ListPublic(context.Background(), 2, 1000)
	if err != nil || page.PageSize != 100 || repository.offset != 100 || repository.limit != 100 {
		t.Fatalf("page=%+v offset=%d limit=%d error=%v", page, repository.offset, repository.limit, err)
	}
}
