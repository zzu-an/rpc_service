CREATE TABLE seckill_activities (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    name VARCHAR(200) NOT NULL,
    start_at DATETIME(6) NOT NULL,
    end_at DATETIME(6) NOT NULL,
    status TINYINT UNSIGNED NOT NULL DEFAULT 2 COMMENT '1=enabled, 2=disabled',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY idx_seckill_activities_status_time (status, start_at, end_at),
    CONSTRAINT chk_seckill_activities_time CHECK (end_at > start_at),
    CONSTRAINT chk_seckill_activities_status CHECK (status IN (1, 2))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE seckill_items (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    activity_id BIGINT UNSIGNED NOT NULL,
    sku_id BIGINT UNSIGNED NOT NULL,
    initial_stock BIGINT NOT NULL,
    available_stock BIGINT NOT NULL,
    version BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'Optimistic-lock CAS version',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_seckill_items_activity_sku (activity_id, sku_id),
    KEY idx_seckill_items_sku (sku_id),
    CONSTRAINT chk_seckill_items_initial_stock CHECK (initial_stock >= 0),
    CONSTRAINT chk_seckill_items_available_stock CHECK (available_stock >= 0 AND available_stock <= initial_stock),
    CONSTRAINT fk_seckill_items_activity FOREIGN KEY (activity_id) REFERENCES seckill_activities (id) ON DELETE CASCADE,
    CONSTRAINT fk_seckill_items_sku FOREIGN KEY (sku_id) REFERENCES product_skus (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE seckill_order_claims (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    activity_id BIGINT UNSIGNED NOT NULL,
    seckill_item_id BIGINT UNSIGNED NOT NULL,
    user_id BIGINT UNSIGNED NOT NULL,
    order_id BIGINT UNSIGNED NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_seckill_claim_user_item (activity_id, seckill_item_id, user_id),
    UNIQUE KEY uk_seckill_claim_order (order_id),
    KEY idx_seckill_claim_user_created (user_id, created_at),
    CONSTRAINT fk_seckill_claim_activity FOREIGN KEY (activity_id) REFERENCES seckill_activities (id),
    CONSTRAINT fk_seckill_claim_item FOREIGN KEY (seckill_item_id) REFERENCES seckill_items (id),
    CONSTRAINT fk_seckill_claim_user FOREIGN KEY (user_id) REFERENCES users (id),
    CONSTRAINT fk_seckill_claim_order FOREIGN KEY (order_id) REFERENCES orders (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

INSERT INTO permissions (code, name)
VALUES ('seckill:write', 'Manage seckill activities');

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.code = 'seckill:write'
WHERE r.code = 'admin';
