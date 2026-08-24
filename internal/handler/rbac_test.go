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
)

type handlerRBACRepository struct {
	allowed    bool
	roles      []string
	replaceErr error
}

func (r *handlerRBACRepository) ReplaceUserRoles(_ context.Context, _ uint64, roles []string) error {
	if r.replaceErr != nil {
		return r.replaceErr
	}
	r.roles = append([]string(nil), roles...)
	return nil
}

func (r *handlerRBACRepository) UserRoles(_ context.Context, _ uint64) ([]string, error) {
	return append([]string(nil), r.roles...), nil
}

func (r *handlerRBACRepository) HasPermission(_ context.Context, _ uint64, permissionCode string) (bool, error) {
	return r.allowed && permissionCode == "rbac:manage", nil
}

func TestReplaceUserRolesAuthorization(t *testing.T) {
	tokens, err := auth.NewTokenManager(handlerTokenSecret, 15*time.Minute)
	if err != nil {
		t.Fatalf("NewTokenManager() error: %v", err)
	}
	tokenText, err := tokens.Issue(13)
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}

	t.Run("permission denied", func(t *testing.T) {
		repository := &handlerRBACRepository{}
		handler := authenticate(tokens)(requirePermission(rbac.NewService(repository), "rbac:manage")(
			replaceUserRolesHandler(rbac.NewService(repository)),
		))
		request := httptest.NewRequest(http.MethodPut, "/v1/admin/users/22/roles", bytes.NewBufferString(`{"roles":["customer"]}`))
		request.Header.Set("Authorization", "Bearer "+tokenText)
		request.Header.Set("Content-Type", "application/json")
		request = pathvar.WithVars(request, map[string]string{"userId": "22"})
		recorder := httptest.NewRecorder()
		handler(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("status=%d body=%s, want 403", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("allowed", func(t *testing.T) {
		repository := &handlerRBACRepository{allowed: true}
		service := rbac.NewService(repository)
		handler := authenticate(tokens)(requirePermission(service, "rbac:manage")(
			replaceUserRolesHandler(service),
		))
		request := httptest.NewRequest(http.MethodPut, "/v1/admin/users/22/roles", bytes.NewBufferString(`{"roles":["customer"]}`))
		request.Header.Set("Authorization", "Bearer "+tokenText)
		request.Header.Set("Content-Type", "application/json")
		request = pathvar.WithVars(request, map[string]string{"userId": "22"})
		recorder := httptest.NewRecorder()
		handler(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s, want 200", recorder.Code, recorder.Body.String())
		}
		var response userRolesResponse
		if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if response.Data.UserID != 22 || len(response.Data.Roles) != 1 || response.Data.Roles[0] != "customer" {
			t.Fatalf("unexpected response: %+v", response)
		}
	})
}
