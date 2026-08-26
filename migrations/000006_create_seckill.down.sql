DELETE rp
FROM role_permissions rp
JOIN permissions p ON p.id = rp.permission_id
WHERE p.code = 'seckill:write';

DELETE FROM permissions WHERE code = 'seckill:write';

DROP TABLE IF EXISTS seckill_order_claims;
DROP TABLE IF EXISTS seckill_items;
DROP TABLE IF EXISTS seckill_activities;
