package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"service_rpc/internal/auth"
	"service_rpc/internal/rbac"
	"service_rpc/internal/user"
)

const handlerTokenSecret = "0123456789abcdef0123456789abcdef"

type loginRepository struct {
	credential user.Credential
	findErr    error
}

func (r *loginRepository) FindCredentialByEmail(_ context.Context, _ string) (user.Credential, error) {
	return r.credential, r.findErr
}

func (r *loginRepository) FindByID(_ context.Context, id uint64) (user.User, error) {
	if r.credential.User.ID != id || !r.credential.Active {
		return user.User{}, user.ErrUserNotFound
	}
	return r.credential.User, nil
}

type loginRBACRepository struct {
	roles []string
}

func (r *loginRBACRepository) ReplaceUserRoles(_ context.Context, _ uint64, roles []string) error {
	r.roles = append([]string(nil), roles...)
	return nil
}

func (r *loginRBACRepository) UserRoles(_ context.Context, _ uint64) ([]string, error) {
	return append([]string(nil), r.roles...), nil
}

func (r *loginRBACRepository) HasPermission(_ context.Context, _ uint64, _ string) (bool, error) {
	return false, nil
}

func newLoginDependencies(t *testing.T) (*user.AuthService, *auth.TokenManager, *rbac.Service) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("generate test hash: %v", err)
	}
	service := user.NewAuthService(&loginRepository{credential: user.Credential{
		User:         user.User{ID: 13, Email: "user@example.com"},
		PasswordHash: string(hash),
		Active:       true,
	}})
	tokens, err := auth.NewTokenManager(handlerTokenSecret, 15*time.Minute)
	if err != nil {
		t.Fatalf("NewTokenManager() error: %v", err)
	}
	return service, tokens, rbac.NewService(&loginRBACRepository{roles: []string{"customer"}})
}

func TestLoginAndCurrentUserHandlers(t *testing.T) {
	service, tokens, rbacService := newLoginDependencies(t)
	login := loginHandler(service, tokens)
	request := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewBufferString(
		`{"email":"user@example.com","password":"correct-password"}`,
	))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	login(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var loginResult loginResponse
	if err := json.NewDecoder(recorder.Body).Decode(&loginResult); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	if loginResult.Data.AccessToken == "" || loginResult.Data.ExpiresInSecond != 900 {
		t.Fatalf("unexpected login response: %+v", loginResult)
	}

	me := authenticate(tokens)(currentUserHandler(service, rbacService))
	request = httptest.NewRequest(http.MethodGet, "/v1/users/me", nil)
	request.Header.Set("Authorization", "Bearer "+loginResult.Data.AccessToken)
	recorder = httptest.NewRecorder()
	me(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("me status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var meResult currentUserResponse
	if err := json.NewDecoder(recorder.Body).Decode(&meResult); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if meResult.Data.ID != 13 || meResult.Data.Email != "user@example.com" || len(meResult.Data.Roles) != 1 || meResult.Data.Roles[0] != "customer" {
		t.Fatalf("unexpected me response: %+v", meResult)
	}
}

func TestAuthenticationMiddlewareRejectsMissingAndTamperedTokens(t *testing.T) {
	service, tokens, rbacService := newLoginDependencies(t)
	handler := authenticate(tokens)(currentUserHandler(service, rbacService))

	for _, authorization := range []string{"", "Bearer invalid.token.value"} {
		request := httptest.NewRequest(http.MethodGet, "/v1/users/me", nil)
		request.Header.Set("Authorization", authorization)
		recorder := httptest.NewRecorder()
		handler(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("authorization=%q status=%d, want 401", authorization, recorder.Code)
		}
	}
}
