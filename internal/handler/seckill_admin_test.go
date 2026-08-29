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

	"service_rpc/internal/auth"
	"service_rpc/internal/rbac"
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

type adminSnapshotReader struct {
	snapshot seckill.PreheatSnapshot
}

func (r adminSnapshotReader) LoadPreheatSnapshot(context.Context, uint64) (seckill.PreheatSnapshot, error) {
	return r.snapshot, nil
}

type adminActivityCache struct {
	result seckill.PreheatResult
	err    error
}

func (c adminActivityCache) PublishActivity(context.Context, seckill.PreheatSnapshot, time.Time) (seckill.PreheatResult, error) {
	return c.result, c.err
}

func (adminActivityCache) InvalidateItems(context.Context, []uint64) error { return nil }

func TestPreheatSeckillActivityHandler(t *testing.T) {
	now := time.Now().UTC()
	want := seckill.PreheatResult{
		ActivityID: 11, ItemCount: 2,
		EarliestExpireAt: now.Add(time.Hour), LatestExpireAt: now.Add(time.Hour + time.Minute),
	}
	service, err := seckill.NewServiceWithCache(
		&seckillAdminRepository{},
		adminSnapshotReader{snapshot: seckill.PreheatSnapshot{
			Activity: seckill.Activity{ID: 11, Status: seckill.StatusEnabled, StartAt: now.Add(time.Minute), EndAt: now.Add(time.Hour)},
			Items:    []seckill.Item{{ID: 12, ActivityID: 11, SKUID: 21, InitialStock: 2, AvailableStock: 2}},
		}},
		adminActivityCache{result: want},
	)
	if err != nil {
		t.Fatalf("NewServiceWithCache() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/seckill/activities/11/preheat", nil)
	request = pathvar.WithVars(request, map[string]string{"activityId": "11"})
	recorder := httptest.NewRecorder()
	preheatSeckillActivityHandler(service)(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response seckillPreheatResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Data.ActivityID != 11 || response.Data.ItemCount != 2 || response.Data.EarliestExpireAt == "" {
		t.Fatalf("response = %+v", response)
	}
}

func TestPreheatSeckillActivityHandlerMapsCacheFailure(t *testing.T) {
	service, err := seckill.NewServiceWithCache(
		&seckillAdminRepository{},
		adminSnapshotReader{snapshot: seckill.PreheatSnapshot{
			Activity: seckill.Activity{ID: 11, Status: seckill.StatusEnabled, StartAt: time.Now().Add(time.Minute), EndAt: time.Now().Add(time.Hour)},
			Items:    []seckill.Item{{ID: 12, ActivityID: 11, SKUID: 21, InitialStock: 1, AvailableStock: 1}},
		}},
		adminActivityCache{err: seckill.ErrAdmissionFailure},
	)
	if err != nil {
		t.Fatalf("NewServiceWithCache() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/seckill/activities/11/preheat", nil)
	request = pathvar.WithVars(request, map[string]string{"activityId": "11"})
	recorder := httptest.NewRecorder()
	preheatSeckillActivityHandler(service)(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable || !bytes.Contains(recorder.Body.Bytes(), []byte("SECKILL_TEMPORARILY_UNAVAILABLE")) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

type seckillPermissionRepository struct {
	allowed bool
}

func (*seckillPermissionRepository) ReplaceUserRoles(context.Context, uint64, []string) error {
	return nil
}

func (*seckillPermissionRepository) UserRoles(context.Context, uint64) ([]string, error) {
	return nil, nil
}

func (r *seckillPermissionRepository) HasPermission(_ context.Context, _ uint64, permission string) (bool, error) {
	return r.allowed && permission == "seckill:write", nil
}

func TestPreheatSeckillActivityAuthorization(t *testing.T) {
	tokens, err := auth.NewTokenManager(handlerTokenSecret, 15*time.Minute)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	token, err := tokens.Issue(7)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	now := time.Now().UTC()
	service, err := seckill.NewServiceWithCache(
		&seckillAdminRepository{},
		adminSnapshotReader{snapshot: seckill.PreheatSnapshot{
			Activity: seckill.Activity{ID: 11, Status: seckill.StatusEnabled, StartAt: now.Add(time.Minute), EndAt: now.Add(time.Hour)},
			Items:    []seckill.Item{{ID: 12, ActivityID: 11, SKUID: 21, InitialStock: 1, AvailableStock: 1}},
		}},
		adminActivityCache{result: seckill.PreheatResult{ActivityID: 11, ItemCount: 1, EarliestExpireAt: now.Add(time.Hour), LatestExpireAt: now.Add(time.Hour)}},
	)
	if err != nil {
		t.Fatalf("NewServiceWithCache() error = %v", err)
	}

	tests := []struct {
		name       string
		authorized bool
		allowed    bool
		wantStatus int
	}{
		{name: "missing token", wantStatus: http.StatusUnauthorized},
		{name: "permission denied", authorized: true, wantStatus: http.StatusForbidden},
		{name: "administrator", authorized: true, allowed: true, wantStatus: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			permissionService := rbac.NewService(&seckillPermissionRepository{allowed: tt.allowed})
			handler := authenticate(tokens)(requirePermission(permissionService, "seckill:write")(preheatSeckillActivityHandler(service)))
			request := httptest.NewRequest(http.MethodPost, "/v1/admin/seckill/activities/11/preheat", nil)
			request = pathvar.WithVars(request, map[string]string{"activityId": "11"})
			if tt.authorized {
				request.Header.Set("Authorization", "Bearer "+token)
			}
			recorder := httptest.NewRecorder()
			handler(recorder, request)
			if recorder.Code != tt.wantStatus {
				t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), tt.wantStatus)
			}
		})
	}
}
