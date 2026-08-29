# TASK-034: 新增异步落单 Job Schema

## 目标

用单张 `seckill_order_jobs` 表持久化已接受请求、冻结消息和投递结果。

## 非目标

- 不接 Kafka、不改 HTTP、不修改 orders.status。

## 允许修改

- `migrations/000008_create_seckill_order_jobs.up.sql`（新增）
- `migrations/000008_create_seckill_order_jobs.down.sql`（新增）
- migration 测试（最多新增 1 个）

## 实现约束

- event_id/order_no 唯一；状态、次数和错误分类有 CHECK。
- 扫描与用户查询有明确索引；down 只删除本表。

## 验收标准

- [x] up/down/up 可重复，原订单表含义不变。
- [x] 重复 event/order 被数据库拒绝。

## 验证命令

```bash
go test ./cmd/migrate ./internal/seckill/mysqlrepo
git diff --check
```

## 回滚点

执行 v8 down，仅删除尚无生产调用方的 job 表。
