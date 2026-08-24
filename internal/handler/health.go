// Package handler contains the HTTP transport layer for the monolith API.
package handler

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/rest/httpx"
)

type healthData struct {
	Status string `json:"status"`
}

type healthResponse struct {
	Code      string     `json:"code"`
	Message   string     `json:"message"`
	Data      healthData `json:"data"`
	RequestID string     `json:"request_id"`
}

// RegisterRoutes registers only routes that have a current implementation.
// Keeping registration explicit prevents later-stage endpoints from appearing
// before their behavior and tests exist.
func RegisterRoutes(server *rest.Server) {
	server.AddRoute(rest.Route{
		Method:  http.MethodGet,
		Path:    "/health",
		Handler: healthHandler,
	})
}

// healthHandler is deliberately a liveness check, not a readiness check. It
// reports that this process can serve HTTP without claiming that future
// dependencies such as MySQL are healthy.
func healthHandler(w http.ResponseWriter, r *http.Request) {
	httpx.OkJsonCtx(r.Context(), w, healthResponse{
		Code:    "OK",
		Message: "",
		Data: healthData{
			Status: "ok",
		},
		RequestID: "",
	})
}
