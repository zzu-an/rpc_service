package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zeromicro/go-zero/rest/pathvar"

	"service_rpc/internal/seckill"
)

type seckillAdminRepository struct {
	activityInput seckill.CreateActivityInput
	itemInput     seckill.AddItemInput
	statusID      uint64
	status        uint8
}

func (r *seckillAdminRepository) CreateActivity(_ context.Context, input seckill.CreateActivityInput) (seckill.Activity, error) {
	r.activityInput = input
	return seckill.Activity{ID: 11, Name: input.Name, StartAt: input.StartAt, EndAt: input.EndAt, Status: seckill.StatusDisabled}, nil
}
func (r *seckillAdminRepository) AddItem(_ context.Context, input seckill.AddItemInput) (seckill.Item, error) {
	r.itemInput = input
	return seckill.Item{ID: 12, ActivityID: input.ActivityID, SKUID: input.SKUID, InitialStock: input.Stock, AvailableStock: input.Stock}, nil
}
func (r *seckillAdminRepository) SetActivityStatus(_ context.Context, id uint64, status uint8) error {
	r.statusID, r.status = id, status
	return nil
}
func (r *seckillAdminRepository) Purchase(context.Context, uint64, uint64, string, time.Time) (seckill.PurchaseResult, error) {
	return seckill.PurchaseResult{}, nil
}

func TestCreateSeckillActivityHandlerParsesRFC3339(t *testing.T) {
	repository := &seckillAdminRepository{}
	service := seckill.NewService(repository)
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/seckill/activities", bytes.NewBufferString(`{"name":"活动","start_at":"2026-08-25T01:00:00+08:00","end_at":"2026-08-25T02:00:00+08:00"}`))
	recorder := httptest.NewRecorder()
	createSeckillActivityHandler(service)(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if repository.activityInput.StartAt.Location() != time.UTC || repository.activityInput.Name != "活动" {
		t.Fatalf("unexpected input: %+v", repository.activityInput)
	}
	var response seckillActivityResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil || response.Data.ID != 11 {
		t.Fatalf("response=%+v error=%v", response, err)
	}
}

func TestCreateSeckillActivityHandlerRejectsInvalidTime(t *testing.T) {
	service := seckill.NewService(&seckillAdminRepository{})
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/seckill/activities", bytes.NewBufferString(`{"name":"活动","start_at":"bad","end_at":"bad"}`))
	recorder := httptest.NewRecorder()
	createSeckillActivityHandler(service)(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", recorder.Code, recorder.Body.String())
	}
}

func TestAddItemAndUpdateActivityStatusHandlers(t *testing.T) {
	repository := &seckillAdminRepository{}
	service := seckill.NewService(repository)

	request := httptest.NewRequest(http.MethodPost, "/v1/admin/seckill/activities/11/items", bytes.NewBufferString(`{"sku_id":21,"stock":10}`))
	request = pathvar.WithVars(request, map[string]string{"activityId": "11"})
	recorder := httptest.NewRecorder()
	addSeckillItemHandler(service)(recorder, request)
	if recorder.Code != http.StatusOK || repository.itemInput.ActivityID != 11 || repository.itemInput.SKUID != 21 {
		t.Fatalf("status=%d input=%+v body=%s", recorder.Code, repository.itemInput, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPut, "/v1/admin/seckill/activities/11/status", bytes.NewBufferString(`{"status":1}`))
	request = pathvar.WithVars(request, map[string]string{"activityId": "11"})
	recorder = httptest.NewRecorder()
	updateSeckillActivityStatusHandler(service)(recorder, request)
	if recorder.Code != http.StatusOK || repository.statusID != 11 || repository.status != seckill.StatusEnabled {
		t.Fatalf("status=%d id=%d value=%d body=%s", recorder.Code, repository.statusID, repository.status, recorder.Body.String())
	}
}
