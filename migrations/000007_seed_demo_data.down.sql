-- Remove children before parents so every foreign-key constraint remains
-- enabled throughout rollback.
START TRANSACTION;

DELETE FROM orders
WHERE order_no = 'DEMO-ORDER-0001';

DELETE a
FROM seckill_activities a
JOIN seckill_items i ON i.activity_id = a.id
JOIN product_skus s ON s.id = i.sku_id
WHERE a.name = 'DEMO Long-running Seckill'
  AND s.code = 'DEMO-KB-BLACK';

DELETE p
FROM products p
JOIN product_skus s ON s.product_id = p.id
WHERE s.code IN ('DEMO-KB-BLACK', 'DEMO-KB-WHITE', 'DEMO-MOUSE-BLACK');

DELETE FROM users
WHERE email IN ('demo.alice@example.com', 'demo.bob@example.com');

COMMIT;
