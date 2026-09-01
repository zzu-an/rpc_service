# TASK-066: Stream RPC 编排 worker

## 背景
v0.4.2 Stream worker 直接调用本地 Purchase；微服务化后必须通过服务公开契约落单。

## 目标
实现 seckill-orchestrator：消费内部 Redis Stream，幂等调用 inventory-rpc 与 order-rpc，成功后 ACK。

## 非目标
- 不经过 Kafka，不写其他服务数据库。
- 不自动释放 reservation，不做 Saga/TCC/对账。

## 允许修改
- `cmd/seckill-orchestrator/*`
- `internal/seckill/streamqueue/*`
- orchestrator 配置与测试

## 禁止修改
- inventory/order repository、Kafka Outbox/consumer。

## 实现约束
- XREADGROUP 读取新消息，XAUTOCLAIM 恢复 PEL。
- 单 task 总 deadline 分配给 inventory/order 两跳；两个写 RPC 必须使用稳定 order_no。
- 两步成功才 XACK+XDEL；临时依赖错误保留 PEL，坏消息进 Stream DLQ。
- inventory 成功/order 失败记录 reserved_without_order，不盲目回补。
- 中文注释解释为什么 RPC 是必要变化、Kafka不是，以及本地事务分裂后的失败窗口。

## 验收标准
- [x] 重复 task 100 次只产生一笔 reservation/order。
- [x] order 提交后、XACK 前崩溃，reclaim 返回原订单。
- [x] order-rpc 停止/超时时 worker 有界返回，PEL 可恢复。
- [x] orchestrator 不导入其他服务 repository 或 Kafka publisher。

## 验证命令
```bash
go test -race ./internal/seckill/streamqueue/... ./cmd/seckill-orchestrator/...
make verify-stream-rpc-v05
```

## 回滚点
停止 orchestrator 并保留 PEL；不得与旧直写 SQL worker 同时运行。

## 完成记录（2026-08-29）

- 新入口 `cmd/seckill-orchestrator` 只装配 Redis、seckill/inventory/order RPC，不持有 MySQL 或 Kafka client。
- Stream 消息补齐 `activity_id`；seckill-rpc 增加 item discovery 契约，避免微服务入口回头读取 seckill 数据库。
- inventory 与 order 两跳各自拥有子 deadline；永久业务错误直接 DLQ，临时错误与 `reserved_without_order` 留在 PEL 等待安全重试。
- `make verify-stream-rpc-v05` 已在临时 MySQL 8.4 + Redis 8 上通过：100 条相同任务、首次订单提交后不 ACK、XAUTOCLAIM 重放，最终 reservation/order/outbox 均严格为 1。
- test-verifier：race、vet、生产边界扫描与 `git diff --check` 全部通过。
