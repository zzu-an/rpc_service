-- 回滚前置条件：所有外部 ID 仍能在 users/product_skus/seckill_activities/orders 中找到，
-- 且新增 reservation 已核对并可由旧 claim 完整表达。下面先恢复 FK；若存在悬空引用，MySQL
-- 会拒绝回滚并保留 reservation/snapshot，不能用清表绕过数据核对。
ALTER TABLE orders
    ADD CONSTRAINT fk_orders_user FOREIGN KEY (user_id) REFERENCES users (id);
ALTER TABLE order_items
    ADD CONSTRAINT fk_order_items_sku FOREIGN KEY (sku_id) REFERENCES product_skus (id);
ALTER TABLE seckill_items
    ADD CONSTRAINT fk_seckill_items_activity FOREIGN KEY (activity_id) REFERENCES seckill_activities (id) ON DELETE CASCADE,
    ADD CONSTRAINT fk_seckill_items_sku FOREIGN KEY (sku_id) REFERENCES product_skus (id);
ALTER TABLE seckill_order_claims
    ADD CONSTRAINT fk_seckill_claim_activity FOREIGN KEY (activity_id) REFERENCES seckill_activities (id),
    ADD CONSTRAINT fk_seckill_claim_item FOREIGN KEY (seckill_item_id) REFERENCES seckill_items (id),
    ADD CONSTRAINT fk_seckill_claim_user FOREIGN KEY (user_id) REFERENCES users (id),
    ADD CONSTRAINT fk_seckill_claim_order FOREIGN KEY (order_id) REFERENCES orders (id);

DROP TABLE seckill_inventory_reservations;

ALTER TABLE seckill_items
    DROP CHECK chk_seckill_items_snapshot_price,
    DROP COLUMN unit_price_cent,
    DROP COLUMN sku_name,
    DROP COLUMN sku_code,
    DROP COLUMN product_name;
