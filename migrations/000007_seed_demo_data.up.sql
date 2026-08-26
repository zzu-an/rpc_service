-- Development-only demo data. The DEMO-* identifiers make the matching down
-- migration precise and keep these rows easy to distinguish from real data.
START TRANSACTION;

-- Both demo users use the password: Demo123456!
INSERT INTO users (email, password_hash, status) VALUES
    ('demo.alice@example.com', '$2a$10$WZVW9p9oRY3.6kR9b1UlpuoxojzC/dJLG6B1QhO39xpQXwKUwxTuG', 1),
    ('demo.bob@example.com', '$2a$10$WZVW9p9oRY3.6kR9b1UlpuoxojzC/dJLG6B1QhO39xpQXwKUwxTuG', 1);

INSERT INTO user_roles (user_id, role_id)
SELECT u.id, r.id
FROM users u
JOIN roles r ON r.code = 'customer'
WHERE u.email IN ('demo.alice@example.com', 'demo.bob@example.com');

INSERT INTO products (name, description, status) VALUES
    ('Demo Mechanical Keyboard', 'Demo hot-swappable mechanical keyboard', 1);

INSERT INTO product_skus (product_id, code, name, price_cent, status)
SELECT p.id, seed.code, seed.name, seed.price_cent, 1
FROM (
    SELECT id
    FROM products
    WHERE name = 'Demo Mechanical Keyboard'
    ORDER BY id DESC
    LIMIT 1
) p
CROSS JOIN (
    SELECT 'DEMO-KB-BLACK' AS code, 'Black / Red Switch' AS name, 39900 AS price_cent
    UNION ALL
    SELECT 'DEMO-KB-WHITE', 'White / Brown Switch', 42900
) seed;

INSERT INTO products (name, description, status) VALUES
    ('Demo Wireless Mouse', 'Demo lightweight dual-mode wireless mouse', 1);

INSERT INTO product_skus (product_id, code, name, price_cent, status)
SELECT p.id, 'DEMO-MOUSE-BLACK', 'Black', 19900, 1
FROM (
    SELECT id
    FROM products
    WHERE name = 'Demo Wireless Mouse'
    ORDER BY id DESC
    LIMIT 1
) p;

INSERT INTO orders (order_no, user_id, status, total_amount_cent)
SELECT 'DEMO-ORDER-0001', u.id, 1, 59800
FROM users u
WHERE u.email = 'demo.alice@example.com';

INSERT INTO order_items (
    order_id,
    sku_id,
    product_name,
    sku_code,
    sku_name,
    unit_price_cent,
    quantity,
    subtotal_cent
)
SELECT
    o.id,
    s.id,
    p.name,
    s.code,
    s.name,
    s.price_cent,
    1,
    s.price_cent
FROM orders o
JOIN product_skus s ON s.code IN ('DEMO-KB-BLACK', 'DEMO-MOUSE-BLACK')
JOIN products p ON p.id = s.product_id
WHERE o.order_no = 'DEMO-ORDER-0001';

INSERT INTO seckill_activities (name, start_at, end_at, status) VALUES
    ('DEMO Long-running Seckill', '2020-01-01 00:00:00.000000', '2099-12-31 23:59:59.999999', 1);

INSERT INTO seckill_items (activity_id, sku_id, initial_stock, available_stock)
SELECT a.id, s.id, 100, 100
FROM (
    SELECT id
    FROM seckill_activities
    WHERE name = 'DEMO Long-running Seckill'
    ORDER BY id DESC
    LIMIT 1
) a
JOIN product_skus s ON s.code = 'DEMO-KB-BLACK';

COMMIT;
