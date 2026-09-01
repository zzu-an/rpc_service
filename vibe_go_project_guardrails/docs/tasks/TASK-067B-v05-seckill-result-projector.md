# TASK-067B: 秒杀结果投影消费者

## 背景
订单成功后不能同步反向依赖 seckill-rpc；结果应由订单事件异步投影到 Redis。

## 目标
实现独立 consumer group，将 seckill 来源的 order.created 幂等投影为 Redis SUCCEEDED。

## 非目标
- 不处理普通订单通知，不修改订单或库存。
- 不实现补偿/对账。

## 允许修改
- `cmd/seckill-result-projector/*`
- seckill result projection adapter
- 结果查询组合逻辑与测试

## 禁止修改
- order/inventory 核心事务、notification repository。

## 实现约束
- 只处理 order_source=SECKILL；以 event_id/order_no 幂等。
- 与 notification 使用不同 group，各自提交 offset/retry/DLQ。
- 查询始终先问 order-rpc；订单存在优先于 Redis QUEUED/FAILED。
- 中文注释解释订单事实与 Redis 投影优先级、独立 group 和提交后重投窗口。

## 验收标准
- [x] 同一 order.created 同时可被 projector 和 notification group 消费。
- [x] 重复事件 100 次只保留一个成功投影。
- [x] projector 停止不影响 notification 和订单 API，恢复后 lag 可排空。
- [x] 越权/不存在 order_no 的结果查询行为保持。

## 验证命令
```bash
go test -race ./internal/seckill/... ./cmd/seckill-result-projector/...
```

## 回滚点
停止 projector 并保留 Kafka lag；结果查询仍以订单事实优先。

## 完成记录（2026-08-29）

- `seckill-result-projector` 只持有 Kafka 与 Redis；普通订单事件直接提交，秒杀事件用 Lua 原子更新 `SUCCEEDED` 与 event ledger。
- 投影前校验 Stream order_no 和 Redis 中的 user 所有权；冲突作为 poison event 进入 DLQ，不能覆盖另一用户的 QUEUED 结果。
- Redis 结果已过期时不重建缓存，只记录消费 ledger；gateway 仍先查 order-rpc 事实，因此缓存缺失不等于订单不存在。
- 真实 MySQL 8.4 + Redis 8 + Kafka 4.3 验证：notification group 先独立排空 100 条重复事件时 projector 保持 QUEUED；随后另一个 group 排空自己的 lag，最终通知为 1、投影 ledger 为 1、状态为 SUCCEEDED。
- race、vet、Redis-only 生产边界扫描和 `git diff --check` 已通过。
