// Package mysqlrepo implements basic order persistence with MySQL.
package mysqlrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	gomysql "github.com/go-sql-driver/mysql"

	"service_rpc/internal/order"
	"service_rpc/internal/order/events"
)

type Repository struct{ db *sql.DB }

func New(db *sql.DB) *Repository { return &Repository{db: db} }

// Create 保留给普通订单 Service；输入必须已经由 product-rpc adapter 填充冻结快照。
// repository 不判断快照是否“来自可信 RPC”，它只验证结构和金额不变量；信任边界属于应用层。
func (r *Repository) Create(ctx context.Context, userID uint64, orderNo string, inputs []order.ItemInput) (order.Order, error) {
	result, err := r.CreateIdempotent(ctx, userID, orderNo, inputs)
	return result.Order, err
}

// CreateIdempotent 只写 orders/order_items。先 INSERT、让 uk_orders_order_no 在并发下
// 裁决唯一赢家；绝不能用“SELECT 不存在再 INSERT”，因为两个事务会同时看到不存在。
// 若 Commit 已成功但响应丢失，重试会命中唯一键并逐字段校验后读回原订单。
func (r *Repository) CreateIdempotent(ctx context.Context, userID uint64, orderNo string, inputs []order.ItemInput) (order.CreateResult, error) {
	orderNo = strings.TrimSpace(orderNo)
	items, total, err := normalizeItems(userID, orderNo, inputs)
	if err != nil {
		return order.CreateResult{}, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return order.CreateResult{}, fmt.Errorf("begin order create: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	result, err := tx.ExecContext(ctx, `INSERT INTO orders (order_no, user_id, status, total_amount_cent, created_at) VALUES (?, ?, 1, ?, ?)`, orderNo, userID, total, createdAt)
	if err != nil {
		if !isDuplicate(err) {
			return order.CreateResult{}, fmt.Errorf("insert order: %w", err)
		}
		// 唯一键冲突可能来自请求重放，也可能是伪造同 order_no。数据库会等待赢家事务
		// 完成后再返回 1062，因此此时可在当前事务读取稳定载荷并做冲突校验。
		found, loadErr := findByOrderNoTx(ctx, tx, orderNo)
		if loadErr != nil {
			return order.CreateResult{}, loadErr
		}
		if !samePayload(found, userID, total, items) {
			return order.CreateResult{}, order.ErrOrderConflict
		}
		if err := tx.Commit(); err != nil {
			return order.CreateResult{}, fmt.Errorf("commit replayed order read: %w", err)
		}
		return order.CreateResult{Order: found, Replayed: true}, nil
	}
	orderID, err := result.LastInsertId()
	if err != nil {
		return order.CreateResult{}, fmt.Errorf("read order ID: %w", err)
	}
	for i := range items {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO order_items (order_id, sku_id, product_name, sku_code, sku_name, unit_price_cent, quantity, subtotal_cent)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, orderID, items[i].SKUID, items[i].ProductName, items[i].SKUCode, items[i].SKUName, items[i].UnitPriceCent, items[i].Quantity, items[i].SubtotalCent)
		if err != nil {
			return order.CreateResult{}, fmt.Errorf("insert order item: %w", err)
		}
		itemID, err := result.LastInsertId()
		if err != nil {
			return order.CreateResult{}, fmt.Errorf("read order item ID: %w", err)
		}
		items[i].ID = uint64(itemID)
	}
	source := events.OrderSourceNormal
	if strings.HasPrefix(orderNo, "T") {
		source = events.OrderSourceSeckill
	}
	domainEvent, err := events.NewOrderCreatedV1(order.Order{
		ID: uint64(orderID), OrderNo: orderNo, UserID: userID, Status: 1,
		TotalAmountCent: total, Items: items, CreatedAt: createdAt,
	}, source)
	if err != nil {
		return order.CreateResult{}, fmt.Errorf("build order outbox event: %w", err)
	}
	payload, err := events.EncodeOrderCreatedV1(domainEvent)
	if err != nil {
		return order.CreateResult{}, err
	}
	// 订单、明细与 Outbox 在同一个 MySQL 本地事务中：Kafka 挂掉不会阻止订单提交，
	// relay 恢复后仍能发布。它不是 MySQL+Kafka 全局事务，ack 后仍存在重复窗口。
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO order_outbox_events (
			event_id, order_id, aggregate_key, event_type, schema_version,
			payload, status, next_attempt_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, domainEvent.EventID, orderID, orderNo, domainEvent.EventType, domainEvent.SchemaVersion,
		payload, order.OutboxStatusPending, createdAt.Truncate(time.Millisecond)); err != nil {
		return order.CreateResult{}, fmt.Errorf("insert order outbox event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		// 面试点：Commit 返回错误不等于事务一定回滚，连接可能只丢了响应。
		// repository 不能在这里换 order_no 重试；上层必须复用原 key 再调用本方法恢复。
		return order.CreateResult{}, fmt.Errorf("commit order create: %w", err)
	}
	created, err := r.FindOwned(ctx, userID, uint64(orderID))
	if err != nil {
		return order.CreateResult{}, err
	}
	return order.CreateResult{Order: created}, nil
}

func normalizeItems(userID uint64, orderNo string, inputs []order.ItemInput) ([]order.Item, int64, error) {
	if userID == 0 || orderNo == "" || len(inputs) == 0 {
		return nil, 0, order.ErrInvalidOrder
	}
	seen := make(map[uint64]struct{}, len(inputs))
	items := make([]order.Item, 0, len(inputs))
	var total int64
	for _, input := range inputs {
		input.ProductName = strings.TrimSpace(input.ProductName)
		input.SKUCode = strings.TrimSpace(input.SKUCode)
		input.SKUName = strings.TrimSpace(input.SKUName)
		if input.SKUID == 0 || input.ProductName == "" || input.SKUCode == "" || input.SKUName == "" || input.UnitPriceCent < 0 || input.Quantity < 1 || input.Quantity > 100 {
			return nil, 0, order.ErrInvalidOrder
		}
		if _, exists := seen[input.SKUID]; exists {
			return nil, 0, order.ErrInvalidOrder
		}
		seen[input.SKUID] = struct{}{}
		if input.UnitPriceCent != 0 && input.Quantity > math.MaxInt64/input.UnitPriceCent {
			return nil, 0, order.ErrInvalidOrder
		}
		subtotal := input.UnitPriceCent * input.Quantity
		if total > math.MaxInt64-subtotal {
			return nil, 0, order.ErrInvalidOrder
		}
		total += subtotal
		items = append(items, order.Item{
			SKUID: input.SKUID, ProductName: input.ProductName, SKUCode: input.SKUCode,
			SKUName: input.SKUName, UnitPriceCent: input.UnitPriceCent,
			Quantity: input.Quantity, SubtotalCent: subtotal,
		})
	}
	return items, total, nil
}

func isDuplicate(err error) bool {
	var mysqlError *gomysql.MySQLError
	return errors.As(err, &mysqlError) && mysqlError.Number == 1062
}

func samePayload(found order.Order, userID uint64, total int64, items []order.Item) bool {
	if found.UserID != userID || found.TotalAmountCent != total || len(found.Items) != len(items) {
		return false
	}
	for index := range items {
		left, right := found.Items[index], items[index]
		if left.SKUID != right.SKUID || left.ProductName != right.ProductName || left.SKUCode != right.SKUCode ||
			left.SKUName != right.SKUName || left.UnitPriceCent != right.UnitPriceCent ||
			left.Quantity != right.Quantity || left.SubtotalCent != right.SubtotalCent {
			return false
		}
	}
	return true
}

func findByOrderNoTx(ctx context.Context, tx *sql.Tx, orderNo string) (order.Order, error) {
	var found order.Order
	err := tx.QueryRowContext(ctx, `
		SELECT id, order_no, user_id, status, total_amount_cent, created_at
		FROM orders WHERE order_no = ?
	`, orderNo).Scan(&found.ID, &found.OrderNo, &found.UserID, &found.Status, &found.TotalAmountCent, &found.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return order.Order{}, order.ErrOrderNotFound
	}
	if err != nil {
		return order.Order{}, fmt.Errorf("find order by idempotency key: %w", err)
	}
	items, err := findItems(ctx, tx, found.ID)
	if err != nil {
		return order.Order{}, err
	}
	found.Items = items
	return found, nil
}

func (r *Repository) FindOwned(ctx context.Context, userID, orderID uint64) (order.Order, error) {
	var found order.Order
	err := r.db.QueryRowContext(ctx, `SELECT id, order_no, user_id, status, total_amount_cent, created_at FROM orders WHERE id = ? AND user_id = ?`, orderID, userID).Scan(&found.ID, &found.OrderNo, &found.UserID, &found.Status, &found.TotalAmountCent, &found.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return order.Order{}, order.ErrOrderNotFound
	}
	if err != nil {
		return order.Order{}, fmt.Errorf("find owned order: %w", err)
	}
	items, err := findItems(ctx, r.db, orderID)
	if err != nil {
		return order.Order{}, err
	}
	found.Items = items
	return found, nil
}

func (r *Repository) FindOwnedByOrderNo(ctx context.Context, userID uint64, orderNo string) (order.Order, error) {
	var found order.Order
	err := r.db.QueryRowContext(ctx, `
		SELECT id, order_no, user_id, status, total_amount_cent, created_at
		FROM orders WHERE order_no = ? AND user_id = ?
	`, strings.TrimSpace(orderNo), userID).Scan(&found.ID, &found.OrderNo, &found.UserID, &found.Status, &found.TotalAmountCent, &found.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return order.Order{}, order.ErrOrderNotFound
	}
	if err != nil {
		return order.Order{}, fmt.Errorf("find owned order by number: %w", err)
	}
	items, err := findItems(ctx, r.db, found.ID)
	if err != nil {
		return order.Order{}, err
	}
	found.Items = items
	return found, nil
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func findItems(ctx context.Context, queryer queryer, orderID uint64) ([]order.Item, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT id, sku_id, product_name, sku_code, sku_name, unit_price_cent, quantity, subtotal_cent FROM order_items WHERE order_id = ? ORDER BY id`, orderID)
	if err != nil {
		return nil, fmt.Errorf("find order items: %w", err)
	}
	defer rows.Close()
	items := make([]order.Item, 0)
	for rows.Next() {
		var item order.Item
		if err := rows.Scan(&item.ID, &item.SKUID, &item.ProductName, &item.SKUCode, &item.SKUName, &item.UnitPriceCent, &item.Quantity, &item.SubtotalCent); err != nil {
			return nil, fmt.Errorf("scan order item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate order items: %w", err)
	}
	return items, nil
}

var _ order.Repository = (*Repository)(nil)
var _ order.IdempotentRepository = (*Repository)(nil)
