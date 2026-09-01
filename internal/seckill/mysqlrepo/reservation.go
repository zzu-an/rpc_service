package mysqlrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"service_rpc/internal/seckill"
)

// ReservationRepository 只访问 inventory-rpc 拥有的 seckill_items 与 reservation 表。
// 它不 JOIN users/products/orders；同一物理库不代表可以绕过服务所有权。
type ReservationRepository struct {
	db *sql.DB
	// afterCommit 仅供同包测试模拟“数据库已提交但响应丢失”。生产为 nil。
	afterCommit func() error
}

func NewReservationRepository(db *sql.DB) *ReservationRepository {
	return &ReservationRepository{db: db}
}

func (r *ReservationRepository) ReserveSeckillStock(ctx context.Context, input seckill.InventoryReservationInput) (seckill.InventoryReservation, error) {
	if existing, found, err := r.findByOrderNo(ctx, input.OrderNo); err != nil {
		return seckill.InventoryReservation{}, err
	} else if found {
		return replayOrder(existing, input)
	}

	created, err := r.reserveOnce(ctx, input)
	if err == nil {
		return created, nil
	}
	if !isDuplicateKey(err) && !errors.Is(err, seckill.ErrOutOfStock) {
		return seckill.InventoryReservation{}, err
	}

	// order_no 唯一键解决“同一个业务请求是否已提交”；载荷不同必须冲突，不能把错误请求伪装成幂等成功。
	if winner, found, findErr := r.findByOrderNo(ctx, input.OrderNo); findErr != nil {
		return seckill.InventoryReservation{}, findErr
	} else if found {
		return replayOrder(winner, input)
	}
	// user-item 唯一键解决“同一用户换了 order_no 再抢一次”。返回赢家让上游继续使用首次业务键，
	// 失败事务中的库存扣减会随 Rollback 撤销，因此这里不会重复减库存。
	winner, found, findErr := r.findByUserItem(ctx, input.ActivityID, input.ItemID, input.UserID)
	if findErr != nil {
		return seckill.InventoryReservation{}, findErr
	}
	if !found {
		if errors.Is(err, seckill.ErrOutOfStock) {
			return seckill.InventoryReservation{}, seckill.ErrOutOfStock
		}
		return seckill.InventoryReservation{}, fmt.Errorf("duplicate reservation has no committed winner")
	}
	winner.Replayed = true
	return winner, nil
}

func (r *ReservationRepository) reserveOnce(ctx context.Context, input seckill.InventoryReservationInput) (seckill.InventoryReservation, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return seckill.InventoryReservation{}, fmt.Errorf("begin inventory reservation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var snapshot seckill.FrozenSKUSnapshot
	err = tx.QueryRowContext(ctx, `
		SELECT sku_id, product_name, sku_code, sku_name, unit_price_cent
		FROM seckill_items
		WHERE id = ? AND activity_id = ?
	`, input.ItemID, input.ActivityID).Scan(
		&snapshot.SKUID, &snapshot.ProductName, &snapshot.SKUCode, &snapshot.SKUName, &snapshot.UnitPriceCent,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return seckill.InventoryReservation{}, seckill.ErrItemNotFound
	}
	if err != nil {
		return seckill.InventoryReservation{}, fmt.Errorf("load inventory snapshot: %w", err)
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE seckill_items
		SET available_stock = available_stock - 1, version = version + 1
		WHERE id = ? AND activity_id = ? AND available_stock > 0
	`, input.ItemID, input.ActivityID)
	if err != nil {
		return seckill.InventoryReservation{}, fmt.Errorf("decrement inventory stock: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return seckill.InventoryReservation{}, fmt.Errorf("read inventory stock update count: %w", err)
	}
	if rows == 0 {
		return seckill.InventoryReservation{}, seckill.ErrOutOfStock
	}

	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	reservationResult, err := tx.ExecContext(ctx, `
		INSERT INTO seckill_inventory_reservations (
			order_no, activity_id, seckill_item_id, user_id,
			product_name, sku_code, sku_name, unit_price_cent, reserved_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.OrderNo, input.ActivityID, input.ItemID, input.UserID,
		snapshot.ProductName, snapshot.SKUCode, snapshot.SKUName, snapshot.UnitPriceCent,
		input.ReservedAt, createdAt,
	)
	if err != nil {
		// INSERT 唯一冲突前的库存 UPDATE 仍在同一事务，defer Rollback 会撤销它。
		return seckill.InventoryReservation{}, err
	}
	id, err := reservationResult.LastInsertId()
	if err != nil {
		return seckill.InventoryReservation{}, fmt.Errorf("read reservation ID: %w", err)
	}
	if err := tx.Commit(); err != nil {
		// Commit 报错不能证明未提交。唯一安全恢复方式是用相同 order_no 重试查询事实，禁止盲目加库存。
		return seckill.InventoryReservation{}, fmt.Errorf("commit inventory reservation: %w", err)
	}
	if r.afterCommit != nil {
		if err := r.afterCommit(); err != nil {
			return seckill.InventoryReservation{}, fmt.Errorf("return inventory reservation result: %w", err)
		}
	}
	return seckill.InventoryReservation{
		ID: uint64(id), OrderNo: input.OrderNo, ActivityID: input.ActivityID, ItemID: input.ItemID,
		UserID: input.UserID, Snapshot: snapshot, ReservedAt: input.ReservedAt,
	}, nil
}

func replayOrder(existing seckill.InventoryReservation, input seckill.InventoryReservationInput) (seckill.InventoryReservation, error) {
	if existing.ActivityID != input.ActivityID || existing.ItemID != input.ItemID || existing.UserID != input.UserID || !existing.ReservedAt.Equal(input.ReservedAt) {
		return seckill.InventoryReservation{}, seckill.ErrConflict
	}
	existing.Replayed = true
	return existing, nil
}

func (r *ReservationRepository) findByOrderNo(ctx context.Context, orderNo string) (seckill.InventoryReservation, bool, error) {
	return r.findOne(ctx, `
		SELECT id, order_no, activity_id, seckill_item_id, user_id,
		       product_name, sku_code, sku_name, unit_price_cent, reserved_at
		FROM seckill_inventory_reservations WHERE order_no = ?
	`, orderNo)
}

func (r *ReservationRepository) findByUserItem(ctx context.Context, activityID, itemID, userID uint64) (seckill.InventoryReservation, bool, error) {
	return r.findOne(ctx, `
		SELECT id, order_no, activity_id, seckill_item_id, user_id,
		       product_name, sku_code, sku_name, unit_price_cent, reserved_at
		FROM seckill_inventory_reservations
		WHERE activity_id = ? AND seckill_item_id = ? AND user_id = ?
	`, activityID, itemID, userID)
}

func (r *ReservationRepository) findOne(ctx context.Context, query string, args ...any) (seckill.InventoryReservation, bool, error) {
	var value seckill.InventoryReservation
	err := r.db.QueryRowContext(ctx, query, args...).Scan(
		&value.ID, &value.OrderNo, &value.ActivityID, &value.ItemID, &value.UserID,
		&value.Snapshot.ProductName, &value.Snapshot.SKUCode, &value.Snapshot.SKUName,
		&value.Snapshot.UnitPriceCent, &value.ReservedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return seckill.InventoryReservation{}, false, nil
	}
	if err != nil {
		return seckill.InventoryReservation{}, false, fmt.Errorf("find inventory reservation: %w", err)
	}
	// sku_id 来自 inventory 自有 item；查询时补齐值对象，不跨到 product 表。
	if err := r.db.QueryRowContext(ctx, "SELECT sku_id FROM seckill_items WHERE id = ?", value.ItemID).Scan(&value.Snapshot.SKUID); err != nil {
		return seckill.InventoryReservation{}, false, fmt.Errorf("load reservation SKU ID: %w", err)
	}
	value.ReservedAt = value.ReservedAt.UTC().Truncate(time.Millisecond)
	return value, true, nil
}
