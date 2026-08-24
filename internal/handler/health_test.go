package handler

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"
)

func TestHealthHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	recorder := httptest.NewRecorder()

	healthHandler(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", recorder.Code, http.StatusOK)
	}

	var response healthResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != "OK" {
		t.Errorf("code = %q, want OK", response.Code)
	}
	if response.Data.Status != "ok" {
		t.Errorf("status = %q, want ok", response.Data.Status)
	}
	if response.RequestID != "" {
		t.Errorf("request_id = %q, want empty before request ID middleware exists", response.RequestID)
	}
}

func TestStoreAPIConfigBuildsServer(t *testing.T) {
	var config rest.RestConf
	if err := conf.Load("../../etc/store-api.yaml", &config); err != nil {
		t.Fatalf("load API config: %v", err)
	}
	if config.Name != "store-api" || config.Host != "127.0.0.1" || config.Port != 8888 {
		t.Fatalf("unexpected API config: name=%q host=%q port=%d", config.Name, config.Host, config.Port)
	}

	// NewServer validates framework-level REST configuration without binding a
	// real port, keeping this unit test deterministic and sandbox-friendly.
	server, err := rest.NewServer(config)
	if err != nil {
		t.Fatalf("construct go-zero REST server: %v", err)
	}
	server.Stop()
}

func TestRegisteredHealthRoute(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve ephemeral port: %v", err)
	}
	_, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		listener.Close()
		t.Fatalf("split listener address: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		listener.Close()
		t.Fatalf("parse listener port: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("release ephemeral port: %v", err)
	}

	var config rest.RestConf
	if err := conf.Load("../../etc/store-api.yaml", &config); err != nil {
		t.Fatalf("load API config: %v", err)
	}
	config.Host = "127.0.0.1"
	config.Port = port

	server, err := rest.NewServer(config)
	if err != nil {
		t.Fatalf("construct go-zero REST server: %v", err)
	}
	RegisterRoutes(server)

	go server.Start()
	t.Cleanup(func() {
		server.Stop()
	})

	url := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
	var response *http.Response
	for attempt := 0; attempt < 50; attempt++ {
		response, err = http.Get(url) // #nosec G107 -- the URL is a test-only loopback address.
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GET registered health route: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status code = %d, want %d", response.StatusCode, http.StatusOK)
	}

	var body healthResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode registered route response: %v", err)
	}
	if body.Code != "OK" || body.Data.Status != "ok" {
		t.Fatalf("unexpected registered route response: code=%q status=%q", body.Code, body.Data.Status)
	}
}
