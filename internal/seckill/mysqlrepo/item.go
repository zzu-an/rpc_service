package mysqlrepo

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"service_rpc/internal/seckill"
)

type InventoryItemRepository struct{ db *sql.DB }

func NewInventoryItemRepository(db *sql.DB) *InventoryItemRepository {
	return &InventoryItemRepository{db: db}
}

func (r *InventoryItemRepository) CreateInventoryItem(ctx context.Context, input seckill.CreateInventoryItemInput, snapshot seckill.FrozenSKUSnapshot) (seckill.InventoryItem, error) {
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO seckill_items (
			activity_id, sku_id, product_name, sku_code, sku_name, unit_price_cent,
			initial_stock, available_stock, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.ActivityID, snapshot.SKUID, snapshot.ProductName, snapshot.SKUCode, snapshot.SKUName,
		snapshot.UnitPriceCent, input.Stock, input.Stock, createdAt)
	if err != nil {
		return seckill.InventoryItem{}, mapConflict("insert inventory item", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return seckill.InventoryItem{}, fmt.Errorf("read inventory item ID: %w", err)
	}
	return seckill.InventoryItem{
		ID: uint64(id), ActivityID: input.ActivityID, Snapshot: snapshot,
		InitialStock: input.Stock, AvailableStock: input.Stock, CreatedAt: createdAt,
	}, nil
}

func (r *InventoryItemRepository) ListActivityItems(ctx context.Context, activityID uint64) ([]seckill.InventoryItem, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, activity_id, sku_id, product_name, sku_code, sku_name, unit_price_cent,
		       initial_stock, available_stock, version, created_at
		FROM seckill_items WHERE activity_id = ? ORDER BY id
	`, activityID)
	if err != nil {
		return nil, fmt.Errorf("list inventory activity items: %w", err)
	}
	defer rows.Close()
	items := make([]seckill.InventoryItem, 0)
	for rows.Next() {
		var item seckill.InventoryItem
		if err := rows.Scan(
			&item.ID, &item.ActivityID, &item.Snapshot.SKUID, &item.Snapshot.ProductName,
			&item.Snapshot.SKUCode, &item.Snapshot.SKUName, &item.Snapshot.UnitPriceCent,
			&item.InitialStock, &item.AvailableStock, &item.Version, &item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan inventory activity item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate inventory activity items: %w", err)
	}
	return items, nil
}
