# ADR-006: v0.5 将 Kafka 用作跨服务领域事件总线

## 状态

Accepted（TASK-053，基于 `b4a7b03` 评审冻结）。

## 背景

v0.4.2 使用 Redis Lua+Stream 原子接受秒杀资格，Stream worker 直接执行本地 MySQL Purchase。
微服务化后，worker 必须改为调用 inventory/order RPC，不能绕过服务直接写库；但这并不意味着
Redis Stream 与 RPC 之间必须增加 Kafka。

当前成功 Stream 消息受库存约束，只有一个落单编排消费者，也没有指标证明 Redis 积压、保留期或
故障域已经成为瓶颈。强制增加 Bridge 会引入两套消费组、ACK、retry/DLQ 和额外网络跳数。

与此同时，订单成功后确实存在两个独立下游：秒杀结果投影和用户通知。它们不应让订单事务同步依赖
自身可用性，因此 Kafka 在领域事件层有明确价值。

## 决策

1. Redis Stream 保持 seckill-service 内部短期队列；orchestrator 直接调用 inventory/order RPC。
2. v0.5 不实现 Redis Stream→Kafka Bridge，也不把 Kafka 放在秒杀 HTTP/落单关键路径中。
3. order-rpc 在订单本地事务中同时写 `order_outbox_events`。
4. Outbox relay 等待 broker ack 后条件标记已发布，允许 ack 后崩溃产生重复事件。
5. Kafka 发布版本化 `order.created.v1`，key 使用 `order_no`。
6. `seckill-result-projector` 和 `notification-service` 使用不同 consumer group，各自收到完整事件流。
7. 两个消费者都以 event_id/业务唯一键幂等；自身失败只影响自身 lag，不回滚订单或阻塞对方。
8. notification-service 只实现站内通知、查询和已读；不假装接入短信/邮件供应商。

## 候选方案

### A. Stream→Kafka→RPC

适合长积压、跨团队 command、多个独立 command consumer 或长期重放，但当前没有证据证明收益大于成本，
不作为 v0.5 默认路径。

### B. Stream worker 直接写订单库

延迟最低，但绕过 order-rpc 数据所有权，形成分布式单体，不选择。

### C. Stream 内部编排 + Order Outbox/Kafka 领域事件（选择）

RPC 改造解决数据边界；Kafka 解决真实的跨服务 fan-out。同步、内部队列和领域事件职责清晰，关键路径更短。

### D. 订单提交后直接 best-effort 发布 Kafka

代码少，但 DB commit 成功、Kafka 发布失败会永久丢事件；不能支撑可靠通知和结果投影，不选择。

## 为什么 notification-service 值得加入

- 它是订单事件的真实第二个 consumer group，能证明 Kafka fan-out 而不是同组竞争。
- 通知失败不应影响订单，天然适合异步解耦。
- event_id 幂等、retry/DLQ、consumer lag、独立数据所有权都有可验证结果。
- 用户可通过 notification-rpc 查询站内通知，不是没有调用方的演示服务。

## 可靠性边界

- order+outbox 是同一个 MySQL 本地事务；MySQL 与 Kafka 不宣称全局原子。
- relay ack 后、状态更新前崩溃会重复发布；消费者唯一键兜底。
- consumer DB commit 后、offset commit 前崩溃会重复消费。
- Kafka 停止只增加 Outbox backlog；订单创建不等待 broker。
- 通知或 projector 停止只增加自己的 lag，不改变订单事实。
- inventory reservation 与 order create 的跨服务一致性缺口仍属于 v0.6。

## 何时重新评估 Stream→Kafka Bridge

只有出现并测量到以下问题之一：Redis Stream 内存/保留期不足、长时间积压、跨故障域要求、订单团队只接受
Kafka command、出现第二个独立落单 command consumer，或 Stream→RPC 吞吐成为瓶颈。

## 验证方式

- Kafka 停止后订单与 Outbox 同时存在，恢复后事件可发布。
- 重复 order.created 100 次，projector 和 notification 各只产生一个事实。
- notification consumer 停止时 projector 继续消费，订单 API 不受影响。
- poison event 进入对应 DLQ，后续正常事件继续。
- 边界扫描证明没有 Stream→Kafka Bridge 或旧 seckill job/outbox。

## 回滚策略

- 停止 relay/consumer，保留 Outbox 和 Kafka backlog 供诊断。
- order-rpc 仍可创建订单；回滚前不得删除未发布 Outbox。
- notification 是下游投影，回滚不修改订单或库存。
