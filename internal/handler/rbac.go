package handler

import (
	"errors"
	"net/http"

	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/rest/httpx"

	"service_rpc/internal/auth"
	"service_rpc/internal/rbac"
)

type replaceUserRolesRequest struct {
	UserID uint64   `path:"userId"`
	Roles  []string `json:"roles"`
}

type userRolesData struct {
	UserID uint64   `json:"user_id"`
	Roles  []string `json:"roles"`
}

type userRolesResponse struct {
	Code      string        `json:"code"`
	Message   string        `json:"message"`
	Data      userRolesData `json:"data"`
	RequestID string        `json:"request_id"`
}

// RegisterRBACRoutes registers role administration behind both identity and
// permission checks. The required permission code is attached at the route
// boundary rather than scattered as an admin-role comparison.
func RegisterRBACRoutes(server *rest.Server, tokens *auth.TokenManager, service *rbac.Service) {
	server.AddRoute(rest.Route{
		Method: http.MethodPut,
		Path:   "/v1/admin/users/:userId/roles",
		Handler: authenticate(tokens)(
			requirePermission(service, "rbac:manage")(
				replaceUserRolesHandler(service),
			),
		),
	})
}

func requirePermission(service *rbac.Service, permissionCode string) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			userID, ok := r.Context().Value(authenticatedUserIDKey{}).(uint64)
			if !ok || userID == 0 {
				writeError(r, w, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
				return
			}
			allowed, err := service.HasPermission(r.Context(), userID, permissionCode)
			if err != nil {
				writeError(r, w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
				return
			}
			if !allowed {
				writeError(r, w, http.StatusForbidden, "PERMISSION_DENIED", "permission denied")
				return
			}
			next(w, r)
		}
	}
}

func replaceUserRolesHandler(service *rbac.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request replaceUserRolesRequest
		if err := httpx.Parse(r, &request); err != nil || request.UserID == 0 {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid role assignment")
			return
		}
		if err := service.ReplaceUserRoles(r.Context(), request.UserID, request.Roles); err != nil {
			switch {
			case errors.Is(err, rbac.ErrUserNotFound):
				writeError(r, w, http.StatusNotFound, "USER_NOT_FOUND", "user not found")
			case errors.Is(err, rbac.ErrRoleAssignmentConflict):
				writeError(r, w, http.StatusConflict, "ROLE_ASSIGNMENT_CONFLICT", "invalid role assignment")
			default:
				writeError(r, w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			}
			return
		}
		roles, err := service.UserRoles(r.Context(), request.UserID)
		if err != nil {
			writeError(r, w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			return
		}
		httpx.OkJsonCtx(r.Context(), w, userRolesResponse{
			Code:    "OK",
			Message: "",
			Data: userRolesData{
				UserID: request.UserID,
				Roles:  roles,
			},
			RequestID: "",
		})
	}
}
