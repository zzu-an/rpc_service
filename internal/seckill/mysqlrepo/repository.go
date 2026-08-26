// Package mysqlrepo 实现秒杀模块的 MySQL 持久化。
package mysqlrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	gomysql "github.com/go-sql-driver/mysql"

	"service_rpc/internal/order"
	ordermysql "service_rpc/internal/order/mysqlrepo"
	"service_rpc/internal/product"
	"service_rpc/internal/seckill"
)

type Repository struct {
	db        *sql.DB
	stockMode StockMode
}

type StockMode uint8

const (
	StockModeAtomic StockMode = iota + 1
	StockModePessimistic
	StockModeOptimistic
)

const optimisticMaxAttempts = 32

func New(db *sql.DB) *Repository {
	return NewWithStockMode(db, StockModeAtomic)
}

// NewWithStockMode 只供主程序装配和当前阶段测试选择实现，不能由 HTTP 请求动态控制。
// 如果允许客户端指定锁策略，攻击者可以主动选择高开销路径，外部契约也会泄漏内部存储细节。
func NewWithStockMode(db *sql.DB, mode StockMode) *Repository {
	return &Repository{db: db, stockMode: mode}
}

// ParseStockMode 把配置文本收敛成内部枚举。空值保持向后兼容并默认使用原子 UPDATE。
// 未知值必须在启动阶段失败，不能静默回退，否则压测报告可能把实际 atomic 错标成其他策略。
func ParseStockMode(value string) (StockMode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "atomic":
		return StockModeAtomic, nil
	case "pessimistic":
		return StockModePessimistic, nil
	case "optimistic":
		return StockModeOptimistic, nil
	default:
		return 0, fmt.Errorf("unsupported seckill stock mode %q; use atomic, pessimistic, or optimistic", value)
	}
}

func (m StockMode) String() string {
	switch m {
	case StockModeAtomic:
		return "atomic"
	case StockModePessimistic:
		return "pessimistic"
	case StockModeOptimistic:
		return "optimistic"
	default:
		return fmt.Sprintf("unknown(%d)", m)
	}
}

func (r *Repository) CreateActivity(ctx context.Context, input seckill.CreateActivityInput) (seckill.Activity, error) {
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO seckill_activities (name, start_at, end_at, status)
		VALUES (?, ?, ?, ?)
	`, input.Name, input.StartAt, input.EndAt, seckill.StatusDisabled)
	if err != nil {
		return seckill.Activity{}, fmt.Errorf("insert seckill activity: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return seckill.Activity{}, fmt.Errorf("read seckill activity ID: %w", err)
	}
	return seckill.Activity{
		ID:      uint64(id),
		Name:    input.Name,
		StartAt: input.StartAt,
		EndAt:   input.EndAt,
		Status:  seckill.StatusDisabled,
	}, nil
}

func (r *Repository) AddItem(ctx context.Context, input seckill.AddItemInput) (seckill.Item, error) {
	// 管理端预检查用于返回清晰的业务错误；真正的一致性仍由外键和唯一索引兜底。
	// 面试常见误区是把“先查一次”当成并发保证，实际上检查结束后数据仍可能变化。
	var activityID uint64
	if err := r.db.QueryRowContext(ctx, `SELECT id FROM seckill_activities WHERE id = ?`, input.ActivityID).Scan(&activityID); errors.Is(err, sql.ErrNoRows) {
		return seckill.Item{}, seckill.ErrActivityNotFound
	} else if err != nil {
		return seckill.Item{}, fmt.Errorf("find seckill activity: %w", err)
	}

	var skuID uint64
	err := r.db.QueryRowContext(ctx, `
		SELECT s.id
		FROM product_skus s
		JOIN products p ON p.id = s.product_id
		WHERE s.id = ? AND s.status = ? AND p.status = ?
	`, input.SKUID, product.StatusActive, product.StatusActive).Scan(&skuID)
	if errors.Is(err, sql.ErrNoRows) {
		return seckill.Item{}, seckill.ErrInvalidArgument
	}
	if err != nil {
		return seckill.Item{}, fmt.Errorf("find active SKU for seckill: %w", err)
	}

	result, err := r.db.ExecContext(ctx, `
		INSERT INTO seckill_items (activity_id, sku_id, initial_stock, available_stock)
		VALUES (?, ?, ?, ?)
	`, input.ActivityID, input.SKUID, input.Stock, input.Stock)
	if err != nil {
		return seckill.Item{}, mapConflict("insert seckill item", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return seckill.Item{}, fmt.Errorf("read seckill item ID: %w", err)
	}
	return seckill.Item{
		ID:             uint64(id),
		ActivityID:     input.ActivityID,
		SKUID:          input.SKUID,
		InitialStock:   input.Stock,
		AvailableStock: input.Stock,
	}, nil
}

func (r *Repository) SetActivityStatus(ctx context.Context, activityID uint64, status uint8) error {
	result, err := r.db.ExecContext(ctx, `UPDATE seckill_activities SET status = ? WHERE id = ?`, status, activityID)
	if err != nil {
		return fmt.Errorf("set seckill activity status: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read seckill activity update count: %w", err)
	}
	if rows > 0 {
		return nil
	}

	// MySQL 默认把“值没有变化”也计为 0 affected rows，所以不能直接把 0 当作不存在。
	// 这是常见面试点：RowsAffected 的语义受 SQL 和驱动配置影响，必须结合业务场景解释。
	exists, err := r.activityExists(ctx, activityID)
	if err != nil {
		return err
	}
	if !exists {
		return seckill.ErrActivityNotFound
	}
	return nil
}

func (r *Repository) activityExists(ctx context.Context, activityID uint64) (bool, error) {
	var exists bool
	if err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM seckill_activities WHERE id = ?)`, activityID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check seckill activity existence: %w", err)
	}
	return exists, nil
}

type purchaseSnapshot struct {
	activityID   uint64
	skuID        uint64
	activityFlag uint8
	startAt      time.Time
	endAt        time.Time
	productName  string
	skuCode      string
	skuName      string
	priceCent    int64
	skuStatus    uint8
	productState uint8
	stock        int64
	version      uint64
}

func (r *Repository) Purchase(ctx context.Context, userID, itemID uint64, orderNo string, now time.Time) (seckill.PurchaseResult, error) {
	// 快速查询只用于减少重复请求的事务开销，不能替代唯一索引。
	// 两个并发请求都可能在这里查不到记录，最终仍必须由 uk_seckill_claim_user_item 决定唯一赢家。
	if existing, found, err := r.findExistingPurchase(ctx, userID, itemID); err != nil {
		return seckill.PurchaseResult{}, err
	} else if found {
		return seckill.PurchaseResult{Order: existing, Replayed: true}, nil
	}

	attemptLimit := 1
	if r.stockMode == StockModeOptimistic {
		attemptLimit = optimisticMaxAttempts
	}
	for attempt := 0; attempt < attemptLimit; attempt++ {
		created, err := r.purchaseOnce(ctx, userID, itemID, orderNo, now)
		switch {
		case err == nil:
			return created, nil
		case errors.Is(err, errDuplicateClaim):
			// 唯一键冲突发生时，InnoDB 会等待竞争事务结束后才返回 duplicate key。
			// purchaseOnce 返回前已经 Rollback，随后再读不会看到自己事务里的临时扣减和临时订单。
			existing, found, findErr := r.findExistingPurchase(ctx, userID, itemID)
			if findErr != nil {
				return seckill.PurchaseResult{}, findErr
			}
			if !found {
				return seckill.PurchaseResult{}, fmt.Errorf("duplicate seckill claim without committed order")
			}
			return seckill.PurchaseResult{Order: existing, Replayed: true}, nil
		case errors.Is(err, errOptimisticConflict):
			if attempt+1 == attemptLimit {
				return seckill.PurchaseResult{}, seckill.ErrInventoryBusy
			}
			if err := waitOptimisticRetry(ctx, userID, attempt); err != nil {
				return seckill.PurchaseResult{}, err
			}
		default:
			return seckill.PurchaseResult{}, err
		}
	}
	return seckill.PurchaseResult{}, seckill.ErrInventoryBusy
}

var (
	errDuplicateClaim     = errors.New("duplicate seckill claim")
	errOptimisticConflict = errors.New("optimistic stock version conflict")
)

func waitOptimisticRetry(ctx context.Context, userID uint64, attempt int) error {
	// 指数退避上限很小，因为事务内只竞争一行；附加可重复 jitter，避免所有 goroutine 同步醒来再次碰撞。
	// 重试必须同时受次数和 context 约束。无限重试会在热点或数据库故障时把冲突放大成自我 DoS。
	shift := attempt
	if shift > 4 {
		shift = 4
	}
	delay := time.Duration(1<<shift)*100*time.Microsecond + time.Duration((userID+uint64(attempt)*17)%7)*50*time.Microsecond
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (r *Repository) purchaseOnce(ctx context.Context, userID, itemID uint64, orderNo string, now time.Time) (seckill.PurchaseResult, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return seckill.PurchaseResult{}, fmt.Errorf("begin seckill purchase: %w", err)
	}
	// Rollback 在 Commit 后会返回 sql.ErrTxDone，因此可以无条件 defer。
	// 关键意义是任何早退路径都不能遗留“扣了库存但没订单”的半成品。
	defer func() { _ = tx.Rollback() }()

	snapshot, err := loadPurchaseSnapshot(ctx, tx, itemID, r.stockMode == StockModePessimistic)
	if err != nil {
		return seckill.PurchaseResult{}, err
	}
	if snapshot.activityFlag != seckill.StatusEnabled || now.Before(snapshot.startAt) || !now.Before(snapshot.endAt) {
		return seckill.PurchaseResult{}, seckill.ErrUnavailable
	}
	if snapshot.skuStatus != product.StatusActive || snapshot.productState != product.StatusActive {
		return seckill.PurchaseResult{}, seckill.ErrUnavailable
	}

	if err := r.decrementStock(ctx, tx, itemID, snapshot); err != nil {
		return seckill.PurchaseResult{}, err
	}

	// DATETIME(6) 只保留微秒。显式写入同一精度的 createdAt，保证首次响应与后续从数据库读取的幂等响应完全一致。
	// 如果首次返回内存中的纳秒时间、重放返回数据库微秒时间，同一订单会出现字段漂移，容易误导调用方做错误比较。
	createdAt := now.UTC().Truncate(time.Microsecond)
	orderResult, err := tx.ExecContext(ctx, `
		INSERT INTO orders (order_no, user_id, status, total_amount_cent, created_at)
		VALUES (?, ?, 1, ?, ?)
	`, orderNo, userID, snapshot.priceCent, createdAt)
	if err != nil {
		return seckill.PurchaseResult{}, fmt.Errorf("insert seckill order: %w", err)
	}
	orderID, err := orderResult.LastInsertId()
	if err != nil {
		return seckill.PurchaseResult{}, fmt.Errorf("read seckill order ID: %w", err)
	}
	itemResult, err := tx.ExecContext(ctx, `
		INSERT INTO order_items (order_id, sku_id, product_name, sku_code, sku_name, unit_price_cent, quantity, subtotal_cent)
		VALUES (?, ?, ?, ?, ?, ?, 1, ?)
	`, orderID, snapshot.skuID, snapshot.productName, snapshot.skuCode, snapshot.skuName, snapshot.priceCent, snapshot.priceCent)
	if err != nil {
		return seckill.PurchaseResult{}, fmt.Errorf("insert seckill order item: %w", err)
	}
	orderItemID, err := itemResult.LastInsertId()
	if err != nil {
		return seckill.PurchaseResult{}, fmt.Errorf("read seckill order item ID: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO seckill_order_claims (activity_id, seckill_item_id, user_id, order_id)
		VALUES (?, ?, ?, ?)
	`, snapshot.activityID, itemID, userID, orderID)
	if err != nil {
		if isDuplicateKey(err) {
			return seckill.PurchaseResult{}, errDuplicateClaim
		}
		return seckill.PurchaseResult{}, fmt.Errorf("insert seckill order claim: %w", err)
	}

	if err := tx.Commit(); err != nil {
		// Commit 返回错误时，调用方不能仅凭错误判断数据库一定没有提交。
		// 正确恢复方式是使用相同用户和 item 重试，由唯一索引查询已提交订单，而不是盲目再次扣库存。
		return seckill.PurchaseResult{}, fmt.Errorf("commit seckill purchase: %w", err)
	}
	created := order.Order{
		ID:              uint64(orderID),
		OrderNo:         orderNo,
		UserID:          userID,
		Status:          1,
		TotalAmountCent: snapshot.priceCent,
		CreatedAt:       createdAt,
		Items: []order.Item{{
			ID:            uint64(orderItemID),
			SKUID:         snapshot.skuID,
			ProductName:   snapshot.productName,
			SKUCode:       snapshot.skuCode,
			SKUName:       snapshot.skuName,
			UnitPriceCent: snapshot.priceCent,
			Quantity:      1,
			SubtotalCent:  snapshot.priceCent,
		}},
	}
	return seckill.PurchaseResult{Order: created}, nil
}

func loadPurchaseSnapshot(ctx context.Context, tx *sql.Tx, itemID uint64, forUpdate bool) (purchaseSnapshot, error) {
	var value purchaseSnapshot
	query := `
		SELECT i.activity_id, i.sku_id, a.status, a.start_at, a.end_at,
		       p.name, s.code, s.name, s.price_cent, s.status, p.status,
		       i.available_stock, i.version
		FROM seckill_items i
		JOIN seckill_activities a ON a.id = i.activity_id
		JOIN product_skus s ON s.id = i.sku_id
		JOIN products p ON p.id = s.product_id
		WHERE i.id = ?
	`
	if forUpdate {
		// FOR UPDATE 只锁命中的库存行，锁持续到事务提交/回滚；业务 SQL 必须保持固定锁顺序以降低死锁概率。
		query += " FOR UPDATE"
	}
	err := tx.QueryRowContext(ctx, query, itemID).Scan(
		&value.activityID, &value.skuID, &value.activityFlag, &value.startAt, &value.endAt,
		&value.productName, &value.skuCode, &value.skuName, &value.priceCent, &value.skuStatus, &value.productState,
		&value.stock, &value.version,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return purchaseSnapshot{}, seckill.ErrItemNotFound
	}
	if err != nil {
		return purchaseSnapshot{}, fmt.Errorf("load seckill purchase snapshot: %w", err)
	}
	return value, nil
}

func (r *Repository) decrementStock(ctx context.Context, tx *sql.Tx, itemID uint64, snapshot purchaseSnapshot) error {
	var (
		result sql.Result
		err    error
	)
	switch r.stockMode {
	case StockModeAtomic:
		result, err = tx.ExecContext(ctx, `
			UPDATE seckill_items
			SET available_stock = available_stock - 1, version = version + 1
			WHERE id = ? AND available_stock > 0
		`, itemID)
	case StockModePessimistic:
		if snapshot.stock <= 0 {
			return seckill.ErrOutOfStock
		}
		// loadPurchaseSnapshot 已经通过 FOR UPDATE 获得排他锁，因此这里的减一不会和其他事务交错。
		// 悲观锁适合冲突高且事务很短的场景，代价是热点行请求排队，P99 延迟可能明显上升。
		result, err = tx.ExecContext(ctx, `
			UPDATE seckill_items
			SET available_stock = available_stock - 1, version = version + 1
			WHERE id = ?
		`, itemID)
	case StockModeOptimistic:
		if snapshot.stock <= 0 {
			return seckill.ErrOutOfStock
		}
		// version 把“我读到的旧状态”写进 WHERE；只有版本仍一致的事务能成功。
		// CAS 适合冲突较低且不希望长时间持锁的场景，高冲突时会产生额外查询和重试，所以并非总比悲观锁快。
		result, err = tx.ExecContext(ctx, `
			UPDATE seckill_items
			SET available_stock = available_stock - 1, version = version + 1
			WHERE id = ? AND available_stock > 0 AND version = ?
		`, itemID, snapshot.version)
	default:
		return fmt.Errorf("unsupported seckill stock mode %d", r.stockMode)
	}
	if err != nil {
		return fmt.Errorf("decrement seckill stock with mode %d: %w", r.stockMode, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read seckill stock update count: %w", err)
	}
	if r.stockMode == StockModeOptimistic && rows == 0 {
		return errOptimisticConflict
	}
	// 原子模式的条件判断和减一由同一条 UPDATE 完成；RowsAffected=0 表示库存不足。
	// 悲观模式已经锁行并检查库存，0 行代表不应发生的数据异常，也按售罄安全失败。
	if rows == 0 {
		return seckill.ErrOutOfStock
	}
	return nil
}

func (r *Repository) findExistingPurchase(ctx context.Context, userID, itemID uint64) (order.Order, bool, error) {
	var orderID uint64
	err := r.db.QueryRowContext(ctx, `
		SELECT order_id FROM seckill_order_claims
		WHERE user_id = ? AND seckill_item_id = ?
	`, userID, itemID).Scan(&orderID)
	if errors.Is(err, sql.ErrNoRows) {
		return order.Order{}, false, nil
	}
	if err != nil {
		return order.Order{}, false, fmt.Errorf("find existing seckill claim: %w", err)
	}
	found, err := ordermysql.New(r.db).FindOwned(ctx, userID, orderID)
	if err != nil {
		return order.Order{}, false, fmt.Errorf("load existing seckill order: %w", err)
	}
	return found, true, nil
}

func isDuplicateKey(err error) bool {
	var mysqlErr *gomysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}

func mapConflict(operation string, err error) error {
	if isDuplicateKey(err) {
		return seckill.ErrConflict
	}
	return fmt.Errorf("%s: %w", operation, err)
}
