package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestValidateOptions(t *testing.T) {
	valid := options{
		BaseURL: "http://127.0.0.1:8888/", Strategy: " PESSIMISTIC ", Scenario: "unique",
		Concurrency: 10, Requests: 20, Stock: 20, RequestTimeout: time.Second, SetupConcurrency: 2,
		AdminEmail: "admin@example.com", AdminPassword: "password", UserPassword: "password",
		UserDomain: "example.com", RunID: "Run-01", Output: "report.json",
	}
	if err := validateOptions(&valid); err != nil {
		t.Fatalf("validateOptions() error = %v", err)
	}
	if valid.BaseURL != "http://127.0.0.1:8888" || valid.Strategy != "pessimistic" || valid.RunID != "run01" {
		t.Fatalf("options were not normalized: %+v", valid)
	}

	invalid := valid
	invalid.Strategy = "mutex"
	if err := validateOptions(&invalid); err == nil {
		t.Fatal("invalid strategy error = nil")
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

func TestExecuteRequestRecordsBusinessResultAndLatency(t *testing.T) {
	serverURL := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/seckill/items/7/orders" || req.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("unexpected request path=%s authorization=%q", req.URL.Path, req.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "OK", "data": map[string]any{"replayed": true}})
	}))
	r := &runner{options: options{BaseURL: serverURL}, client: http.DefaultClient}
	got := r.executeRequest(3, 7, "token")
	if got.Sequence != 3 || got.HTTPCode != http.StatusOK || got.Code != "OK" || !got.Replayed || got.Duration <= 0 || got.Error != "" {
		t.Fatalf("executeRequest() = %+v", got)
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
	got := buildReport(options{BaseURL: "http://example", Strategy: "atomic", Scenario: "unique", Concurrency: 2, Requests: 6, Stock: 2}, 7, samples, time.Second, 100*time.Millisecond)
	if got.Counts.Created != 1 || got.Counts.Replayed != 1 || got.Counts.OutOfStock != 1 ||
		got.Counts.NetworkError != 1 || got.Counts.ServerTimeout != 1 || got.Counts.ServerRejected != 1 {
		t.Fatalf("counts = %+v", got.Counts)
	}
	if got.Latency.P50MS != 20 || got.Latency.P90MS != 50 || got.Latency.P99MS != 50 || got.ThroughputQPS != 60 {
		t.Fatalf("latency=%+v throughput=%v", got.Latency, got.ThroughputQPS)
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
