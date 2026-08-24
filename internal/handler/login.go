package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/rest/httpx"

	"service_rpc/internal/auth"
	"service_rpc/internal/rbac"
	"service_rpc/internal/user"
)

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginData struct {
	AccessToken     string `json:"access_token"`
	ExpiresInSecond int64  `json:"expires_in_seconds"`
}

type loginResponse struct {
	Code      string    `json:"code"`
	Message   string    `json:"message"`
	Data      loginData `json:"data"`
	RequestID string    `json:"request_id"`
}

type currentUserData struct {
	ID    uint64   `json:"id"`
	Email string   `json:"email"`
	Roles []string `json:"roles"`
}

type currentUserResponse struct {
	Code      string          `json:"code"`
	Message   string          `json:"message"`
	Data      currentUserData `json:"data"`
	RequestID string          `json:"request_id"`
}

type authenticatedUserIDKey struct{}

// RegisterAuthRoutes registers public login and identity-protected user routes.
func RegisterAuthRoutes(
	server *rest.Server,
	service *user.AuthService,
	tokens *auth.TokenManager,
	rbacService *rbac.Service,
) {
	server.AddRoute(rest.Route{
		Method:  http.MethodPost,
		Path:    "/v1/auth/login",
		Handler: loginHandler(service, tokens),
	})
	server.AddRoute(rest.Route{
		Method: http.MethodGet,
		Path:   "/v1/users/me",
		Handler: authenticate(tokens)(
			currentUserHandler(service, rbacService),
		),
	})
}

func loginHandler(service *user.AuthService, tokens *auth.TokenManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request loginRequest
		if err := httpx.Parse(r, &request); err != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid request body")
			return
		}

		authenticated, err := service.Authenticate(r.Context(), request.Email, request.Password)
		if err != nil {
			if errors.Is(err, user.ErrInvalidCredentials) {
				writeError(r, w, http.StatusUnauthorized, "UNAUTHENTICATED", "invalid credentials")
				return
			}
			writeError(r, w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			return
		}

		tokenText, err := tokens.Issue(authenticated.ID)
		if err != nil {
			writeError(r, w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			return
		}
		httpx.OkJsonCtx(r.Context(), w, loginResponse{
			Code:    "OK",
			Message: "",
			Data: loginData{
				AccessToken:     tokenText,
				ExpiresInSecond: tokens.ExpiresInSeconds(),
			},
			RequestID: "",
		})
	}
}

func authenticate(tokens *auth.TokenManager) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			authorization := strings.TrimSpace(r.Header.Get("Authorization"))
			parts := strings.Fields(authorization)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				writeError(r, w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
				return
			}

			userID, err := tokens.ParseUserID(parts[1])
			if err != nil {
				if errors.Is(err, auth.ErrTokenExpired) {
					writeError(r, w, http.StatusUnauthorized, "TOKEN_EXPIRED", "access token expired")
					return
				}
				writeError(r, w, http.StatusUnauthorized, "UNAUTHENTICATED", "invalid access token")
				return
			}

			ctx := context.WithValue(r.Context(), authenticatedUserIDKey{}, userID)
			next(w, r.WithContext(ctx))
		}
	}
}

func currentUserHandler(service *user.AuthService, rbacService *rbac.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := r.Context().Value(authenticatedUserIDKey{}).(uint64)
		if !ok || userID == 0 {
			writeError(r, w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
			return
		}
		current, err := service.CurrentUser(r.Context(), userID)
		if err != nil {
			if errors.Is(err, user.ErrUserNotFound) {
				writeError(r, w, http.StatusUnauthorized, "UNAUTHENTICATED", "user is not active")
				return
			}
			writeError(r, w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			return
		}
		roles, err := rbacService.UserRoles(r.Context(), userID)
		if err != nil {
			writeError(r, w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			return
		}

		httpx.OkJsonCtx(r.Context(), w, currentUserResponse{
			Code:    "OK",
			Message: "",
			Data: currentUserData{
				ID:    current.ID,
				Email: current.Email,
				Roles: roles,
			},
			RequestID: "",
		})
	}
}
