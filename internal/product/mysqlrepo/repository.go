// Package mysqlrepo implements product persistence with MySQL.
package mysqlrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	gomysql "github.com/go-sql-driver/mysql"

	"service_rpc/internal/product"
)

type Repository struct{ db *sql.DB }

func New(db *sql.DB) *Repository { return &Repository{db: db} }

func (r *Repository) Create(ctx context.Context, input product.CreateInput) (product.Product, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return product.Product{}, fmt.Errorf("begin product create: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO products (name, description, status) VALUES (?, ?, ?)`, input.Name, input.Description, product.StatusInactive)
	if err != nil {
		return product.Product{}, mapConflict("insert product", err)
	}
	productID, err := result.LastInsertId()
	if err != nil {
		return product.Product{}, fmt.Errorf("read product ID: %w", err)
	}
	createdSKUs := make([]product.SKU, 0, len(input.SKUs))
	for _, sku := range input.SKUs {
		result, err := tx.ExecContext(ctx, `INSERT INTO product_skus (product_id, code, name, price_cent, status) VALUES (?, ?, ?, ?, ?)`, productID, sku.Code, sku.Name, sku.PriceCent, product.StatusActive)
		if err != nil {
			return product.Product{}, mapConflict("insert product SKU", err)
		}
		skuID, err := result.LastInsertId()
		if err != nil {
			return product.Product{}, fmt.Errorf("read SKU ID: %w", err)
		}
		sku.ID = uint64(skuID)
		createdSKUs = append(createdSKUs, sku)
	}
	if err := tx.Commit(); err != nil {
		return product.Product{}, fmt.Errorf("commit product create: %w", err)
	}
	return product.Product{ID: uint64(productID), Name: input.Name, Description: input.Description, Status: product.StatusInactive, SKUs: createdSKUs, CreatedAt: time.Now().UTC()}, nil
}

func mapConflict(operation string, err error) error {
	var mysqlErr *gomysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return product.ErrProductConflict
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func (r *Repository) Update(ctx context.Context, id uint64, name, description string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE products SET name = ?, description = ? WHERE id = ?`, name, description, id)
	if err != nil {
		return fmt.Errorf("update product: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read update count: %w", err)
	}
	if rows == 0 {
		return product.ErrProductNotFound
	}
	return nil
}

func (r *Repository) SetStatus(ctx context.Context, id uint64, status uint8) error {
	result, err := r.db.ExecContext(ctx, `UPDATE products SET status = ? WHERE id = ?`, status, id)
	if err != nil {
		return fmt.Errorf("set product status: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read status count: %w", err)
	}
	if rows == 0 {
		return product.ErrProductNotFound
	}
	return nil
}

func (r *Repository) ListActive(ctx context.Context, offset, limit int) ([]product.Product, int64, error) {
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM products WHERE status = ?`, product.StatusActive).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count active products: %w", err)
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, name, description, status, created_at FROM products WHERE status = ? ORDER BY id DESC LIMIT ? OFFSET ?`, product.StatusActive, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list active products: %w", err)
	}
	defer rows.Close()
	items := make([]product.Product, 0)
	for rows.Next() {
		var item product.Product
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.Status, &item.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan product: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate products: %w", err)
	}
	return items, total, nil
}

func (r *Repository) FindActive(ctx context.Context, id uint64) (product.Product, error) {
	var item product.Product
	err := r.db.QueryRowContext(ctx, `SELECT id, name, description, status, created_at FROM products WHERE id = ? AND status = ?`, id, product.StatusActive).Scan(&item.ID, &item.Name, &item.Description, &item.Status, &item.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return product.Product{}, product.ErrProductNotFound
	}
	if err != nil {
		return product.Product{}, fmt.Errorf("find active product: %w", err)
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, code, name, price_cent, status FROM product_skus WHERE product_id = ? AND status = ? ORDER BY id`, id, product.StatusActive)
	if err != nil {
		return product.Product{}, fmt.Errorf("find product SKUs: %w", err)
	}
	defer rows.Close()
	item.SKUs = make([]product.SKU, 0)
	for rows.Next() {
		var sku product.SKU
		if err := rows.Scan(&sku.ID, &sku.Code, &sku.Name, &sku.PriceCent, &sku.Status); err != nil {
			return product.Product{}, fmt.Errorf("scan SKU: %w", err)
		}
		item.SKUs = append(item.SKUs, sku)
	}
	if err := rows.Err(); err != nil {
		return product.Product{}, fmt.Errorf("iterate SKUs: %w", err)
	}
	return item, nil
}

func (r *Repository) FindActiveSKU(ctx context.Context, skuID uint64) (product.SKUSnapshot, error) {
	var snapshot product.SKUSnapshot
	err := r.db.QueryRowContext(ctx, `
		SELECT s.id, p.id, p.name, s.code, s.name, s.price_cent, p.status, s.status
		FROM product_skus s
		JOIN products p ON p.id = s.product_id
		WHERE s.id = ? AND s.status = ? AND p.status = ?
	`, skuID, product.StatusActive, product.StatusActive).Scan(
		&snapshot.SKUID, &snapshot.ProductID, &snapshot.ProductName,
		&snapshot.SKUCode, &snapshot.SKUName, &snapshot.PriceCent,
		&snapshot.ProductStatus, &snapshot.SKUStatus,
	)
	if errors.Is(err, sql.ErrNoRows) {
		// 对调用方统一表现为不可用于新业务；不泄漏是 SKU 停用、商品停用还是 ID 不存在。
		return product.SKUSnapshot{}, product.ErrSKUNotFound
	}
	if err != nil {
		return product.SKUSnapshot{}, fmt.Errorf("find active SKU snapshot: %w", err)
	}
	return snapshot, nil
}
