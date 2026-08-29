-- seckill_order_jobs 记录“服务已经接受、但订单可能尚未创建”的异步请求。
-- 它不是订单事实表：最终成功仍由 orders + seckill_order_claims 判断。单独建表的原因是
-- Kafka 暂时不可用时不能让已返回 202 的任务只存在进程内存中。
CREATE TABLE seckill_order_jobs (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    event_id VARCHAR(128) NOT NULL,
    order_no VARCHAR(64) NOT NULL,
    user_id BIGINT UNSIGNED NOT NULL,
    seckill_item_id BIGINT UNSIGNED NOT NULL,
    reserved_at DATETIME(6) NOT NULL,
    payload JSON NOT NULL,
    status TINYINT UNSIGNED NOT NULL DEFAULT 1
        COMMENT '1=pending_publish, 2=published, 3=succeeded, 4=failed',
    publish_attempts INT UNSIGNED NOT NULL DEFAULT 0,
    consume_attempts INT UNSIGNED NOT NULL DEFAULT 0,
    next_publish_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    last_error_code VARCHAR(64) NOT NULL DEFAULT '',
    published_at DATETIME(6) NULL,
    completed_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_seckill_jobs_event_id (event_id),
    UNIQUE KEY uk_seckill_jobs_order_no (order_no),
    KEY idx_seckill_jobs_dispatch (status, next_publish_at, id),
    KEY idx_seckill_jobs_user_order (user_id, order_no),
    CONSTRAINT chk_seckill_jobs_status CHECK (status IN (1, 2, 3, 4)),
    CONSTRAINT chk_seckill_jobs_error_code CHECK (CHAR_LENGTH(last_error_code) <= 64),
    CONSTRAINT fk_seckill_jobs_user FOREIGN KEY (user_id) REFERENCES users (id),
    CONSTRAINT fk_seckill_jobs_item FOREIGN KEY (seckill_item_id) REFERENCES seckill_items (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
