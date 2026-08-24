// Package product contains catalog rules and application use cases.
package product

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	StatusActive    uint8 = 1
	StatusInactive  uint8 = 2
	defaultPage           = 1
	defaultPageSize       = 20
	maxPageSize           = 100
)

var (
	ErrInvalidProduct  = errors.New("invalid product")
	ErrProductNotFound = errors.New("product not found")
	ErrProductConflict = errors.New("product conflict")
)

type SKU struct {
	ID        uint64
	Code      string
	Name      string
	PriceCent int64
	Status    uint8
}

type Product struct {
	ID          uint64
	Name        string
	Description string
	Status      uint8
	SKUs        []SKU
	CreatedAt   time.Time
}

type CreateInput struct {
	Name        string
	Description string
	SKUs        []SKU
}

type Page struct {
	Items    []Product
	Page     int
	PageSize int
	Total    int64
}

type Repository interface {
	Create(ctx context.Context, input CreateInput) (Product, error)
	Update(ctx context.Context, id uint64, name, description string) error
	SetStatus(ctx context.Context, id uint64, status uint8) error
	ListActive(ctx context.Context, offset, limit int) ([]Product, int64, error)
	FindActive(ctx context.Context, id uint64) (Product, error)
}

type Service struct {
	repository Repository
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}

// Create validates all SKU invariants before opening the repository
// transaction, preventing predictable partial-write attempts.
func (s *Service) Create(ctx context.Context, input CreateInput) (Product, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if input.Name == "" || len(input.SKUs) == 0 {
		return Product{}, ErrInvalidProduct
	}
	seen := make(map[string]struct{}, len(input.SKUs))
	for i := range input.SKUs {
		input.SKUs[i].Code = strings.TrimSpace(input.SKUs[i].Code)
		input.SKUs[i].Name = strings.TrimSpace(input.SKUs[i].Name)
		if input.SKUs[i].Code == "" || input.SKUs[i].Name == "" || input.SKUs[i].PriceCent < 0 {
			return Product{}, ErrInvalidProduct
		}
		if _, exists := seen[input.SKUs[i].Code]; exists {
			return Product{}, ErrProductConflict
		}
		seen[input.SKUs[i].Code] = struct{}{}
		input.SKUs[i].Status = StatusActive
	}
	return s.repository.Create(ctx, input)
}

func (s *Service) Update(ctx context.Context, id uint64, name, description string) error {
	name = strings.TrimSpace(name)
	if id == 0 || name == "" {
		return ErrInvalidProduct
	}
	return s.repository.Update(ctx, id, name, strings.TrimSpace(description))
}

func (s *Service) SetStatus(ctx context.Context, id uint64, status uint8) error {
	if id == 0 || (status != StatusActive && status != StatusInactive) {
		return ErrInvalidProduct
	}
	return s.repository.SetStatus(ctx, id, status)
}

func (s *Service) ListPublic(ctx context.Context, page, pageSize int) (Page, error) {
	if page <= 0 {
		page = defaultPage
	}
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	items, total, err := s.repository.ListActive(ctx, (page-1)*pageSize, pageSize)
	return Page{Items: items, Page: page, PageSize: pageSize, Total: total}, err
}

func (s *Service) GetPublic(ctx context.Context, id uint64) (Product, error) {
	if id == 0 {
		return Product{}, ErrProductNotFound
	}
	return s.repository.FindActive(ctx, id)
}
