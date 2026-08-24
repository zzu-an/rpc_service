// Command loadtest benchmarks the v0.1 order creation API.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	baseURL       = flag.String("url", "http://127.0.0.1:8888", "service base URL")
	concurrency   = flag.Int("concurrency", 10, "number of concurrent workers")
	perWorker     = flag.Int("requests", 100, "requests per worker")
	requestLimit  = flag.Duration("timeout", 10*time.Second, "timeout for each HTTP request")
	adminEmail    = flag.String("admin-email", os.Getenv("SERVICE_RPC_LOADTEST_ADMIN_EMAIL"), "existing administrator email")
	adminPassword = flag.String(
		"admin-password",
		os.Getenv("SERVICE_RPC_LOADTEST_ADMIN_PASSWORD"),
		"existing administrator password; environment variable is preferred",
	)
	loadUserEmail = flag.String(
		"user-email",
		envOrDefault("SERVICE_RPC_LOADTEST_USER_EMAIL", "loadtest@example.com"),
		"email used to own benchmark orders",
	)
	loadUserPassword = flag.String(
		"user-password",
		envOrDefault("SERVICE_RPC_LOADTEST_USER_PASSWORD", "loadtest-password-2026"),
		"password for the benchmark order owner; environment variable is preferred",
	)

	httpClient *http.Client
)

func main() {
	flag.Parse()
	if err := validateFlags(); err != nil {
		fmt.Fprintf(os.Stderr, "invalid arguments: %v\n", err)
		os.Exit(2)
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = *concurrency + 10
	transport.MaxIdleConnsPerHost = *concurrency
	httpClient = &http.Client{Timeout: *requestLimit, Transport: transport}
	defer transport.CloseIdleConnections()

	fmt.Println("=== Setup ===")
	token, skuID, err := setup()
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Ready: sku_id=%d\n\n", skuID)

	fmt.Println("=== Benchmark ===")
	fmt.Printf("Concurrency: %d\n", *concurrency)
	fmt.Printf("Requests per worker: %d\n", *perWorker)
	totalRequests := *concurrency * *perWorker
	fmt.Printf("Total requests: %d\n\n", totalRequests)

	results, elapsed := runBenchmark(token, skuID)
	report(results, elapsed)
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func validateFlags() error {
	parsed, err := url.Parse(*baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("url must be an absolute HTTP URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("url scheme must be http or https")
	}
	*baseURL = strings.TrimRight(*baseURL, "/")
	if *concurrency <= 0 {
		return errors.New("concurrency must be positive")
	}
	if *perWorker <= 0 {
		return errors.New("requests must be positive")
	}
	if *requestLimit <= 0 {
		return errors.New("timeout must be positive")
	}
	if strings.TrimSpace(*adminEmail) == "" || *adminPassword == "" {
		return errors.New("administrator credentials are required; set SERVICE_RPC_LOADTEST_ADMIN_EMAIL and SERVICE_RPC_LOADTEST_ADMIN_PASSWORD")
	}
	if strings.TrimSpace(*loadUserEmail) == "" || *loadUserPassword == "" {
		return errors.New("benchmark user credentials must not be empty")
	}
	return nil
}

// setup uses an existing administrator only for catalog preparation. Orders
// are created with a separate ordinary user so the benchmark does not depend
// on a hard-coded user ID or grant administrative rights to its traffic user.
func setup() (token string, skuID uint64, err error) {
	adminToken, err := login(*adminEmail, *adminPassword)
	if err != nil {
		return "", 0, fmt.Errorf("admin login: %w", err)
	}

	if err := registerBenchmarkUser(); err != nil {
		return "", 0, err
	}
	loadToken, err := login(*loadUserEmail, *loadUserPassword)
	if err != nil {
		return "", 0, fmt.Errorf("benchmark user login: %w", err)
	}

	type skuPayload struct {
		Code      string `json:"code"`
		Name      string `json:"name"`
		PriceCent int64  `json:"price_cent"`
	}
	type createProductResponse struct {
		apiResponse
		Data struct {
			ID   uint64 `json:"id"`
			SKUs []struct {
				ID uint64 `json:"id"`
			} `json:"skus"`
		} `json:"data"`
	}

	skuCode := fmt.Sprintf("loadtest-sku-%d", time.Now().UnixNano())
	var created createProductResponse
	status, err := requestJSON(http.MethodPost, "/v1/admin/products", adminToken, map[string]any{
		"name":        "Load Test Product",
		"description": "Auto-created for order load testing",
		"skus": []skuPayload{
			{Code: skuCode, Name: "Default", PriceCent: 9900},
		},
	}, &created)
	if err != nil {
		return "", 0, fmt.Errorf("create product request: %w", err)
	}
	if status != http.StatusOK || created.Code != "OK" {
		return "", 0, apiFailure("create product", status, created.apiResponse)
	}
	if created.Data.ID == 0 || len(created.Data.SKUs) == 0 || created.Data.SKUs[0].ID == 0 {
		return "", 0, errors.New("create product returned no product or SKU ID")
	}

	var activated apiResponse
	status, err = requestJSON(
		http.MethodPut,
		fmt.Sprintf("/v1/admin/products/%d/status", created.Data.ID),
		adminToken,
		map[string]uint8{"status": 1},
		&activated,
	)
	if err != nil {
		return "", 0, fmt.Errorf("activate product request: %w", err)
	}
	if status != http.StatusOK || activated.Code != "OK" {
		return "", 0, apiFailure("activate product", status, activated)
	}

	return loadToken, created.Data.SKUs[0].ID, nil
}

type apiResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func registerBenchmarkUser() error {
	var response apiResponse
	status, err := requestJSON(http.MethodPost, "/v1/auth/register", "", map[string]string{
		"email": *loadUserEmail, "password": *loadUserPassword,
	}, &response)
	if err != nil {
		return fmt.Errorf("register benchmark user request: %w", err)
	}
	if status == http.StatusOK && response.Code == "OK" {
		return nil
	}
	// Reusing the same benchmark identity is safe: the following login still
	// proves that the supplied password belongs to that existing account.
	if status == http.StatusConflict && response.Code == "USER_ALREADY_EXISTS" {
		return nil
	}
	return apiFailure("register benchmark user", status, response)
}

func login(email, password string) (string, error) {
	var response struct {
		apiResponse
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	status, err := requestJSON(http.MethodPost, "/v1/auth/login", "", map[string]string{
		"email": email, "password": password,
	}, &response)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK || response.Code != "OK" {
		return "", apiFailure("login", status, response.apiResponse)
	}
	if response.Data.AccessToken == "" {
		return "", errors.New("login returned an empty access token")
	}
	return response.Data.AccessToken, nil
}

func apiFailure(operation string, status int, response apiResponse) error {
	return fmt.Errorf("%s failed: HTTP %d, code=%q, message=%q", operation, status, response.Code, response.Message)
}

type result struct {
	status   int
	duration time.Duration
}

func runBenchmark(token string, skuID uint64) ([]result, time.Duration) {
	totalRequests := *concurrency * *perWorker
	results := make([]result, totalRequests)
	var wg sync.WaitGroup
	var index atomic.Int64

	startedAt := time.Now()
	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < *perWorker; j++ {
				item := doOrder(token, skuID)
				position := index.Add(1) - 1
				results[position] = item
			}
		}()
	}
	wg.Wait()
	return results, time.Since(startedAt)
}

func doOrder(token string, skuID uint64) result {
	body, err := json.Marshal(map[string]any{
		"items": []map[string]any{{"sku_id": skuID, "quantity": 1}},
	})
	if err != nil {
		return result{}
	}
	req, err := http.NewRequest(http.MethodPost, *baseURL+"/v1/orders", bytes.NewReader(body))
	if err != nil {
		return result{}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	startedAt := time.Now()
	response, err := httpClient.Do(req)
	if err != nil {
		return result{duration: time.Since(startedAt)}
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	return result{status: response.StatusCode, duration: time.Since(startedAt)}
}

func report(results []result, elapsed time.Duration) {
	var success, failed int64
	errorsByStatus := make(map[int]int64)
	latencies := make([]time.Duration, 0, len(results))

	for _, item := range results {
		if item.status == http.StatusOK {
			success++
		} else {
			failed++
			errorsByStatus[item.status]++
		}
		latencies = append(latencies, item.duration)
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	fmt.Printf("Duration: %v\n", elapsed)
	fmt.Printf("Success: %d\n", success)
	fmt.Printf("Failed:  %d\n", failed)
	if elapsed > 0 {
		fmt.Printf("Throughput: %.1f req/s\n", float64(len(results))/elapsed.Seconds())
	}

	if len(latencies) > 0 {
		fmt.Println("\nLatency:")
		fmt.Printf("  P50: %v\n", percentile(latencies, 50))
		fmt.Printf("  P95: %v\n", percentile(latencies, 95))
		fmt.Printf("  P99: %v\n", percentile(latencies, 99))
		fmt.Printf("  Max: %v\n", latencies[len(latencies)-1])
	}

	if len(errorsByStatus) > 0 {
		statuses := make([]int, 0, len(errorsByStatus))
		for status := range errorsByStatus {
			statuses = append(statuses, status)
		}
		sort.Ints(statuses)
		fmt.Println("\nErrors:")
		for _, status := range statuses {
			label := fmt.Sprintf("HTTP %d", status)
			if status == 0 {
				label = "network/client error"
			}
			fmt.Printf("  %s: %d\n", label, errorsByStatus[status])
		}
	}
}

func percentile(sorted []time.Duration, percentage int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	// Nearest-rank percentiles keep P99 on the slowest observation for small
	// samples instead of understating latency by rounding the index down.
	index := (len(sorted)*percentage+99)/100 - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

// requestJSON always closes the response body and preserves transport and
// decode errors. Setup must fail loudly; benchmarking an unprepared API would
// otherwise produce a plausible-looking but meaningless report.
func requestJSON(method, path, token string, body, destination any) (int, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return 0, fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequest(method, *baseURL+path, bytes.NewReader(encoded))
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("send request: %w", err)
	}
	defer response.Body.Close()
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(destination); err != nil {
		return response.StatusCode, fmt.Errorf("decode HTTP %d response: %w", response.StatusCode, err)
	}
	return response.StatusCode, nil
}
