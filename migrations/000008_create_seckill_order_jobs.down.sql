-- 只回滚 v0.4 新增的投递表，不触碰订单、claim 或 Redis 资格。
-- 实际环境执行前必须先确认没有待处理 job，否则回滚会丢失“已接受但尚未落单”的任务。
DROP TABLE IF EXISTS seckill_order_jobs;
