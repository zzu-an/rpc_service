# TASK-046: v0.4 阶段验收与收口

## 目标

用单一门禁证明异步削峰、至少一次幂等、失败恢复、backlog 和阶段隔离。

## 非目标

- 不在验收任务补做大范围业务功能。

## 允许修改

- `Makefile`
- `internal/seckill/mq/stage_v04_test.go`（新增）
- `docs/v0.4-acceptance.md`（新增）
- 本 TASK 文档

## 实现约束

- `verify-v04` 强制显式 MySQL/Redis/Kafka，不能静默 skip。
- 至少覆盖 1000/100、同用户 100、重复 100、Kafka 故障恢复、consumer backlog/recovery 和 poison→DLQ。
- 执行全量 race、vet、migration up/down/up、边界扫描和 diff check。
- 注释质量按 Spec 清单人工审查，不能只统计行数。

## 验收标准

- [x] 每个返回 202 的请求最终 succeeded 或显式 failed，没有无记录丢失。
- [x] 不超卖、不重复、lag 可见可排空、故障可恢复。
- [x] 没有 RPC/etcd、支付状态机、自动对账、系统限流或完整观测平台。

## 验证命令

```bash
make verify-v04 TEST_DSN='...' TEST_REDIS_ADDR='...' TEST_KAFKA_BROKERS='192.168.0.107:9092'
git diff --check
```

## 回滚点

移除阶段门禁和报告；业务实现不变。

## 完成记录（2026-08-29）

- `make verify-v04` 使用 `192.168.0.107` 远程 MySQL/Redis/Kafka 通过。
- migration v8→v7→v8、全量 race、vet、边界扫描、diff check 均通过。
- 真实场景与验收中修复的问题记录在 `docs/v0.4-acceptance.md`。
