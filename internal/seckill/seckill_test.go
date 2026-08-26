package seckill

import (
	"context"
	"errors"
	"testing"
	"time"
)

type repositoryStub struct {
	createInput   CreateActivityInput
	addInput      AddItemInput
	statusID      uint64
	status        uint8
	purchaseUser  uint64
	purchaseItem  uint64
	purchaseNo    string
	purchaseNow   time.Time
	purchaseError error
}

func (r *repositoryStub) CreateActivity(_ context.Context, input CreateActivityInput) (Activity, error) {
	r.createInput = input
	return Activity{ID: 1, Name: input.Name, StartAt: input.StartAt, EndAt: input.EndAt, Status: StatusDisabled}, nil
}
func (r *repositoryStub) AddItem(_ context.Context, input AddItemInput) (Item, error) {
	r.addInput = input
	return Item{ID: 1, ActivityID: input.ActivityID, SKUID: input.SKUID, InitialStock: input.Stock, AvailableStock: input.Stock}, nil
}
func (r *repositoryStub) SetActivityStatus(_ context.Context, id uint64, status uint8) error {
	r.statusID, r.status = id, status
	return nil
}
func (r *repositoryStub) Purchase(_ context.Context, userID, itemID uint64, orderNo string, now time.Time) (PurchaseResult, error) {
	r.purchaseUser, r.purchaseItem, r.purchaseNo, r.purchaseNow = userID, itemID, orderNo, now
	return PurchaseResult{}, r.purchaseError
}

func TestCreateActivityNormalizesAndValidates(t *testing.T) {
	repository := &repositoryStub{}
	service := NewService(repository)
	start := time.Date(2026, 8, 25, 1, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	created, err := service.CreateActivity(context.Background(), CreateActivityInput{Name: "  秋招秒杀  ", StartAt: start, EndAt: start.Add(time.Hour)})
	if err != nil {
		t.Fatalf("CreateActivity() error = %v", err)
	}
	if created.Name != "秋招秒杀" || repository.createInput.StartAt.Location() != time.UTC {
		t.Fatalf("activity was not normalized: %+v", repository.createInput)
	}

	_, err = service.CreateActivity(context.Background(), CreateActivityInput{Name: "bad", StartAt: start, EndAt: start})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid time error = %v, want ErrInvalidArgument", err)
	}
}

func TestAddItemAndStatusValidation(t *testing.T) {
	service := NewService(&repositoryStub{})
	if _, err := service.AddItem(context.Background(), AddItemInput{ActivityID: 1, SKUID: 2, Stock: 10}); err != nil {
		t.Fatalf("AddItem() error = %v", err)
	}
	if _, err := service.AddItem(context.Background(), AddItemInput{ActivityID: 1, SKUID: 2, Stock: 0}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("zero stock error = %v, want ErrInvalidArgument", err)
	}
	if err := service.SetActivityStatus(context.Background(), 1, 9); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid status error = %v, want ErrInvalidArgument", err)
	}
}

func TestPurchasePassesOneStableUTCInstant(t *testing.T) {
	repository := &repositoryStub{purchaseError: ErrOutOfStock}
	service := NewService(repository)
	wantNow := time.Date(2026, 8, 25, 2, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return wantNow }

	_, err := service.Purchase(context.Background(), 7, 9)
	if !errors.Is(err, ErrOutOfStock) {
		t.Fatalf("Purchase() error = %v, want ErrOutOfStock", err)
	}
	if repository.purchaseUser != 7 || repository.purchaseItem != 9 || repository.purchaseNo == "" || !repository.purchaseNow.Equal(wantNow) {
		t.Fatalf("unexpected repository call: user=%d item=%d no=%q now=%v", repository.purchaseUser, repository.purchaseItem, repository.purchaseNo, repository.purchaseNow)
	}
}

func TestPurchaseRejectsMissingIdentityOrItem(t *testing.T) {
	service := NewService(&repositoryStub{})
	for _, input := range [][2]uint64{{0, 1}, {1, 0}} {
		if _, err := service.Purchase(context.Background(), input[0], input[1]); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("Purchase(%d, %d) error = %v, want ErrInvalidArgument", input[0], input[1], err)
		}
	}
}
