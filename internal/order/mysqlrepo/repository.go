// Package mysqlrepo implements basic order persistence with MySQL.
package mysqlrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"service_rpc/internal/order"
	"service_rpc/internal/product"
)

type Repository struct{ db *sql.DB }

func New(db *sql.DB) *Repository { return &Repository{db: db} }

// Create resolves every price inside the same transaction and writes immutable
// snapshots. There is deliberately no inventory read, lock, or update.
func (r *Repository) Create(ctx context.Context, userID uint64, orderNo string, inputs []order.ItemInput) (order.Order, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return order.Order{}, fmt.Errorf("begin order create: %w", err)
	}
	defer tx.Rollback()
	items := make([]order.Item, 0, len(inputs))
	var total int64
	for _, input := range inputs {
		var item order.Item
		err := tx.QueryRowContext(ctx, `
			SELECT s.id, p.name, s.code, s.name, s.price_cent
			FROM product_skus s JOIN products p ON p.id = s.product_id
			WHERE s.id = ? AND s.status = ? AND p.status = ?
		`, input.SKUID, product.StatusActive, product.StatusActive).Scan(&item.SKUID, &item.ProductName, &item.SKUCode, &item.SKUName, &item.UnitPriceCent)
		if errors.Is(err, sql.ErrNoRows) {
			return order.Order{}, order.ErrInvalidOrder
		}
		if err != nil {
			return order.Order{}, fmt.Errorf("load order SKU: %w", err)
		}
		item.Quantity = input.Quantity
		item.SubtotalCent = item.UnitPriceCent * input.Quantity
		total += item.SubtotalCent
		items = append(items, item)
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO orders (order_no, user_id, status, total_amount_cent) VALUES (?, ?, 1, ?)`, orderNo, userID, total)
	if err != nil {
		return order.Order{}, fmt.Errorf("insert order: %w", err)
	}
	orderID, err := result.LastInsertId()
	if err != nil {
		return order.Order{}, fmt.Errorf("read order ID: %w", err)
	}
	for i := range items {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO order_items (order_id, sku_id, product_name, sku_code, sku_name, unit_price_cent, quantity, subtotal_cent)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, orderID, items[i].SKUID, items[i].ProductName, items[i].SKUCode, items[i].SKUName, items[i].UnitPriceCent, items[i].Quantity, items[i].SubtotalCent)
		if err != nil {
			return order.Order{}, fmt.Errorf("insert order item: %w", err)
		}
		itemID, err := result.LastInsertId()
		if err != nil {
			return order.Order{}, fmt.Errorf("read order item ID: %w", err)
		}
		items[i].ID = uint64(itemID)
	}
	if err := tx.Commit(); err != nil {
		return order.Order{}, fmt.Errorf("commit order create: %w", err)
	}
	return r.FindOwned(ctx, userID, uint64(orderID))
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
	rows, err := r.db.QueryContext(ctx, `SELECT id, sku_id, product_name, sku_code, sku_name, unit_price_cent, quantity, subtotal_cent FROM order_items WHERE order_id = ? ORDER BY id`, orderID)
	if err != nil {
		return order.Order{}, fmt.Errorf("find order items: %w", err)
	}
	defer rows.Close()
	found.Items = make([]order.Item, 0)
	for rows.Next() {
		var item order.Item
		if err := rows.Scan(&item.ID, &item.SKUID, &item.ProductName, &item.SKUCode, &item.SKUName, &item.UnitPriceCent, &item.Quantity, &item.SubtotalCent); err != nil {
			return order.Order{}, fmt.Errorf("scan order item: %w", err)
		}
		found.Items = append(found.Items, item)
	}
	if err := rows.Err(); err != nil {
		return order.Order{}, fmt.Errorf("iterate order items: %w", err)
	}
	return found, nil
}
