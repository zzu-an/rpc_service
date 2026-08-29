package mq

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	migratemysql "github.com/golang-migrate/migrate/v4/database/mysql"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/segmentio/kafka-go"

	"service_rpc/internal/config"
	platformcache "service_rpc/internal/platform/cache"
	platformmq "service_rpc/internal/platform/mq"
	"service_rpc/internal/seckill"
	seckillmysql "service_rpc/internal/seckill/mysqlrepo"
	"service_rpc/internal/seckill/redisgate"
)

// TestV04StageAsyncPipeline 是 v0.4 的真实依赖门禁。普通 go test 会跳过；Makefile
// 设置 V04_STAGE_VERIFY=1 后，缺少任一依赖都必须失败，防止用 fake 测试冒充阶段验收。
// 测试会写入 1000 个用户和独立 Kafka topic，因此 TEST_DSN 必须指向一次性测试库。
func TestV04StageAsyncPipeline(t *testing.T) {
	if os.Getenv("V04_STAGE_VERIFY") != "1" {
		t.Skip("V04_STAGE_VERIFY is not enabled")
	}
	dsn := requireV04Environment(t, "TEST_DSN")
	redisAddress := requireV04Environment(t, "TEST_REDIS_ADDR")
	brokers := splitRequired(t, requireV04Environment(t, "TEST_KAFKA_BROKERS"))

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open stage MySQL: %v", err)
	}
	db.SetMaxOpenConns(32)
	db.SetMaxIdleConns(16)
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping stage MySQL: %v", err)
	}
	// 远程测试库允许重复执行门禁。上一次被强制中断可能留下 stage 专用 pending，
	// 它们会按全局 outbox 顺序占据下一轮 batch。只终结带 v04 测试邮箱的历史 job，
	// 不删除订单，也不触碰普通用户产生的任务。
	if _, err := db.ExecContext(ctx, `
		UPDATE seckill_order_jobs j
		JOIN users u ON u.id = j.user_id
		SET j.status = 4, j.completed_at = CURRENT_TIMESTAMP(6), j.last_error_code = 'STAGE_SUPERSEDED'
		WHERE u.email LIKE 'v04-%@example.invalid' AND j.status IN (1, 2)
	`); err != nil {
		t.Fatalf("close stale v0.4 stage jobs: %v", err)
	}

	redisClient, err := platformcache.OpenRedis(ctx, config.RedisConfig{
		Address: redisAddress, Password: os.Getenv("TEST_REDIS_PASSWORD"),
		DB: envInt(t, "TEST_REDIS_DB", 0), DialTimeoutMilliseconds: 1000, OperationTimeoutMilliseconds: 1000,
	})
	if err != nil {
		t.Fatalf("open stage Redis: %v", err)
	}
	t.Cleanup(func() { _ = redisClient.Close() })
	gate, err := redisgate.New(redisClient, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	runID := strconv.FormatInt(time.Now().UTC().UnixNano(), 36)
	repository := seckillmysql.NewWithStockMode(db, seckillmysql.StockModeAtomic)
	users, snapshot := seedV04StageData(t, ctx, db, runID, 1000, 100)
	itemID := snapshot.Items[0].ID
	t.Cleanup(func() {
		_ = redisClient.Del(context.Background(), redisgate.StateKey(itemID), redisgate.BuyersKey(itemID)).Err()
	})
	// PublishActivity 采用显式业务时间，允许门禁不依赖 wall-clock sleep。生产代码仍会用真实 now；
	// 面试点：可注入时钟能让时间窗口测试稳定，但不能改变数据库保存的业务时间。
	if _, err := gate.PublishActivity(ctx, snapshot, snapshot.Activity.StartAt.Add(-time.Minute)); err != nil {
		t.Fatalf("preheat stage Redis: %v", err)
	}

	apiService, err := seckill.NewServiceWithAsyncAdmission(repository, repository, gate, gate, repository, NewJobFactory())
	if err != nil {
		t.Fatal(err)
	}
	type accepted struct {
		userID  uint64
		orderNo string
	}
	acceptedCh := make(chan accepted, len(users))
	var soldOut atomic.Int64
	var submitErrors atomic.Int64
	var firstSubmitError error
	var firstSubmitErrorOnce sync.Once
	var submitGroup sync.WaitGroup
	for _, userID := range users {
		userID := userID
		submitGroup.Add(1)
		go func() {
			defer submitGroup.Done()
			result, submitErr := apiService.Enqueue(ctx, userID, itemID)
			switch {
			case submitErr == nil:
				acceptedCh <- accepted{userID: userID, orderNo: result.OrderNo}
			case errors.Is(submitErr, seckill.ErrOutOfStock):
				soldOut.Add(1)
			default:
				submitErrors.Add(1)
				firstSubmitErrorOnce.Do(func() { firstSubmitError = submitErr })
			}
		}()
	}
	submitGroup.Wait()
	close(acceptedCh)
	acceptedRequests := make([]accepted, 0, 100)
	for item := range acceptedCh {
		acceptedRequests = append(acceptedRequests, item)
	}
	if len(acceptedRequests) != 100 || soldOut.Load() != 900 || submitErrors.Load() != 0 {
		t.Fatalf("accepted=%d sold_out=%d errors=%d first_error=%v, want 100/900/0", len(acceptedRequests), soldOut.Load(), submitErrors.Load(), firstSubmitError)
	}

	// 同一用户并发重放 100 次必须全部返回第一次的 order_no。HTTP 层是否使用 202 已由
	// handler 测试覆盖；这里重点验证 Redis buyer 与 MySQL job 唯一键的跨层幂等。
	first := acceptedRequests[0]
	var replayErrors atomic.Int64
	var replayGroup sync.WaitGroup
	for range 100 {
		replayGroup.Add(1)
		go func() {
			defer replayGroup.Done()
			result, replayErr := apiService.Enqueue(ctx, first.userID, itemID)
			if replayErr != nil || result.OrderNo != first.orderNo || !result.Replayed {
				replayErrors.Add(1)
			}
		}()
	}
	replayGroup.Wait()
	if replayErrors.Load() != 0 {
		t.Fatalf("same-user replay failures=%d", replayErrors.Load())
	}

	// 先用必败 producer 模拟 Kafka 不可用：job 必须仍在 MySQL，不能先改成 published。
	// 把本轮 next_publish_at 设为确定的早期时间，避免共享测试库中的其他合法 pending
	// 抢占 batch；生产代码仍按全局 next_publish_at/id 公平扫描。
	if _, err := db.ExecContext(ctx, `UPDATE seckill_order_jobs SET next_publish_at = '2000-01-01 00:00:00' WHERE seckill_item_id = ? AND status = 1`, itemID); err != nil {
		t.Fatalf("prioritize current stage jobs: %v", err)
	}
	failureRelay, _ := NewRelay(repository, stageFailingProducer{}, time.Millisecond, 100)
	if processed, err := failureRelay.ProcessOnce(ctx); err != nil || processed != 0 {
		t.Fatalf("failed-broker relay processed=%d error=%v", processed, err)
	}
	assertStageJobCounts(t, ctx, db, itemID, 100, 0, 0, 0)
	if _, err := db.ExecContext(ctx, `UPDATE seckill_order_jobs SET next_publish_at = '2000-01-01 00:00:00' WHERE seckill_item_id = ? AND status = 1`, itemID); err != nil {
		t.Fatalf("make persisted jobs immediately retryable: %v", err)
	}

	kafkaCfg := stageKafkaConfig(brokers, runID)
	mainProducer, err := platformmq.OpenKafkaProducer(ctx, kafkaCfg, kafkaCfg.MainTopic)
	if err != nil {
		t.Fatalf("open main Kafka producer: %v", err)
	}
	retryProducer, err := platformmq.OpenKafkaProducer(ctx, kafkaCfg, kafkaCfg.RetryTopic)
	if err != nil {
		_ = mainProducer.Close()
		t.Fatalf("open retry Kafka producer: %v", err)
	}
	dlqProducer, err := platformmq.OpenKafkaProducer(ctx, kafkaCfg, kafkaCfg.DLQTopic)
	if err != nil {
		_ = mainProducer.Close()
		_ = retryProducer.Close()
		t.Fatalf("open DLQ Kafka producer: %v", err)
	}
	relay, _ := NewRelay(repository, mainProducer, 20*time.Millisecond, 100)
	// broker ack 后更新 MySQL 也可能遇到瞬时断连。此时 job 仍 pending，下一轮会重复
	// 发布同一 event_id；门禁按持久状态等待恢复，不要求一次 ProcessOnce 毫无网络抖动。
	waitStage(t, 3*time.Minute, func() (bool, error) {
		pending, published, succeeded, failed, readErr := readStageJobCounts(ctx, db, itemID)
		if readErr != nil {
			return false, readErr
		}
		if pending == 0 && published == 100 && succeeded == 0 && failed == 0 {
			return true, nil
		}
		processed, relayErr := relay.ProcessOnce(ctx)
		return false, fmt.Errorf("relay processed=%d jobs=%d/%d/%d/%d error=%v", processed, pending, published, succeeded, failed, relayErr)
	}, "persisted jobs to recover and reach PUBLISHED")
	backlogBefore := stageTopicEnd(t, ctx, kafkaCfg, kafkaCfg.MainTopic)
	// offset 是位置而不是消息计数，首条消息可位于 0；这里结合上面的 processed=100，
	// 只要求 consumer 启动前 topic 的 offset span 已增长，最终再用 group lag=0 验证排空。
	if backlogBefore <= 0 {
		t.Fatalf("main topic offset before consumer=%d, want positive backlog", backlogBefore)
	}

	// 用包装仓储测量 Purchase 同时执行数。它统计的是重事务并发，不是 Kafka reader 数；
	// worker 可以拥有更多 reader，但共享 semaphore 必须把数据库并发限制在配置值以内。
	countedRepository := &stageCountingRepository{Repository: repository}
	countedRepository.failRemaining.Store(1) // 第一笔模拟暂时性 MySQL 超时，验证 retry topic 可恢复。
	workerService, _ := seckill.NewWorkerService(countedRepository, repository)
	consumer, _ := NewConsumerHandler(repository, workerService)
	delivery, _ := NewDeliveryHandler(consumer, repository, retryProducer, dlqProducer, kafkaCfg.MaxConsumeAttempts)
	runtime, err := NewRuntime(kafkaCfg, relay, delivery, mainProducer, retryProducer, dlqProducer)
	if err != nil {
		t.Fatal(err)
	}
	runtimeCtx, stopRuntime := context.WithCancel(context.Background())
	runtimeDone := make(chan error, 1)
	go func() { runtimeDone <- runtime.Run(runtimeCtx) }()
	var stopRuntimeOnce sync.Once
	stopAndWaitRuntime := func() {
		stopRuntimeOnce.Do(func() {
			stopRuntime()
			select {
			case runErr := <-runtimeDone:
				if runErr != nil {
					t.Errorf("runtime shutdown: %v", runErr)
				}
			case <-time.After(15 * time.Second):
				t.Errorf("runtime did not stop within shutdown budget")
			}
		})
	}
	t.Cleanup(stopAndWaitRuntime)

	// poison 必须转入 DLQ；随后再投合法重复消息，证明坏消息不会永久阻塞 partition。
	dlqReader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers, GroupID: kafkaCfg.ConsumerGroup + "-stage-dlq", Topic: kafkaCfg.DLQTopic,
		StartOffset: kafka.FirstOffset, MinBytes: 1, MaxBytes: 1 << 20, MaxWait: 200 * time.Millisecond,
	})
	t.Cleanup(func() { _ = dlqReader.Close() })
	if err := mainProducer.Publish(ctx, "poison", []byte("not-json")); err != nil {
		t.Fatalf("publish poison: %v", err)
	}

	waitStage(t, 150*time.Second, func() (bool, error) {
		pending, published, succeeded, failed, readErr := readStageJobCounts(ctx, db, itemID)
		if readErr != nil {
			return false, readErr
		}
		return pending == 0 && published == 0 && succeeded == 100 && failed == 0,
			fmt.Errorf("jobs pending=%d published=%d succeeded=%d failed=%d", pending, published, succeeded, failed)
	}, "100 accepted jobs to reach SUCCEEDED")
	if countedRepository.maxInFlight.Load() > int64(kafkaCfg.ConsumerConcurrency) || countedRepository.maxInFlight.Load() == 0 {
		t.Fatalf("Purchase peak=%d, configured limit=%d", countedRepository.maxInFlight.Load(), kafkaCfg.ConsumerConcurrency)
	}

	dlqCtx, cancelDLQ := context.WithTimeout(ctx, 20*time.Second)
	dlqMessage, err := dlqReader.ReadMessage(dlqCtx)
	cancelDLQ()
	if err != nil || len(dlqMessage.Value) == 0 {
		t.Fatalf("read poison from DLQ: bytes=%d error=%v", len(dlqMessage.Value), err)
	}

	firstJob, err := repository.FindJobOwned(ctx, first.userID, first.orderNo)
	if err != nil {
		t.Fatal(err)
	}
	for range 100 {
		if err := mainProducer.Publish(ctx, firstJob.OrderNo, firstJob.Payload); err != nil {
			t.Fatalf("publish duplicate event: %v", err)
		}
	}
	waitStage(t, 30*time.Second, func() (bool, error) {
		lag, lagErr := stageGroupLag(ctx, kafkaCfg, kafkaCfg.MainTopic, kafkaCfg.ConsumerGroup+"-main")
		return lagErr == nil && lag == 0, lagErr
	}, "main consumer lag to drain after duplicate messages")
	assertStageBusinessState(t, ctx, repository, gate, db, itemID, 100)

	stopAndWaitRuntime()
}

func TestV04MigrationRoundTrip(t *testing.T) {
	if os.Getenv("V04_MIGRATION_VERIFY") != "1" {
		t.Skip("V04_MIGRATION_VERIFY is not enabled")
	}
	dsn := requireV04Environment(t, "TEST_DSN")
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	driver, err := migratemysql.WithInstance(db, &migratemysql.Config{})
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	sourceURL := (&url.URL{Scheme: "file", Path: filepath.Join(repositoryRoot, "migrations")}).String()
	migrator, err := migrate.NewWithDatabaseInstance(sourceURL, "mysql", driver)
	if err != nil {
		_ = driver.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = migrator.Close() })
	if err := migrator.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migration up: %v", err)
	}
	before, dirty, err := migrator.Version()
	if err != nil || dirty || before != 8 {
		t.Fatalf("latest migration version=%d dirty=%t error=%v, want 8/false", before, dirty, err)
	}
	if err := migrator.Steps(-1); err != nil {
		t.Fatalf("migration down from v8: %v", err)
	}
	if err := migrator.Steps(1); err != nil {
		t.Fatalf("migration restore to v8: %v", err)
	}
}

type stageFailingProducer struct{}

func (stageFailingProducer) Publish(context.Context, string, []byte) error {
	return context.DeadlineExceeded
}
func (stageFailingProducer) Close() error { return nil }

type stageCountingRepository struct {
	seckill.Repository
	inFlight      atomic.Int64
	maxInFlight   atomic.Int64
	failRemaining atomic.Int64
}

func (r *stageCountingRepository) Purchase(ctx context.Context, userID, itemID uint64, orderNo string, now time.Time) (seckill.PurchaseResult, error) {
	current := r.inFlight.Add(1)
	defer r.inFlight.Add(-1)
	for peak := r.maxInFlight.Load(); current > peak && !r.maxInFlight.CompareAndSwap(peak, current); peak = r.maxInFlight.Load() {
	}
	for {
		remaining := r.failRemaining.Load()
		if remaining <= 0 {
			break
		}
		if r.failRemaining.CompareAndSwap(remaining, remaining-1) {
			return seckill.PurchaseResult{}, context.DeadlineExceeded
		}
	}
	return r.Repository.Purchase(ctx, userID, itemID, orderNo, now)
}

func seedV04StageData(t *testing.T, ctx context.Context, db *sql.DB, runID string, userCount int, stock int64) ([]uint64, seckill.PreheatSnapshot) {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	users := make([]uint64, 0, userCount)
	statement, err := tx.PrepareContext(ctx, `INSERT INTO users (email, password_hash, status) VALUES (?, 'stage-not-a-login-secret', 1)`)
	if err != nil {
		t.Fatal(err)
	}
	for index := range userCount {
		result, insertErr := statement.ExecContext(ctx, fmt.Sprintf("v04-%s-%04d@example.invalid", runID, index))
		if insertErr != nil {
			_ = statement.Close()
			t.Fatal(insertErr)
		}
		id, _ := result.LastInsertId()
		users = append(users, uint64(id))
	}
	_ = statement.Close()
	productResult, err := tx.ExecContext(ctx, `INSERT INTO products (name, description, status) VALUES (?, 'v0.4 stage', 1)`, "v04-stage-"+runID)
	if err != nil {
		t.Fatal(err)
	}
	productID, _ := productResult.LastInsertId()
	skuResult, err := tx.ExecContext(ctx, `INSERT INTO product_skus (product_id, code, name, price_cent, status) VALUES (?, ?, 'stage-sku', 9900, 1)`, productID, "V04-"+runID)
	if err != nil {
		t.Fatal(err)
	}
	skuID, _ := skuResult.LastInsertId()
	now := time.Now().UTC().Truncate(time.Microsecond)
	activityResult, err := tx.ExecContext(ctx, `INSERT INTO seckill_activities (name, start_at, end_at, status) VALUES (?, ?, ?, 1)`, "v04-stage-"+runID, now.Add(-time.Minute), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	activityID, _ := activityResult.LastInsertId()
	itemResult, err := tx.ExecContext(ctx, `INSERT INTO seckill_items (activity_id, sku_id, initial_stock, available_stock) VALUES (?, ?, ?, ?)`, activityID, skuID, stock, stock)
	if err != nil {
		t.Fatal(err)
	}
	itemID, _ := itemResult.LastInsertId()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return users, seckill.PreheatSnapshot{
		Activity: seckill.Activity{ID: uint64(activityID), Status: seckill.StatusEnabled, StartAt: now.Add(-time.Minute), EndAt: now.Add(time.Hour)},
		Items:    []seckill.Item{{ID: uint64(itemID), ActivityID: uint64(activityID), SKUID: uint64(skuID), InitialStock: stock, AvailableStock: stock}},
	}
}

func stageKafkaConfig(brokers []string, runID string) config.KafkaConfig {
	prefix := "service-rpc-v04-stage-" + runID
	return config.KafkaConfig{
		Brokers: brokers, MainTopic: prefix + "-main", RetryTopic: prefix + "-retry", DLQTopic: prefix + "-dlq",
		ConsumerGroup: prefix, AllowAutoTopicCreation: true, TopicPartitions: 4,
		// 真实门禁用单消费槽证明严格上限和 backlog 排空，减少单 broker 上多成员同时
		// join 造成的重平衡噪声；Runtime 的多 reader/共享 semaphore 另有并发单测。
		OperationTimeoutMilliseconds: 3000, ConsumerConcurrency: 1, MaxConsumeAttempts: 3,
		RelayIntervalMilliseconds: 20, ShutdownTimeoutMilliseconds: 10000,
	}
}

func stageTopicEnd(t *testing.T, ctx context.Context, cfg config.KafkaConfig, topic string) int64 {
	t.Helper()
	bootstrap, err := kafka.DialContext(ctx, "tcp", cfg.Brokers[0])
	if err != nil {
		t.Fatal(err)
	}
	partitions, err := bootstrap.ReadPartitions(topic)
	_ = bootstrap.Close()
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, partition := range partitions {
		leader := net.JoinHostPort(partition.Leader.Host, strconv.Itoa(partition.Leader.Port))
		conn, dialErr := kafka.DialLeader(ctx, "tcp", leader, topic, partition.ID)
		if dialErr != nil {
			t.Fatal(dialErr)
		}
		end, readErr := conn.ReadLastOffset()
		_ = conn.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		total += end
	}
	return total
}

func stageGroupLag(ctx context.Context, cfg config.KafkaConfig, topic, group string) (int64, error) {
	client := &kafka.Client{Addr: kafka.TCP(cfg.Brokers...), Timeout: cfg.OperationTimeout()}
	committed, err := client.ConsumerOffsets(ctx, kafka.TopicAndGroup{Topic: topic, GroupId: group})
	if err != nil {
		return 0, err
	}
	bootstrap, err := kafka.DialContext(ctx, "tcp", cfg.Brokers[0])
	if err != nil {
		return 0, err
	}
	partitions, err := bootstrap.ReadPartitions(topic)
	_ = bootstrap.Close()
	if err != nil {
		return 0, err
	}
	var lag int64
	for _, partition := range partitions {
		leader := net.JoinHostPort(partition.Leader.Host, strconv.Itoa(partition.Leader.Port))
		conn, dialErr := kafka.DialLeader(ctx, "tcp", leader, topic, partition.ID)
		if dialErr != nil {
			return 0, dialErr
		}
		end, readErr := conn.ReadLastOffset()
		_ = conn.Close()
		if readErr != nil {
			return 0, readErr
		}
		current := committed[partition.ID]
		if current < 0 {
			current = 0
		}
		if end > current {
			lag += end - current
		}
	}
	return lag, nil
}

func assertStageBusinessState(t *testing.T, ctx context.Context, repository *seckillmysql.Repository, gate *redisgate.Gate, db *sql.DB, itemID uint64, want int64) {
	t.Helper()
	dbState, err := repository.InspectItemState(ctx, itemID)
	if err != nil {
		t.Fatal(err)
	}
	redisState, err := gate.InspectItem(ctx, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if dbState.AvailableStock != 0 || dbState.ClaimCount != want || redisState.Stock != 0 || redisState.BuyerCount != want {
		t.Fatalf("db=%+v redis=%+v, want stock=0 claims/buyers=%d", dbState, redisState, want)
	}
	assertStageJobCounts(t, ctx, db, itemID, 0, 0, want, 0)
}

func readStageJobCounts(ctx context.Context, db *sql.DB, itemID uint64) (pending, published, succeeded, failed int64, err error) {
	err = db.QueryRowContext(ctx, `SELECT COALESCE(SUM(status=1),0), COALESCE(SUM(status=2),0), COALESCE(SUM(status=3),0), COALESCE(SUM(status=4),0) FROM seckill_order_jobs WHERE seckill_item_id=?`, itemID).Scan(&pending, &published, &succeeded, &failed)
	return
}

func assertStageJobCounts(t *testing.T, ctx context.Context, db *sql.DB, itemID uint64, wantPending, wantPublished, wantSucceeded, wantFailed int64) {
	t.Helper()
	pending, published, succeeded, failed, err := readStageJobCounts(ctx, db, itemID)
	if err != nil {
		t.Fatalf("read stage job counts: %v", err)
	}
	if pending != wantPending || published != wantPublished || succeeded != wantSucceeded || failed != wantFailed {
		t.Fatalf("job counts=%d/%d/%d/%d, want %d/%d/%d/%d", pending, published, succeeded, failed, wantPending, wantPublished, wantSucceeded, wantFailed)
	}
}

func waitStage(t *testing.T, timeout time.Duration, check func() (bool, error), description string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		ok, err := check()
		if ok {
			return
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s: %v", description, lastErr)
}

func requireV04Environment(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required by the v0.4 stage gate", name)
	}
	return value
}

func splitRequired(t *testing.T, value string) []string {
	t.Helper()
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	if len(result) == 0 {
		t.Fatal("TEST_KAFKA_BROKERS contains no broker")
	}
	return result
}

func envInt(t *testing.T, name string, fallback int) int {
	t.Helper()
	if os.Getenv(name) == "" {
		return fallback
	}
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil || value < 0 {
		t.Fatalf("%s must be a non-negative integer", name)
	}
	return value
}
