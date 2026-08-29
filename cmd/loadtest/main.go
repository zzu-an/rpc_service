// Command loadtest 对 v0.2 同步秒杀 HTTP 接口执行可重复并发压测。
//
// 工具把数据准备时间与正式压测时间分开统计。自动准备模式会创建商品、活动和测试用户，
// 因此只能对隔离测试库使用，不能直接指向生产环境。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zeromicro/go-zero/core/conf"

	"service_rpc/internal/config"
	platformcache "service_rpc/internal/platform/cache"
	"service_rpc/internal/platform/database"
	seckillmysql "service_rpc/internal/seckill/mysqlrepo"
	"service_rpc/internal/seckill/redisgate"
)

var loadtestResourceSequence atomic.Uint64

type options struct {
	BaseURL          string
	Strategy         string
	Admission        string
	OrderMode        string
	ConfigFile       string
	RedisDeployment  string
	Scenario         string
	Concurrency      int
	Requests         int
	Stock            int64
	RequestTimeout   time.Duration
	DrainTimeout     time.Duration
	PollInterval     time.Duration
	SetupConcurrency int
	AdminEmail       string
	AdminPassword    string
	UserPassword     string
	UserDomain       string
	RunID            string
	TokensFile       string
	SKUID            uint64
	ItemID           uint64
	Output           string
}

type runner struct {
	options options
	client  *http.Client
	readyAt time.Time
}

type apiResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type sample struct {
	Sequence int    `json:"sequence"`
	HTTPCode int    `json:"http_status"`
	Code     string `json:"business_code,omitempty"`
	// ResponseBody 保留非 JSON 响应的摘要。压测排障时只记录“解析失败”是不够的，
	// 服务端网关、超时中间件返回的纯文本往往才是判断故障来源的关键证据。
	ResponseBody string `json:"response_body,omitempty"`
	Replayed     bool   `json:"replayed"`
	OrderID      uint64 `json:"order_id,omitempty"`
	OrderNo      string `json:"order_no,omitempty"`
	// Status 是异步订单的最终观测状态。入口返回 202 时先记为 QUEUED，排空阶段再更新为
	// SUCCEEDED/FAILED；到达 drain-timeout 仍为 QUEUED 不等于失败，只表示观测窗口不足。
	Status      string        `json:"status,omitempty"`
	ResultError string        `json:"result_error,omitempty"`
	LatencyMS   float64       `json:"latency_ms"`
	Error       string        `json:"error,omitempty"`
	Duration    time.Duration `json:"-"`
}

type latencyReport struct {
	MinMS float64 `json:"min_ms"`
	AvgMS float64 `json:"avg_ms"`
	P50MS float64 `json:"p50_ms"`
	P90MS float64 `json:"p90_ms"`
	P95MS float64 `json:"p95_ms"`
	P99MS float64 `json:"p99_ms"`
	MaxMS float64 `json:"max_ms"`
}

type countReport struct {
	HTTP200              int            `json:"http_200"`
	HTTP202              int            `json:"http_202"`
	Created              int            `json:"created"`
	Replayed             int            `json:"replayed"`
	Queued               int            `json:"queued"`
	FinalSucceeded       int            `json:"final_succeeded"`
	FinalFailed          int            `json:"final_failed"`
	FinalPending         int            `json:"final_pending"`
	ResultPollError      int            `json:"result_poll_error"`
	OutOfStock           int            `json:"out_of_stock"`
	Unavailable          int            `json:"unavailable"`
	InventoryBusy        int            `json:"inventory_busy"`
	CacheNotReady        int            `json:"cache_not_ready"`
	TemporaryUnavailable int            `json:"temporary_unavailable"`
	ServerTimeout        int            `json:"server_timeout"`
	ServerRejected       int            `json:"server_rejected"`
	NetworkError         int            `json:"network_error"`
	OtherError           int            `json:"other_error"`
	ByHTTPStatus         map[string]int `json:"by_http_status"`
	ByCode               map[string]int `json:"by_business_code"`
}

type backendReport struct {
	MySQL                  *seckillmysql.ItemConsistencyState `json:"mysql,omitempty"`
	Redis                  *redisgate.ItemConsistencyState    `json:"redis,omitempty"`
	MySQLPurchaseCalls     *int64                             `json:"mysql_purchase_calls,omitempty"`
	PurchaseCallsAvailable bool                               `json:"mysql_purchase_calls_available"`
	ErrorCategories        []string                           `json:"error_categories,omitempty"`
}

type environmentReport struct {
	Hardware        string               `json:"hardware"`
	GoVersion       string               `json:"go_version"`
	RedisDeployment string               `json:"redis_deployment"`
	ReportFile      string               `json:"report_file"`
	ServiceConfig   serviceConfigSummary `json:"service_config"`
}

// serviceConfigSummary 只保留比较压测所需的非敏感配置。DSN、Redis 密码、JWT
// secret 即使对排障有帮助也不能进入可长期保存或提交到仓库的原始报告。
type serviceConfigSummary struct {
	StockMode                  string `json:"stock_mode,omitempty"`
	AdmissionMode              string `json:"admission_mode,omitempty"`
	MySQLMaxOpenConns          int    `json:"mysql_max_open_conns,omitempty"`
	MySQLMaxIdleConns          int    `json:"mysql_max_idle_conns,omitempty"`
	RedisAddress               string `json:"redis_address,omitempty"`
	RedisDB                    int    `json:"redis_db"`
	RedisOperationTimeoutMilli int    `json:"redis_operation_timeout_ms,omitempty"`
}

const (
	codeServerTimeout  = "SERVER_TIMEOUT"
	codeServerRejected = "SERVER_REJECTED"
)

type report struct {
	GeneratedAt         string            `json:"generated_at"`
	BaseURL             string            `json:"base_url"`
	Strategy            string            `json:"strategy_label"`
	Admission           string            `json:"admission_label"`
	OrderMode           string            `json:"order_mode"`
	Scenario            string            `json:"scenario"`
	ItemID              uint64            `json:"item_id"`
	SKUID               uint64            `json:"sku_id,omitempty"`
	Concurrency         int               `json:"concurrency"`
	Requests            int               `json:"requests"`
	ConfiguredStock     int64             `json:"configured_stock"`
	SetupDurationMS     float64           `json:"setup_duration_ms"`
	BenchmarkDurationMS float64           `json:"benchmark_duration_ms"`
	DrainDurationMS     float64           `json:"drain_duration_ms"`
	ThroughputQPS       float64           `json:"throughput_qps"`
	Counts              countReport       `json:"counts"`
	Latency             latencyReport     `json:"latency"`
	Backend             backendReport     `json:"backend"`
	Environment         environmentReport `json:"environment"`
	Samples             []sample          `json:"samples"`
}

func main() {
	opts := parseFlags()
	if opts.Stock < 0 {
		opts.Stock = int64(opts.Requests)
	}
	if opts.RunID == "" {
		opts.RunID = time.Now().UTC().Format("20060102T150405.000000000")
	}
	if err := validateOptions(&opts); err != nil {
		fmt.Fprintf(os.Stderr, "参数错误: %v\n", err)
		os.Exit(2)
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = opts.Concurrency + opts.SetupConcurrency + 10
	transport.MaxIdleConnsPerHost = opts.Concurrency + opts.SetupConcurrency
	transport.MaxConnsPerHost = opts.Concurrency + opts.SetupConcurrency
	client := &http.Client{Timeout: opts.RequestTimeout, Transport: transport}
	defer transport.CloseIdleConnections()
	r := &runner{options: opts, client: client}

	setupStarted := time.Now()
	itemID, tokens, err := r.prepare()
	if err != nil {
		fmt.Fprintf(os.Stderr, "准备压测数据失败: %v\n", err)
		os.Exit(1)
	}
	setupDuration := time.Since(setupStarted)

	fmt.Printf("开始压测: strategy=%s admission=%s order_mode=%s scenario=%s sku_id=%d item_id=%d requests=%d concurrency=%d stock=%d\n",
		opts.Strategy, opts.Admission, opts.OrderMode, opts.Scenario, opts.SKUID, itemID, opts.Requests, opts.Concurrency, opts.Stock)
	samples, elapsed := r.run(itemID, tokens)
	var drainDuration time.Duration
	if opts.OrderMode == "async" {
		samples, drainDuration = r.drainAsync(samples, tokens)
	}
	result := buildReport(opts, itemID, samples, setupDuration, elapsed)
	result.DrainDurationMS = durationMS(drainDuration)
	result.Backend = collectBackendState(opts.ConfigFile, itemID)
	printReport(result)
	if err := writeReport(opts.Output, result); err != nil {
		fmt.Fprintf(os.Stderr, "写入报告失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("JSON 报告: %s\n", opts.Output)
}

func parseFlags() options {
	var opts options
	flag.StringVar(&opts.BaseURL, "url", "http://127.0.0.1:8888", "服务端基础 URL")
	flag.StringVar(&opts.Strategy, "strategy", "atomic", "报告策略标签: atomic/pessimistic/optimistic")
	flag.StringVar(&opts.Admission, "admission", "redis", "报告准入标签: mysql/redis")
	flag.StringVar(&opts.OrderMode, "order-mode", "sync", "下单模式: sync/async")
	flag.StringVar(&opts.ConfigFile, "config", "etc/store-api.yaml", "用于采集压测后端状态的服务配置")
	flag.StringVar(&opts.RedisDeployment, "redis-deployment", "unspecified", "Redis 部署说明，例如 remote-standalone；不含凭据")
	flag.StringVar(&opts.Scenario, "scenario", "unique", "unique=每请求独立用户，replay=所有请求重复同一用户")
	flag.IntVar(&opts.Concurrency, "concurrency", 100, "并发 worker 数")
	flag.IntVar(&opts.Requests, "requests", 1000, "请求总数")
	flag.Int64Var(&opts.Stock, "stock", -1, "自动创建的库存；默认等于请求总数")
	flag.DurationVar(&opts.RequestTimeout, "timeout", 10*time.Second, "单个 HTTP 请求超时")
	flag.DurationVar(&opts.DrainTimeout, "drain-timeout", 30*time.Second, "异步入口压测结束后的结果排空等待上限")
	flag.DurationVar(&opts.PollInterval, "poll-interval", 200*time.Millisecond, "异步订单结果轮询间隔")
	flag.IntVar(&opts.SetupConcurrency, "setup-concurrency", 20, "注册和登录测试用户的并发数")
	flag.StringVar(&opts.AdminEmail, "admin-email", os.Getenv("SERVICE_RPC_LOADTEST_ADMIN_EMAIL"), "现有管理员邮箱")
	flag.StringVar(&opts.AdminPassword, "admin-password", os.Getenv("SERVICE_RPC_LOADTEST_ADMIN_PASSWORD"), "管理员密码，推荐使用环境变量")
	flag.StringVar(&opts.UserPassword, "user-password", envOrDefault("SERVICE_RPC_LOADTEST_USER_PASSWORD", "LoadTest123456!"), "自动创建测试用户的密码")
	flag.StringVar(&opts.UserDomain, "user-domain", "example.com", "自动创建测试用户的邮箱域名")
	flag.StringVar(&opts.RunID, "run-id", "", "报告和测试数据标识；默认使用 UTC 时间")
	flag.StringVar(&opts.TokensFile, "tokens-file", "", "已有 JWT 文件，每行一个；设置后跳过自动准备")
	flag.Uint64Var(&opts.SKUID, "sku-id", 0, "已有商品 SKU ID；自动创建秒杀活动并按 stock 设置库存")
	flag.Uint64Var(&opts.ItemID, "item-id", 0, "已有秒杀 item ID，与 tokens-file 一起使用")
	flag.StringVar(&opts.Output, "output", "loadtest-report.json", "JSON 报告输出路径")
	flag.Parse()
	return opts
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func validateOptions(opts *options) error {
	parsed, err := url.Parse(opts.BaseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("url 必须是绝对 HTTP/HTTPS URL")
	}
	opts.BaseURL = strings.TrimRight(opts.BaseURL, "/")
	opts.Strategy = strings.ToLower(strings.TrimSpace(opts.Strategy))
	if opts.Strategy != "atomic" && opts.Strategy != "pessimistic" && opts.Strategy != "optimistic" {
		return errors.New("strategy 必须是 atomic、pessimistic 或 optimistic")
	}
	opts.Admission = strings.ToLower(strings.TrimSpace(opts.Admission))
	if opts.Admission == "" {
		opts.Admission = "mysql"
	}
	if opts.Admission != "mysql" && opts.Admission != "redis" {
		return errors.New("admission 必须是 mysql 或 redis")
	}
	opts.OrderMode = strings.ToLower(strings.TrimSpace(opts.OrderMode))
	if opts.OrderMode == "" {
		opts.OrderMode = "sync"
	}
	if opts.OrderMode != "sync" && opts.OrderMode != "async" {
		return errors.New("order-mode 必须是 sync 或 async")
	}
	if strings.TrimSpace(opts.ConfigFile) == "" {
		return errors.New("config 不能为空")
	}
	opts.RedisDeployment = strings.TrimSpace(opts.RedisDeployment)
	if opts.RedisDeployment == "" {
		return errors.New("redis-deployment 不能为空")
	}
	opts.Scenario = strings.ToLower(strings.TrimSpace(opts.Scenario))
	if opts.Scenario != "unique" && opts.Scenario != "replay" {
		return errors.New("scenario 必须是 unique 或 replay")
	}
	if opts.Concurrency <= 0 || opts.Requests <= 0 || opts.SetupConcurrency <= 0 || opts.RequestTimeout <= 0 || opts.DrainTimeout <= 0 || opts.PollInterval <= 0 {
		return errors.New("concurrency、requests、setup-concurrency、timeout、drain-timeout 和 poll-interval 必须为正数")
	}
	if opts.Stock < 0 {
		return errors.New("stock 不能为负数")
	}
	if opts.Output == "" {
		return errors.New("output 不能为空")
	}
	if opts.SKUID > 0 && opts.ItemID > 0 {
		return errors.New("sku-id 与 item-id 不能同时使用：前者创建新秒杀项，后者直接压测已有秒杀项")
	}
	if opts.TokensFile != "" {
		if opts.SKUID > 0 {
			return errors.New("使用 sku-id 创建新秒杀项时不能同时使用 tokens-file；当前 tokens-file 模式只压测已有 item-id")
		}
		if opts.ItemID == 0 {
			return errors.New("使用 tokens-file 时必须提供 item-id")
		}
		return nil
	}
	if opts.Stock == 0 {
		return errors.New("自动准备模式的 stock 必须大于 0")
	}
	if strings.TrimSpace(opts.AdminEmail) == "" || opts.AdminPassword == "" {
		return errors.New("自动准备模式需要管理员账号；设置 SERVICE_RPC_LOADTEST_ADMIN_EMAIL 和 SERVICE_RPC_LOADTEST_ADMIN_PASSWORD")
	}
	if opts.UserPassword == "" || strings.TrimSpace(opts.UserDomain) == "" {
		return errors.New("测试用户密码和邮箱域名不能为空")
	}
	opts.RunID = sanitizeRunID(opts.RunID)
	if opts.RunID == "" {
		return errors.New("run-id 必须至少包含一个字母或数字")
	}
	if len(opts.RunID) > 40 {
		return errors.New("run-id 清理后不能超过 40 个字符")
	}
	return nil
}

func sanitizeRunID(value string) string {
	var builder strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			builder.WriteRune(r)
		}
	}
	return strings.ToLower(builder.String())
}

func (r *runner) prepare() (uint64, []string, error) {
	if r.options.TokensFile != "" {
		tokens, err := readTokens(r.options.TokensFile)
		if err != nil {
			return 0, nil, err
		}
		required := r.options.Requests
		if r.options.Scenario == "replay" {
			required = 1
		}
		if len(tokens) < required {
			return 0, nil, fmt.Errorf("tokens-file 只有 %d 个 token，需要至少 %d 个", len(tokens), required)
		}
		return r.options.ItemID, tokens, nil
	}

	adminToken, err := r.login(r.options.AdminEmail, r.options.AdminPassword)
	if err != nil {
		return 0, nil, fmt.Errorf("管理员登录: %w", err)
	}
	itemID, err := r.createSeckillItem(adminToken)
	if err != nil {
		return 0, nil, err
	}
	userCount := r.options.Requests
	if r.options.Scenario == "replay" {
		userCount = 1
	}
	tokens, err := r.prepareUsers(userCount)
	if err != nil {
		return 0, nil, err
	}
	if wait := time.Until(r.readyAt); wait > 0 {
		// 等待发生在 setup 阶段，不计入正式 QPS。replay 只创建一个用户，准备可能早于
		// 活动 start_at 完成；若直接开压会把“未开始”错误误当成 Redis 性能结果。
		timer := time.NewTimer(wait)
		defer timer.Stop()
		<-timer.C
	}
	return itemID, tokens, nil
}

func readTokens(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 tokens-file: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	tokens := make([]string, 0, len(lines))
	for _, line := range lines {
		if token := strings.TrimSpace(line); token != "" {
			tokens = append(tokens, token)
		}
	}
	if len(tokens) == 0 {
		return nil, errors.New("tokens-file 没有有效 token")
	}
	return tokens, nil
}

func (r *runner) createSeckillItem(adminToken string) (uint64, error) {
	skuID := r.options.SKUID
	if skuID == 0 {
		var err error
		skuID, err = r.createLoadtestSKU(adminToken)
		if err != nil {
			return 0, err
		}
	}

	type activityResponse struct {
		apiResponse
		Data struct {
			ID uint64 `json:"id"`
		} `json:"data"`
	}
	var activity activityResponse
	now := time.Now().UTC()
	startAt := now.Add(-time.Minute)
	if r.options.Admission == "redis" {
		startAt = now.Add(3 * time.Second)
	}
	r.readyAt = startAt
	status, err := r.requestJSON(http.MethodPost, "/v1/admin/seckill/activities", adminToken, map[string]any{
		"name": "Load Test Activity " + r.options.RunID, "start_at": startAt.Format(time.RFC3339Nano), "end_at": now.Add(time.Hour).Format(time.RFC3339Nano),
	}, &activity)
	if err != nil || status != http.StatusOK || activity.Code != "OK" || activity.Data.ID == 0 {
		return 0, combineAPIErr("创建压测活动", status, activity.apiResponse, err)
	}
	type itemResponse struct {
		apiResponse
		Data struct {
			ID uint64 `json:"id"`
		} `json:"data"`
	}
	var item itemResponse
	status, err = r.requestJSON(http.MethodPost, fmt.Sprintf("/v1/admin/seckill/activities/%d/items", activity.Data.ID), adminToken, map[string]any{
		"sku_id": skuID, "stock": r.options.Stock,
	}, &item)
	if err != nil || status != http.StatusOK || item.Code != "OK" || item.Data.ID == 0 {
		return 0, combineAPIErr("为 SKU 配置压测库存", status, item.apiResponse, err)
	}
	var basic apiResponse
	status, err = r.requestJSON(http.MethodPut, fmt.Sprintf("/v1/admin/seckill/activities/%d/status", activity.Data.ID), adminToken, map[string]uint8{"status": 1}, &basic)
	if err != nil || status != http.StatusOK || basic.Code != "OK" {
		return 0, combineAPIErr("启用压测活动", status, basic, err)
	}
	if r.options.Admission == "redis" {
		var preheat apiResponse
		status, err = r.requestJSON(http.MethodPost, fmt.Sprintf("/v1/admin/seckill/activities/%d/preheat", activity.Data.ID), adminToken, nil, &preheat)
		if err != nil || status != http.StatusOK || preheat.Code != "OK" {
			return 0, combineAPIErr("预热压测活动", status, preheat, err)
		}
	}
	return item.Data.ID, nil
}

func (r *runner) createLoadtestSKU(adminToken string) (uint64, error) {
	type createProductResponse struct {
		apiResponse
		Data struct {
			ID   uint64 `json:"id"`
			SKUs []struct {
				ID uint64 `json:"id"`
			} `json:"skus"`
		} `json:"data"`
	}
	var product createProductResponse
	// run-id 是便于人阅读和聚合报告的逻辑标识，不能直接充当数据库唯一键。
	// 同一组参数经常需要重复压测；若 SKU 只包含 run-id，第二轮必然触发唯一索引冲突。
	// 面试/优化点：幂等键、业务分组键、资源唯一键语义不同，不应为了省字段混用。
	skuCode := newLoadtestSKUCode(r.options.RunID, time.Now())
	status, err := r.requestJSON(http.MethodPost, "/v1/admin/products", adminToken, map[string]any{
		"name":        "Load Test Product " + r.options.RunID,
		"description": "由 cmd/loadtest 自动创建，仅用于隔离性能测试",
		"skus": []map[string]any{{
			"code": skuCode, "name": "Default", "price_cent": 9900,
		}},
	}, &product)
	if err != nil || status != http.StatusOK || product.Code != "OK" || product.Data.ID == 0 || len(product.Data.SKUs) == 0 {
		return 0, combineAPIErr("创建压测商品", status, product.apiResponse, err)
	}
	var basic apiResponse
	status, err = r.requestJSON(http.MethodPut, fmt.Sprintf("/v1/admin/products/%d/status", product.Data.ID), adminToken, map[string]uint8{"status": 1}, &basic)
	if err != nil || status != http.StatusOK || basic.Code != "OK" {
		return 0, combineAPIErr("启用压测商品", status, basic, err)
	}
	return product.Data.SKUs[0].ID, nil
}

func newLoadtestSKUCode(runID string, now time.Time) string {
	// 时间戳保证跨进程运行基本唯一，进程内序号覆盖同一纳秒内连续创建的极端情况。
	// 结果最长 74 字符，小于 product_skus.code 的 VARCHAR(100) 限制。
	return fmt.Sprintf("LOADTEST-%s-%X-%X", strings.ToUpper(runID), now.UnixNano(), loadtestResourceSequence.Add(1))
}

func combineAPIErr(operation string, status int, response apiResponse, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return fmt.Errorf("%s失败: HTTP %d code=%q message=%q", operation, status, response.Code, response.Message)
}

func (r *runner) prepareUsers(count int) ([]string, error) {
	tokens := make([]string, count)
	jobs := make(chan int)
	errCh := make(chan error, 1)
	workers := r.options.SetupConcurrency
	if workers > count {
		workers = count
	}
	var group sync.WaitGroup
	group.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer group.Done()
			for index := range jobs {
				email := fmt.Sprintf("loadtest-%s-%06d@%s", r.options.RunID, index, r.options.UserDomain)
				if err := r.register(email, r.options.UserPassword); err != nil {
					select {
					case errCh <- err:
					default:
					}
					continue
				}
				token, err := r.login(email, r.options.UserPassword)
				if err != nil {
					select {
					case errCh <- fmt.Errorf("登录测试用户 %s: %w", email, err):
					default:
					}
					continue
				}
				tokens[index] = token
			}
		}()
	}
	for index := 0; index < count; index++ {
		jobs <- index
	}
	close(jobs)
	group.Wait()
	close(errCh)
	if err := <-errCh; err != nil {
		return nil, err
	}
	for index, token := range tokens {
		if token == "" {
			return nil, fmt.Errorf("测试用户 %d 没有 token", index)
		}
	}
	return tokens, nil
}

func (r *runner) register(email, password string) error {
	var response apiResponse
	status, err := r.requestJSON(http.MethodPost, "/v1/auth/register", "", map[string]string{"email": email, "password": password}, &response)
	if err != nil {
		return fmt.Errorf("注册测试用户 %s: %w", email, err)
	}
	if status == http.StatusOK && response.Code == "OK" {
		return nil
	}
	if status == http.StatusConflict && response.Code == "USER_ALREADY_EXISTS" {
		return nil
	}
	return combineAPIErr("注册测试用户 "+email, status, response, nil)
}

func (r *runner) login(email, password string) (string, error) {
	var response struct {
		apiResponse
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	status, err := r.requestJSON(http.MethodPost, "/v1/auth/login", "", map[string]string{"email": email, "password": password}, &response)
	if err != nil || status != http.StatusOK || response.Code != "OK" || response.Data.AccessToken == "" {
		return "", combineAPIErr("登录 "+email, status, response.apiResponse, err)
	}
	return response.Data.AccessToken, nil
}

func (r *runner) requestJSON(method, path, token string, body, destination any) (int, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return 0, fmt.Errorf("编码请求: %w", err)
	}
	req, err := http.NewRequest(method, r.options.BaseURL+path, bytes.NewReader(encoded))
	if err != nil {
		return 0, fmt.Errorf("创建请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := r.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("发送请求: %w", err)
	}
	defer response.Body.Close()
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(destination); err != nil {
		return response.StatusCode, fmt.Errorf("解析 HTTP %d 响应: %w", response.StatusCode, err)
	}
	return response.StatusCode, nil
}

func (r *runner) run(itemID uint64, tokens []string) ([]sample, time.Duration) {
	results := make([]sample, r.options.Requests)
	jobs := make(chan int)
	start := make(chan struct{})
	var group sync.WaitGroup
	workers := r.options.Concurrency
	if workers > r.options.Requests {
		workers = r.options.Requests
	}
	group.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer group.Done()
			<-start
			for sequence := range jobs {
				// replay 场景只准备一个用户，因此必须先决定 token 下标，再访问切片。
				// 之前先取 tokens[sequence] 再改成 tokens[0]，sequence > 0 时会在改值前直接 panic。
				tokenIndex := sequence
				if r.options.Scenario == "replay" {
					tokenIndex = 0
				}
				token := tokens[tokenIndex]
				results[sequence] = r.executeRequest(sequence, itemID, token)
			}
		}()
	}
	elapsedStarted := time.Now()
	close(start)
	for sequence := 0; sequence < r.options.Requests; sequence++ {
		jobs <- sequence
	}
	close(jobs)
	group.Wait()
	return results, time.Since(elapsedStarted)
}

func (r *runner) executeRequest(sequence int, itemID uint64, token string) sample {
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/v1/seckill/items/%d/orders", r.options.BaseURL, itemID), nil)
	if err != nil {
		return sample{Sequence: sequence, Error: err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	startedAt := time.Now()
	response, err := r.client.Do(req)
	duration := time.Since(startedAt)
	result := sample{Sequence: sequence, Duration: duration, LatencyMS: durationMS(duration)}
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer response.Body.Close()
	result.HTTPCode = response.StatusCode
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if readErr != nil {
		result.Error = readErr.Error()
		return result
	}

	// go-zero 的 TimeoutHandler 在超时时返回 HTTP 503 + 纯文本 "Request Timeout"，
	// 而过载保护可能直接返回 HTTP 503 + 空响应体。它们都不是客户端 JSON 解码故障：
	// 前者说明请求执行超过服务端时限，后者说明请求在业务处理前就被快速拒绝。
	// 面试/优化点：状态码只能说明大类，必须结合响应体和耗时才能区分“慢请求超时”与“快速拒绝”。
	trimmedBody := strings.TrimSpace(string(body))
	if response.StatusCode == http.StatusServiceUnavailable {
		if strings.EqualFold(trimmedBody, "Request Timeout") {
			result.Code = codeServerTimeout
			result.ResponseBody = trimmedBody
			return result
		}
		if trimmedBody == "" {
			result.Code = codeServerRejected
			return result
		}
	}

	var envelope struct {
		apiResponse
		Data struct {
			Replayed bool   `json:"replayed"`
			Status   string `json:"status"`
			OrderNo  string `json:"order_no"`
			Order    struct {
				ID      uint64 `json:"id"`
				OrderNo string `json:"order_no"`
			} `json:"order"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		result.ResponseBody = responseBodySummary(trimmedBody)
		if response.StatusCode == http.StatusServiceUnavailable {
			result.Code = codeServerRejected
			return result
		}
		result.Error = "decode response: " + err.Error()
		return result
	}
	result.Code = envelope.Code
	result.Replayed = envelope.Data.Replayed
	result.OrderID = envelope.Data.Order.ID
	result.OrderNo = envelope.Data.Order.OrderNo
	if result.OrderNo == "" {
		result.OrderNo = envelope.Data.OrderNo
	}
	result.Status = envelope.Data.Status
	return result
}

// drainAsync 在入口压测计时结束后轮询最终状态。它不会把查询耗时计入 BenchmarkDuration，
// 否则“接口接收能力”和“Kafka 消费落库能力”会混成一个无法解释的 QPS 数字。
func (r *runner) drainAsync(samples []sample, tokens []string) ([]sample, time.Duration) {
	startedAt := time.Now()
	type pendingOrder struct {
		token   string
		indices []int
	}
	pending := make(map[string]*pendingOrder)
	for index := range samples {
		item := &samples[index]
		if item.HTTPCode != http.StatusAccepted || item.Code != "OK" || item.OrderNo == "" {
			continue
		}
		item.Status = "QUEUED"
		tokenIndex := item.Sequence
		if r.options.Scenario == "replay" {
			tokenIndex = 0
		}
		if tokenIndex < 0 || tokenIndex >= len(tokens) {
			item.ResultError = "missing token for result polling"
			continue
		}
		entry := pending[item.OrderNo]
		if entry == nil {
			entry = &pendingOrder{token: tokens[tokenIndex]}
			pending[item.OrderNo] = entry
		}
		entry.indices = append(entry.indices, index)
	}
	if len(pending) == 0 {
		return samples, time.Since(startedAt)
	}

	deadline := time.Now().Add(r.options.DrainTimeout)
	for len(pending) > 0 && time.Now().Before(deadline) {
		for orderNo, entry := range pending {
			status, err := r.pollAsyncResult(orderNo, entry.token)
			if err != nil {
				for _, index := range entry.indices {
					samples[index].ResultError = err.Error()
				}
				continue
			}
			for _, index := range entry.indices {
				samples[index].Status = status
				samples[index].ResultError = ""
			}
			if status == "SUCCEEDED" || status == "FAILED" {
				delete(pending, orderNo)
			}
		}
		if len(pending) == 0 {
			break
		}
		wait := r.options.PollInterval
		if remaining := time.Until(deadline); wait > remaining {
			wait = remaining
		}
		if wait > 0 {
			time.Sleep(wait)
		}
	}
	return samples, time.Since(startedAt)
}

func (r *runner) pollAsyncResult(orderNo, token string) (string, error) {
	var response struct {
		apiResponse
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	path := "/v1/seckill/orders/" + url.PathEscape(orderNo) + "/result"
	status, err := r.requestJSON(http.MethodGet, path, token, nil, &response)
	if err != nil || status != http.StatusOK || response.Code != "OK" {
		return "", combineAPIErr("查询异步订单结果", status, response.apiResponse, err)
	}
	switch response.Data.Status {
	case "QUEUED", "SUCCEEDED", "FAILED":
		return response.Data.Status, nil
	default:
		return "", fmt.Errorf("查询异步订单结果: 未知状态 %q", response.Data.Status)
	}
}

func responseBodySummary(body string) string {
	const maxLength = 512
	if len(body) <= maxLength {
		return body
	}
	return body[:maxLength] + "..."
}

func buildReport(opts options, itemID uint64, samples []sample, setupDuration, elapsed time.Duration) report {
	counts := countReport{ByHTTPStatus: make(map[string]int), ByCode: make(map[string]int)}
	latencies := make([]time.Duration, 0, len(samples))
	var latencyTotal time.Duration
	for _, item := range samples {
		counts.ByHTTPStatus[strconv.Itoa(item.HTTPCode)]++
		code := item.Code
		if code == "" && item.Error != "" && item.HTTPCode == 0 {
			code = "CLIENT_ERROR"
		} else if code == "" && item.Error != "" {
			code = "INVALID_SERVER_RESPONSE"
		}
		counts.ByCode[code]++
		if item.Status == "SUCCEEDED" {
			counts.FinalSucceeded++
		} else if item.Status == "FAILED" {
			counts.FinalFailed++
		} else if item.Status == "QUEUED" {
			counts.FinalPending++
		}
		if item.ResultError != "" {
			counts.ResultPollError++
		}
		switch {
		case item.Error != "" && item.HTTPCode == 0:
			counts.NetworkError++
		case item.HTTPCode == http.StatusOK:
			counts.HTTP200++
			if item.Replayed {
				counts.Replayed++
			} else {
				counts.Created++
			}
		case item.HTTPCode == http.StatusAccepted:
			counts.HTTP202++
			counts.Queued++
			if item.Replayed {
				counts.Replayed++
			}
		case item.Code == "OUT_OF_STOCK":
			counts.OutOfStock++
		case item.Code == "SECKILL_UNAVAILABLE":
			counts.Unavailable++
		case item.Code == "INVENTORY_BUSY":
			counts.InventoryBusy++
		case item.Code == "SECKILL_CACHE_NOT_READY":
			counts.CacheNotReady++
		case item.Code == "SECKILL_TEMPORARILY_UNAVAILABLE":
			counts.TemporaryUnavailable++
		case item.Code == codeServerTimeout:
			counts.ServerTimeout++
		case item.Code == codeServerRejected:
			counts.ServerRejected++
		default:
			counts.OtherError++
		}
		latencies = append(latencies, item.Duration)
		latencyTotal += item.Duration
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	latency := latencyReport{}
	if len(latencies) > 0 {
		latency = latencyReport{
			MinMS: durationMS(latencies[0]), AvgMS: durationMS(latencyTotal / time.Duration(len(latencies))),
			P50MS: durationMS(percentile(latencies, 50)), P90MS: durationMS(percentile(latencies, 90)),
			P95MS: durationMS(percentile(latencies, 95)), P99MS: durationMS(percentile(latencies, 99)), MaxMS: durationMS(latencies[len(latencies)-1]),
		}
	}
	throughput := 0.0
	if elapsed > 0 {
		throughput = float64(len(samples)) / elapsed.Seconds()
	}
	result := report{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano), BaseURL: opts.BaseURL, Strategy: opts.Strategy, Admission: opts.Admission, OrderMode: opts.OrderMode,
		Scenario: opts.Scenario, ItemID: itemID, SKUID: opts.SKUID, Concurrency: opts.Concurrency, Requests: opts.Requests,
		ConfiguredStock: opts.Stock, SetupDurationMS: durationMS(setupDuration), BenchmarkDurationMS: durationMS(elapsed),
		ThroughputQPS: throughput, Counts: counts, Latency: latency, Samples: samples,
	}
	result.Environment = collectEnvironment(opts)
	return result
}

func collectEnvironment(opts options) environmentReport {
	reportPath, err := filepath.Abs(opts.Output)
	if err != nil {
		// filepath.Abs 只依赖当前工作目录；即使极端环境下失败，仍保留用户传入路径，
		// 不能因为辅助元数据让一次昂贵压测的业务样本全部丢失。
		reportPath = opts.Output
	}
	result := environmentReport{
		Hardware:        fmt.Sprintf("%s/%s logical_cpu=%d", runtime.GOOS, runtime.GOARCH, runtime.NumCPU()),
		GoVersion:       runtime.Version(),
		RedisDeployment: opts.RedisDeployment,
		ReportFile:      reportPath,
	}
	var cfg config.Config
	if err := conf.Load(opts.ConfigFile, &cfg); err == nil {
		result.ServiceConfig = serviceConfigSummary{
			StockMode:                  cfg.Seckill.StockMode,
			AdmissionMode:              cfg.Seckill.AdmissionMode,
			MySQLMaxOpenConns:          cfg.MySQL.MaxOpenConns,
			MySQLMaxIdleConns:          cfg.MySQL.MaxIdleConns,
			RedisAddress:               cfg.Redis.Address,
			RedisDB:                    cfg.Redis.DB,
			RedisOperationTimeoutMilli: cfg.Redis.OperationTimeoutMilliseconds,
		}
	}
	return result
}

func collectBackendState(configFile string, itemID uint64) backendReport {
	result := backendReport{}
	var cfg config.Config
	if err := conf.Load(configFile, &cfg); err != nil {
		result.ErrorCategories = append(result.ErrorCategories, "config_load_failed")
		return result
	}
	ctx := context.Background()
	db, err := database.OpenMySQL(ctx, cfg.MySQL)
	if err != nil {
		result.ErrorCategories = append(result.ErrorCategories, "mysql_open_failed")
	} else {
		state, inspectErr := seckillmysql.New(db).InspectItemState(ctx, itemID)
		_ = db.Close()
		if inspectErr != nil {
			result.ErrorCategories = append(result.ErrorCategories, "mysql_read_failed")
		} else {
			result.MySQL = &state
		}
	}

	redisClient, err := platformcache.OpenRedis(ctx, cfg.Redis)
	if err != nil {
		result.ErrorCategories = append(result.ErrorCategories, "redis_open_failed")
	} else {
		gate, gateErr := redisgate.New(redisClient, cfg.Redis.OperationTimeout())
		if gateErr != nil {
			result.ErrorCategories = append(result.ErrorCategories, "redis_inspector_failed")
		} else {
			state, inspectErr := gate.InspectItem(ctx, itemID)
			if inspectErr != nil {
				result.ErrorCategories = append(result.ErrorCategories, "redis_read_failed")
			} else {
				result.Redis = &state
			}
		}
		_ = redisClient.Close()
	}
	// HTTP 客户端无法观察服务内部函数调用次数。claim 数只代表提交结果，不能冒充
	// Purchase 调用数；精确“售罄不回源”由 TASK-027 的 spy 测试证明。
	result.PurchaseCallsAvailable = false
	return result
}

func percentile(sorted []time.Duration, percentage int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	// nearest-rank 对小样本更保守，不会因为向下取整而低估 P99。
	index := (len(sorted)*percentage+99)/100 - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func durationMS(value time.Duration) float64 {
	return float64(value) / float64(time.Millisecond)
}

func printReport(value report) {
	fmt.Printf("完成: duration=%.2fms throughput=%.2f req/s\n", value.BenchmarkDurationMS, value.ThroughputQPS)
	fmt.Printf("结果: created=%d replayed=%d sold_out=%d busy=%d unavailable=%d cache_not_ready=%d redis_unavailable=%d server_timeout=%d server_rejected=%d network_error=%d other_error=%d\n",
		value.Counts.Created, value.Counts.Replayed, value.Counts.OutOfStock, value.Counts.InventoryBusy,
		value.Counts.Unavailable, value.Counts.CacheNotReady, value.Counts.TemporaryUnavailable, value.Counts.ServerTimeout, value.Counts.ServerRejected,
		value.Counts.NetworkError, value.Counts.OtherError)
	if value.OrderMode == "async" {
		// 异步入口的 created 必然为 0；若不单独打印终态，终端摘要会让人误以为
		// 100 个 202 全部丢失。排空指标与入口 duration 分开，保持压测口径可解释。
		fmt.Printf("异步终态: accepted=%d succeeded=%d failed=%d pending=%d poll_error=%d drain=%.2fms\n",
			value.Counts.Queued, value.Counts.FinalSucceeded, value.Counts.FinalFailed,
			value.Counts.FinalPending, value.Counts.ResultPollError, value.DrainDurationMS)
	}
	fmt.Printf("延迟(ms): min=%.3f avg=%.3f p50=%.3f p90=%.3f p95=%.3f p99=%.3f max=%.3f\n",
		value.Latency.MinMS, value.Latency.AvgMS, value.Latency.P50MS, value.Latency.P90MS,
		value.Latency.P95MS, value.Latency.P99MS, value.Latency.MaxMS)
}

func writeReport(path string, value report) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("编码报告: %w", err)
	}
	directory := filepath.Dir(path)
	if directory != "." {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return fmt.Errorf("创建报告目录: %w", err)
		}
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		return fmt.Errorf("写报告文件: %w", err)
	}
	return nil
}
