package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zeromicro/go-zero/core/conf"

	"service_rpc/internal/config"
)

func TestValidateOptions(t *testing.T) {
	valid := options{
		BaseURL: "http://127.0.0.1:8888/", Strategy: " PESSIMISTIC ", Scenario: "unique",
		Admission: " REDIS ", OrderMode: " ASYNC ", ConfigFile: "etc/store-api.yaml", RedisDeployment: " remote-standalone ",
		Concurrency: 10, Requests: 20, Stock: 20, RequestTimeout: time.Second, DrainTimeout: time.Second, PollInterval: time.Millisecond, SetupConcurrency: 2,
		AdminEmail: "admin@example.com", AdminPassword: "password", UserPassword: "password",
		UserDomain: "example.com", RunID: "Run-01", Output: "report.json",
	}
	if err := validateOptions(&valid); err != nil {
		t.Fatalf("validateOptions() error = %v", err)
	}
	if valid.BaseURL != "http://127.0.0.1:8888" || valid.Strategy != "pessimistic" || valid.Admission != "redis" || valid.OrderMode != "async" || valid.RedisDeployment != "remote-standalone" || valid.RunID != "run01" {
		t.Fatalf("options were not normalized: %+v", valid)
	}

	invalid := valid
	invalid.Strategy = "mutex"
	if err := validateOptions(&invalid); err == nil {
		t.Fatal("invalid strategy error = nil")
	}
	invalidAdmission := valid
	invalidAdmission.Admission = "fallback"
	if err := validateOptions(&invalidAdmission); err == nil {
		t.Fatal("invalid admission error = nil")
	}
	invalidOrderMode := valid
	invalidOrderMode.OrderMode = "eventual-magic"
	if err := validateOptions(&invalidOrderMode); err == nil {
		t.Fatal("invalid order mode error = nil")
	}

	conflictingTarget := valid
	conflictingTarget.SKUID = 10
	conflictingTarget.ItemID = 20
	if err := validateOptions(&conflictingTarget); err == nil {
		t.Fatal("sku-id and item-id conflict error = nil")
	}
}

func TestNewLoadtestSKUCodeIsUniqueAndKeepsRunID(t *testing.T) {
	now := time.Unix(1_700_000_000, 123)
	first := newLoadtestSKUCode("atomic01", now)
	second := newLoadtestSKUCode("atomic01", now)
	if first == second {
		t.Fatalf("SKU codes must be unique: %q", first)
	}
	if len(first) > 100 || first[:18] != "LOADTEST-ATOMIC01-" {
		t.Fatalf("unexpected SKU code: %q", first)
	}
}

func TestCreateSeckillItemPreheatsRedisBeforeStart(t *testing.T) {
	var operations []string
	serverURL := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		operations = append(operations, req.Method+" "+req.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/v1/admin/seckill/activities":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "OK", "data": map[string]any{"id": 11}})
		case "/v1/admin/seckill/activities/11/items":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "OK", "data": map[string]any{"id": 12}})
		case "/v1/admin/seckill/activities/11/status", "/v1/admin/seckill/activities/11/preheat":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "OK"})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "NOT_FOUND"})
		}
	}))
	r := &runner{
		options: options{BaseURL: serverURL, Admission: "redis", SKUID: 21, Stock: 10, RunID: "redis01"},
		client:  http.DefaultClient,
	}
	itemID, err := r.createSeckillItem("admin-token")
	if err != nil {
		t.Fatalf("createSeckillItem() error = %v", err)
	}
	if itemID != 12 || !r.readyAt.After(time.Now()) {
		t.Fatalf("itemID=%d readyAt=%v", itemID, r.readyAt)
	}
	wantLast := "POST /v1/admin/seckill/activities/11/preheat"
	if len(operations) != 4 || operations[len(operations)-1] != wantLast {
		t.Fatalf("operations=%v, want final %q", operations, wantLast)
	}
}

func TestExecuteRequestRecordsBusinessResultAndLatency(t *testing.T) {
	serverURL := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/seckill/items/7/orders" || req.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("unexpected request path=%s authorization=%q", req.URL.Path, req.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "OK", "data": map[string]any{
			"replayed": true, "order": map[string]any{"id": 9, "order_no": "order-9"},
		}})
	}))
	r := &runner{options: options{BaseURL: serverURL}, client: http.DefaultClient}
	got := r.executeRequest(3, 7, "token")
	if got.Sequence != 3 || got.HTTPCode != http.StatusOK || got.Code != "OK" || !got.Replayed || got.OrderID != 9 || got.OrderNo != "order-9" || got.Duration <= 0 || got.Error != "" {
		t.Fatalf("executeRequest() = %+v", got)
	}
}

func TestExecuteRequestRecordsAsyncAccepted(t *testing.T) {
	serverURL := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "OK", "data": map[string]any{
			"replayed": false, "order_no": "async-9", "status": "QUEUED",
		}})
	}))
	r := &runner{options: options{BaseURL: serverURL}, client: http.DefaultClient}
	got := r.executeRequest(3, 7, "token")
	if got.HTTPCode != http.StatusAccepted || got.Code != "OK" || got.OrderNo != "async-9" || got.Status != "QUEUED" || got.Error != "" {
		t.Fatalf("executeRequest() = %+v", got)
	}
}

func TestDrainAsyncPollsEachReplayedOrderOncePerRound(t *testing.T) {
	polls := 0
	serverURL := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/seckill/orders/async-9/result" || req.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("unexpected request path=%s authorization=%q", req.URL.Path, req.Header.Get("Authorization"))
		}
		polls++
		status := "QUEUED"
		if polls == 2 {
			status = "SUCCEEDED"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "OK", "data": map[string]any{"status": status}})
	}))
	r := &runner{options: options{
		BaseURL: serverURL, Scenario: "replay", DrainTimeout: time.Second, PollInterval: time.Millisecond,
	}, client: http.DefaultClient}
	samples, _ := r.drainAsync([]sample{
		{Sequence: 0, HTTPCode: http.StatusAccepted, Code: "OK", OrderNo: "async-9"},
		{Sequence: 1, HTTPCode: http.StatusAccepted, Code: "OK", OrderNo: "async-9", Replayed: true},
	}, []string{"token"})
	if polls != 2 || samples[0].Status != "SUCCEEDED" || samples[1].Status != "SUCCEEDED" {
		t.Fatalf("polls=%d samples=%+v", polls, samples)
	}
}

func TestRunReplayUsesSingleTokenForAllConcurrentRequests(t *testing.T) {
	serverURL := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer only-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "OK", "data": map[string]any{"replayed": true}})
	}))

	r := &runner{
		options: options{BaseURL: serverURL, Scenario: "replay", Requests: 100, Concurrency: 100},
		client:  http.DefaultClient,
	}
	results, _ := r.run(7, []string{"only-token"})
	if len(results) != 100 {
		t.Fatalf("result count = %d, want 100", len(results))
	}
	for index, result := range results {
		if result.HTTPCode != http.StatusOK || result.Code != "OK" || !result.Replayed || result.Error != "" {
			t.Fatalf("result[%d] = %+v", index, result)
		}
	}
}

func TestExecuteRequestRecognizesServerTimeout(t *testing.T) {
	serverURL := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("Request Timeout"))
	}))

	r := &runner{options: options{BaseURL: serverURL}, client: http.DefaultClient}
	got := r.executeRequest(0, 7, "token")
	if got.HTTPCode != http.StatusServiceUnavailable || got.Code != codeServerTimeout ||
		got.ResponseBody != "Request Timeout" || got.Error != "" {
		t.Fatalf("executeRequest() = %+v", got)
	}
}

func TestExecuteRequestRecognizesFastServerRejection(t *testing.T) {
	serverURL := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))

	r := &runner{options: options{BaseURL: serverURL}, client: http.DefaultClient}
	got := r.executeRequest(0, 7, "token")
	if got.HTTPCode != http.StatusServiceUnavailable || got.Code != codeServerRejected || got.Error != "" {
		t.Fatalf("executeRequest() = %+v", got)
	}
}

func newTestHTTPServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	server := &http.Server{Handler: handler}
	t.Cleanup(func() {
		_ = server.Close()
		_ = listener.Close()
	})
	go func() {
		_ = server.Serve(listener)
	}()
	return "http://" + listener.Addr().String()
}

func TestBuildReportCountsAndPercentiles(t *testing.T) {
	samples := []sample{
		{HTTPCode: 200, Code: "OK", Duration: 10 * time.Millisecond, LatencyMS: 10},
		{HTTPCode: 200, Code: "OK", Replayed: true, Duration: 20 * time.Millisecond, LatencyMS: 20},
		{HTTPCode: 409, Code: "OUT_OF_STOCK", Duration: 30 * time.Millisecond, LatencyMS: 30},
		{Error: "timeout", Duration: 40 * time.Millisecond, LatencyMS: 40},
		{HTTPCode: 503, Code: codeServerTimeout, Duration: 50 * time.Millisecond, LatencyMS: 50},
		{HTTPCode: 503, Code: codeServerRejected, Duration: time.Millisecond, LatencyMS: 1},
	}
	got := buildReport(options{BaseURL: "http://example", Strategy: "atomic", Admission: "redis", Scenario: "unique", Concurrency: 2, Requests: 6, Stock: 2}, 7, samples, time.Second, 100*time.Millisecond)
	if got.Counts.Created != 1 || got.Counts.Replayed != 1 || got.Counts.OutOfStock != 1 ||
		got.Counts.NetworkError != 1 || got.Counts.ServerTimeout != 1 || got.Counts.ServerRejected != 1 {
		t.Fatalf("counts = %+v", got.Counts)
	}
	if got.Latency.P50MS != 20 || got.Latency.P90MS != 50 || got.Latency.P99MS != 50 || got.ThroughputQPS != 60 {
		t.Fatalf("latency=%+v throughput=%v", got.Latency, got.ThroughputQPS)
	}
	if got.Admission != "redis" {
		t.Fatalf("admission=%q, want redis", got.Admission)
	}
}

func TestBuildReportSeparatesAsyncAdmissionAndFinalState(t *testing.T) {
	samples := []sample{
		{HTTPCode: http.StatusAccepted, Code: "OK", OrderNo: "a", Status: "SUCCEEDED", Duration: time.Millisecond},
		{HTTPCode: http.StatusAccepted, Code: "OK", OrderNo: "b", Status: "FAILED", Duration: time.Millisecond},
		{HTTPCode: http.StatusAccepted, Code: "OK", OrderNo: "c", Status: "QUEUED", ResultError: "poll timeout", Duration: time.Millisecond},
	}
	got := buildReport(options{BaseURL: "http://example", OrderMode: "async", Requests: 3}, 7, samples, 0, time.Second)
	if got.Counts.HTTP202 != 3 || got.Counts.Queued != 3 || got.Counts.FinalSucceeded != 1 || got.Counts.FinalFailed != 1 || got.Counts.FinalPending != 1 || got.Counts.ResultPollError != 1 {
		t.Fatalf("counts = %+v", got.Counts)
	}
}

func TestCollectEnvironmentDoesNotExposeSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store-api.yaml")
	configBody := []byte("Name: test\nHost: 127.0.0.1\nPort: 8888\nMode: test\nMySQL:\n  DataSource: user:top-secret@tcp(db:3306)/test\n  MaxOpenConns: 12\n  MaxIdleConns: 6\n  ConnMaxLifetimeSeconds: 300\nRedis:\n  Address: redis.example:6379\n  Username: \"\"\n  Password: redis-secret\n  DB: 3\n  DialTimeoutMilliseconds: 500\n  OperationTimeoutMilliseconds: 200\nAuth:\n  AccessSecret: jwt-secret-with-required-minimum-length\n  AccessTTLSeconds: 60\nSeckill:\n  StockMode: atomic\n  AdmissionMode: redis\n")
	if err := os.WriteFile(path, configBody, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	var parsed config.Config
	if err := conf.Load(path, &parsed); err != nil {
		t.Fatalf("load test config: %v", err)
	}
	got := collectEnvironment(options{ConfigFile: path, Output: "report.json", RedisDeployment: "remote-standalone"})
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	text := string(encoded)
	for _, secret := range []string{"top-secret", "redis-secret", "jwt-secret-with-required-minimum-length"} {
		if strings.Contains(text, secret) {
			t.Fatalf("environment report leaks secret %q: %s", secret, text)
		}
	}
	if got.ServiceConfig.RedisAddress != "redis.example:6379" || got.ServiceConfig.MySQLMaxOpenConns != 12 {
		t.Fatalf("service config summary = %+v", got.ServiceConfig)
	}
}

func TestWriteReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "report.json")
	if err := writeReport(path, report{Strategy: "optimistic"}); err != nil {
		t.Fatalf("writeReport() error = %v", err)
	}
	data, err := readReportStrategy(path)
	if err != nil || data != "optimistic" {
		t.Fatalf("report strategy=%q error=%v", data, err)
	}
}

func readReportStrategy(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var value report
	if err := json.Unmarshal(data, &value); err != nil {
		return "", err
	}
	return value.Strategy, nil
}
