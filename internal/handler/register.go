package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/rest/httpx"

	"service_rpc/internal/user"
)

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type registeredUserData struct {
	ID        uint64 `json:"id"`
	Email     string `json:"email"`
	CreatedAt string `json:"created_at"`
}

type registerResponse struct {
	Code      string             `json:"code"`
	Message   string             `json:"message"`
	Data      registeredUserData `json:"data"`
	RequestID string             `json:"request_id"`
}

type errorResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Data      any    `json:"data"`
	RequestID string `json:"request_id"`
}

// RegisterUserRoutes registers user routes whose use cases exist now.
func RegisterUserRoutes(server *rest.Server, service *user.Service) {
	server.AddRoute(rest.Route{
		Method:  http.MethodPost,
		Path:    "/v1/auth/register",
		Handler: registerUserHandler(service),
	})
}

func registerUserHandler(service *user.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request registerRequest
		if err := httpx.Parse(r, &request); err != nil {
			writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid request body")
			return
		}

		created, err := service.Register(r.Context(), request.Email, request.Password)
		if err != nil {
			switch {
			case errors.Is(err, user.ErrInvalidEmail), errors.Is(err, user.ErrInvalidPassword):
				writeError(r, w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid email or password")
			case errors.Is(err, user.ErrUserAlreadyExists):
				writeError(r, w, http.StatusConflict, "USER_ALREADY_EXISTS", "user already exists")
			default:
				writeError(r, w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			}
			return
		}

		httpx.OkJsonCtx(r.Context(), w, registerResponse{
			Code:    "OK",
			Message: "",
			Data: registeredUserData{
				ID:        created.ID,
				Email:     created.Email,
				CreatedAt: created.CreatedAt.UTC().Format(time.RFC3339Nano),
			},
			RequestID: "",
		})
	}
}

func writeError(r *http.Request, w http.ResponseWriter, status int, code, message string) {
	httpx.WriteJsonCtx(r.Context(), w, status, errorResponse{
		Code:      code,
		Message:   message,
		Data:      nil,
		RequestID: "",
	})
}
