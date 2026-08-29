# ADR-003: v0.4 使用 Kafka 与持久化 Job 异步落单

## 状态

Accepted

## 背景

v0.3 已挡住无资格流量，但获得资格的请求仍在 HTTP 链路同步执行 MySQL Purchase。突发成功资格会同时占用 HTTP goroutine、数据库连接和热点事务。直接在 Lua 后发送 Kafka 又无法回答“Redis 已扣资格、broker 不可用或确认结果未知”时如何恢复。

## 决策

1. v0.4 只使用 Kafka 作为主 MQ，主链路不接入 RabbitMQ/RocketMQ。
2. Redis Reserve 后幂等写入 `seckill_order_jobs`；只有 job 提交成功才返回 HTTP 202。
3. 独立 worker 的 relay 发布 job，消费者以有界并发调用现有 MySQL Purchase。
4. 接受至少一次和重复消息，以稳定 order_no、job 唯一键、orders.order_no 与 claim 唯一键实现幂等。
5. 使用主/retry/DLQ topics；任何跨 topic 转交只有在目标发布确认后才能确认源消息。
6. 保留 sync 模式用于 v0.3 回归和性能对照，正式 v0.4 使用 async。

## 候选方案

### 方案 A：HTTP 直接同步发送 Kafka

优点：代码和 schema 最少。

缺点：Kafka 故障直接拖累 HTTP；发送失败或结果未知只能依赖客户端重试，无法承诺返回 202 后任务仍可恢复。

### 方案 B：Redis Stream 与 Lua 原子 XADD

优点：资格预扣与入队可在同一 Redis shard 原子执行，消除 Redis→队列窗口。

缺点：绕开本阶段希望学习的 Kafka partition、consumer group、offset 和 lag；Redis 同时承担准入与消息积压，故障域更集中。

### 方案 C：Kafka + MySQL 持久化 Job（选择）

优点：HTTP 接受结果可持久化；Kafka 故障后可续传；发送确认与状态更新之间的重复可由幂等消费安全吸收；可以测量真实 backlog。

缺点：增加一张表和 relay；Redis Reserve 与 job 插入仍非原子，可能保守少卖；总 SQL 数不会必然下降。

### 方案 D：Kafka 事务或分布式事务

优点：在有限边界内提供更强事务语义。

缺点：Kafka 事务仍不能与 Redis Lua 和 MySQL 业务事务组成简单的全局原子提交；引入分布式事务超出 v0.4 学习边界。

## 为什么选择当前方案

- 当前瓶颈是同步重事务峰值，而不是跨服务一致性；持久化 job + 有界消费直接解决该问题。
- go-zero 有 Kafka kq 组件，符合项目技术主线。
- 方案公开保留至少一次和残余非原子窗口，适合学习真实消息可靠性，不制造 exactly-once 幻觉。
- 单体 API 和 worker 共享领域/仓储，不提前拆微服务或引入 RPC。

## 代价

- 新增 Kafka 运维依赖、topic 配置、job 表和 worker 生命周期。
- broker ack 后状态更新前宕机会重复发布；数据库提交后 offset 前宕机会重复消费。
- Redis 成功而 job 插入失败且用户永不重试时仍可能少卖；自动对账留给 v0.6。
- retry/DLQ 需要明确错误分类和转交确认顺序。

## 验证方式

- Kafka 停止时 job 保持 pending，恢复后继续投递。
- 注入发送成功后状态更新失败，证明重复消息只产生一个订单。
- 注入 MySQL 提交后 consumer 失败，证明重投只返回原订单。
- 1000/100 并发和同用户 100 并发验证不超卖、不重复。
- 停止消费者制造 lag，恢复后排空并记录 MySQL Purchase 峰值并发。

## 回滚策略

- 将 `Seckill.OrderMode` 切回 `sync`，停止 worker；v0.3 同步链路仍可运行。
- 保留 job 表用于审计，确认无待处理记录后再通过 migration 回滚。
- 不删除 Redis buyer 或自动增加资格，避免回滚过程制造超卖窗口。
