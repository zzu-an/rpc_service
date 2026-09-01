-- v0.5 先在同一 MySQL 实例建立逻辑所有权：删除跨服务 FK 不等于放弃校验，
-- 而是把“外部 ID 是否存在”交给 RPC/幂等键验证，避免一个服务的 DDL/删除被另一个服务锁死。
ALTER TABLE seckill_items
    ADD COLUMN product_name VARCHAR(200) NULL AFTER sku_id,
    ADD COLUMN sku_code VARCHAR(100) NULL AFTER product_name,
    ADD COLUMN sku_name VARCHAR(200) NULL AFTER sku_code,
    ADD COLUMN unit_price_cent BIGINT NULL AFTER sku_name;

-- 先用当前商品事实回填所有 item；若存在悬空 SKU，后续 NOT NULL 会让 migration fail closed。
UPDATE seckill_items i
JOIN product_skus s ON s.id = i.sku_id
JOIN products p ON p.id = s.product_id
SET i.product_name = p.name,
    i.sku_code = s.code,
    i.sku_name = s.name,
    i.unit_price_cent = s.price_cent;

ALTER TABLE seckill_items
    MODIFY COLUMN product_name VARCHAR(200) NOT NULL COMMENT '创建 item 时冻结的商品名；不随 product-rpc 后续修改',
    MODIFY COLUMN sku_code VARCHAR(100) NOT NULL COMMENT '创建 item 时冻结的 SKU 编码',
    MODIFY COLUMN sku_name VARCHAR(200) NOT NULL COMMENT '创建 item 时冻结的 SKU 名称',
    MODIFY COLUMN unit_price_cent BIGINT NOT NULL COMMENT '整数分冻结价，禁止浮点金额',
    ADD CONSTRAINT chk_seckill_items_snapshot_price CHECK (unit_price_cent >= 0);

CREATE TABLE seckill_inventory_reservations (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    order_no VARCHAR(64) NOT NULL,
    activity_id BIGINT UNSIGNED NOT NULL COMMENT '外部 seckill-service 引用，不创建跨服务 FK',
    seckill_item_id BIGINT UNSIGNED NOT NULL,
    user_id BIGINT UNSIGNED NOT NULL COMMENT '外部 user-service 引用，由 RPC 身份与业务校验负责',
    product_name VARCHAR(200) NOT NULL,
    sku_code VARCHAR(100) NOT NULL,
    sku_name VARCHAR(200) NOT NULL,
    unit_price_cent BIGINT NOT NULL,
    reserved_at DATETIME(3) NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_inventory_reservation_order_no (order_no),
    UNIQUE KEY uk_inventory_reservation_user_item (activity_id, seckill_item_id, user_id),
    KEY idx_inventory_reservation_item_created (seckill_item_id, created_at),
    CONSTRAINT chk_inventory_reservation_price CHECK (unit_price_cent >= 0),
    -- reservation 与 seckill_items 同属 inventory-rpc，服务内 FK 继续保留。
    CONSTRAINT fk_inventory_reservation_item FOREIGN KEY (seckill_item_id) REFERENCES seckill_items (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 历史 claim 通过 order/order_items 读取当时真正落单的不可变快照，而不是拿当前商品价格猜测。
-- 一个订单理论上只含该秒杀 SKU；若历史数据缺少对应 item，本 INSERT 少行，阶段测试会让计数不等时失败。
INSERT INTO seckill_inventory_reservations (
    order_no, activity_id, seckill_item_id, user_id,
    product_name, sku_code, sku_name, unit_price_cent, reserved_at, created_at
)
SELECT
    o.order_no, c.activity_id, c.seckill_item_id, c.user_id,
    oi.product_name, oi.sku_code, oi.sku_name, oi.unit_price_cent,
    c.created_at, c.created_at
FROM seckill_order_claims c
JOIN orders o ON o.id = c.order_id
JOIN seckill_items i ON i.id = c.seckill_item_id
JOIN order_items oi ON oi.order_id = o.id AND oi.sku_id = i.sku_id;

-- 以下 FK 均跨越 ADR-005 的服务所有权。外部 ID 仍保留索引/唯一键和应用校验；
-- inventory 成功、order 失败的残余窗口不会靠 FK 解决，v0.5 只记录 reserved_without_order。
ALTER TABLE orders DROP FOREIGN KEY fk_orders_user;
ALTER TABLE order_items DROP FOREIGN KEY fk_order_items_sku;
ALTER TABLE seckill_items DROP FOREIGN KEY fk_seckill_items_activity;
ALTER TABLE seckill_items DROP FOREIGN KEY fk_seckill_items_sku;
ALTER TABLE seckill_order_claims DROP FOREIGN KEY fk_seckill_claim_activity;
ALTER TABLE seckill_order_claims DROP FOREIGN KEY fk_seckill_claim_item;
ALTER TABLE seckill_order_claims DROP FOREIGN KEY fk_seckill_claim_user;
ALTER TABLE seckill_order_claims DROP FOREIGN KEY fk_seckill_claim_order;
