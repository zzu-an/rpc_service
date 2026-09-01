-- 回滚只允许在 relay 已停止且没有待发布事件时执行；migration 工具无法替业务判断 backlog。
DROP TABLE IF EXISTS order_outbox_events;
