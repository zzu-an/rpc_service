package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"service_rpc/internal/user"
)

type handlerUserRepository struct {
	createErr error
}

func (r *handlerUserRepository) Create(_ context.Context, email, _ string) (user.User, error) {
	return user.User{ID: 7, Email: email, CreatedAt: time.Unix(1, 0).UTC()}, r.createErr
}

func TestRegisterUserHandler(t *testing.T) {
	service := user.NewService(&handlerUserRepository{})
	handler := registerUserHandler(service)
	body := []byte(`{"email":" USER@example.com ","password":"password123"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "password123") || strings.Contains(recorder.Body.String(), "$2") {
		t.Fatal("response leaked password material")
	}
	var response registerResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != "OK" || response.Data.ID != 7 || response.Data.Email != "user@example.com" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestRegisterUserHandlerDuplicate(t *testing.T) {
	service := user.NewService(&handlerUserRepository{createErr: user.ErrUserAlreadyExists})
	handler := registerUserHandler(service)
	body := []byte(`{"email":"user@example.com","password":"password123"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler(recorder, req)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", recorder.Code, recorder.Body.String())
	}
	var response errorResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != "USER_ALREADY_EXISTS" {
		t.Fatalf("code = %q, want USER_ALREADY_EXISTS", response.Code)
	}
}
