# TASK-070: v0.5 阶段验收

## 背景
v0.5 同时改变进程、数据边界和消息路径，必须用真实依赖与边界扫描证明，而非只跑 mock 单测。

## 目标
建立 `make verify-v05`，完成 RPC、Stream orchestrator、Order Outbox/Kafka、notification 下游、
故障、migration、注释和阶段边界验收。

## 非目标
- 不在验收任务修复新功能或顺手重构。
- 不宣称固定 QPS 提升。

## 允许修改
- `Makefile`
- `cmd/v05check/*_test.go`
- `tests/*` 的 v0.5 验收用例
- `docs/v0.5-acceptance.md`
- `docs/v0.5-loadtest.md`

## 禁止修改
- 生产业务逻辑、schema、RPC/消息契约。

## 实现约束
- 缺少真实 MySQL/Redis/Kafka/etcd 参数必须失败，不 silent skip。
- 测试数据随机命名并精确清理；不得指向生产依赖。
- 验收必须含 1000/100、同用户 100 并发、重复 task/event 100 次、PEL/Outbox/双 group lag 恢复、
  RPC timeout/熔断。
- 日常拓扑必须启动 11 个应用进程+4 个基础设施实例；治理拓扑必须额外启动第二个 order-rpc 和
  第二个 notification-consumer，Kafka topic 至少 2 partitions。
- 人工审查 Spec 第 13 节关键中文注释，拒绝逐行翻译式注释。
- 边界扫描禁止跨服务 SQL、Stream→Kafka Bridge、旧 seckill job/outbox、补偿/对账、Saga/TCC、
  系统级限流和 v1.0 技术。

## 验收标准
- [x] `make verify-v05` 全部通过。
- [x] Redis 接受数、MySQL reservation/order 数均不超过库存且重复不增量。
- [x] gateway 不直连 DB；六个 RPC 只访问自有数据。
- [x] Stream、RPC、Order Outbox/Kafka 和 notification 隔离故障均有可复现报告。
- [x] order-rpc 双实例负载/摘除/滚动重启和 notification consumer rebalance 验证通过。
- [x] reserved_without_order 被明确演示，且无自动修复实现。
- [x] v0.5 Spec DoD 全部勾选，遗留只指向 v0.6。

## 验证命令
```bash
make verify-v05 \
  TEST_DSN='<remote-test-dsn>' \
  TEST_REDIS_ADDR='<remote-redis>' \
  TEST_KAFKA_BROKERS='<remote-kafka>' \
  TEST_ORDER_CREATED_TOPIC='<pre-created-disposable-topic>' \
  TEST_ETCD_HOSTS='<remote-etcd>'
```

## 回滚点
验收仅新增测试/报告；失败时停在 v0.5，不进入 v0.6。

## 完成记录（2026-08-29）

- 在空 MySQL 8.4.11 数据目录、Redis 8.10.1、Kafka 4.3.1 双分区测试 topic、etcd 3.7.1 上执行
  `make verify-v05`，IDL、v8/v9/v10 migration、真实异步链路、RPC 故障、生命周期、全仓 race/vet、
  七项架构边界扫描与 `git diff --check` 全部通过。
- 总门禁发现并修复两类真实回归：旧阶段测试夹具未填写 v8 冻结快照；Stream Lua 未在写副作用前校验
  `activity_id`，且未把该字段写入任务消息。新增测试证明不完整缓存状态失败时库存、buyer、Stream、
  result 均不改变。
- 修正并发测试对 goroutine 调度顺序的错误假设：记录真实抢购赢家后再验证幂等重放，避免把偶发绿/红
  当作 Redis 正确性结论。
- 实测 1000 请求/库存 100、同用户与同 task/event 100 次重放均由库存条件和唯一键收敛；Outbox、
  notification、projector、PEL、RPC timeout/breaker、服务发现、15/17 实例生命周期均有自动化证据。
- `reserved_without_order` 与订单库长期低于入口速率时的 backlog 只进入诊断和容量文档；没有实现自动
  补偿、对账、系统级限流、Saga/TCC 或 Stream→Kafka Bridge。
