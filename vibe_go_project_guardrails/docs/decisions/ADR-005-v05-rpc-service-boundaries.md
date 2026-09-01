# ADR-005: v0.5 拆分 RPC 服务并建立逻辑数据所有权

## 状态

Accepted（TASK-053，基于 `b4a7b03` 评审冻结）。

## 背景

v0.4.2 的 HTTP、领域服务、Redis worker 和 MySQL repository 在同一进程内，秒杀 `Purchase`
可以在一个事务里同时扣库存、写订单和 claim。这个边界无法学习远程调用失败；同时现有 repository
会跨用户、商品、订单和秒杀表 JOIN，直接把它放进多个 RPC 进程只会形成“分布式单体”。

ROADMAP 规定 v0.5 先处理服务边界、RPC、服务发现、超时和熔断，v0.6 再处理最终一致性、补偿和对账。

## 决策

1. 拆分 `user-rpc`、`product-rpc`、`seckill-rpc`、`inventory-rpc`、`order-rpc`、
   `notification-rpc`，gateway 只保留 HTTP/JWT/DTO 映射。
2. 使用 go-zero zRPC、Protobuf v1 契约和 etcd 服务发现；不同时引入第二套 RPC 框架。
3. v0.5 仍使用同一 MySQL 实例，但明确表所有权；每个进程只装配自有 repository，禁止跨服务 SQL/JOIN。
4. 移除跨服务外键；外部 ID 只作引用。服务内 activity/item/reservation 外键继续保留。
5. 秒杀 item 在配置时通过 product-rpc 冻结订单快照，异步消费时不重新读取实时商品价格。
6. 将原本的单事务 Purchase 拆为：inventory 本地事务幂等预留，再由 order 本地事务幂等建单。
7. 两步间失败时优先保证不超卖，允许出现 reservation 无 order；只做稳定状态和诊断，不在 v0.5 自动补偿。
8. Redis Stream 保持为 seckill-service 内部队列；orchestrator 通过 RPC 落单，不直接访问其他服务数据。
9. order-rpc 使用本地 Outbox 发布 Kafka `order.created`，notification 与秒杀结果投影作为独立下游。
10. 日常每个应用进程单实例；治理验收只扩展 order-rpc 和 notification-consumer 为双实例，避免用
    无目的的全量副本替代真实故障验证。

## 服务所有权

- user-rpc：用户、凭据、角色与权限。
- product-rpc：商品与 SKU。
- seckill-rpc：秒杀活动、Redis 准入、Stream 和结果投影。
- inventory-rpc：秒杀 item、冻结快照与 MySQL reservation。
- order-rpc：普通订单与秒杀订单。
- notification-rpc：站内通知与用户查询。

## 候选方案

### A. 只把单体套一层 RPC

改动最小，但各服务继续共享 repository 和跨域 JOIN；独立部署、权限和故障边界都是假的，不选择。

### B. v0.5 直接物理分库并实现 Saga/TCC

边界最彻底，但把 RPC 拆分、数据迁移和一致性治理一次完成，无法隔离学习变量，也违反 v0.6 阶段边界。

### C. 保留 order+inventory 为 trade-rpc

可以继续使用单事务，正确性风险最低；但不会暴露 ROADMAP 中“跨服务后没有本地事务”的真实问题，
inventory/order 的 RPC 治理也无法验证，不选择。

### D. 逻辑表所有权 + 两个幂等本地事务（选择）

服务边界真实、迁移成本受控；先用唯一键和重试守住不超卖，再把 reservation 无 order 明确留给 v0.6。

## 代价

- 需要 migration 移除跨服务外键、冻结商品快照并建立 reservation 账本。
- 同一 MySQL 实例仍共享故障域，不代表已经实现完整物理隔离。
- inventory 成功、order 失败会少卖；只靠重试不能证明所有异常最终修复。
- gateway 和 worker 需要维护调用预算、错误映射和契约兼容。

## 验证方式

- 静态扫描服务装配和 SQL，证明没有跨服务 repository/表访问。
- migration 对历史 claim 回填并执行 up/down/up。
- 重复 100 次 `ReserveSeckillStock` 和 `CreateSeckillOrder`，各自只有一个业务事实。
- order-rpc 超时/不可用时 reservation 保留且可诊断，不盲目回补库存。
- 同 order_no 不同载荷返回冲突。

## 回滚策略

- 停止 gateway/worker/RPC 进程，切回 `codex/v0.4.2-redis-stream` 的单体与 Stream worker。
- 回滚前排空或保留 Kafka/Stream backlog 供诊断；不得清空 Redis 或手工加库存。
- migration down 只在核对 reservation/order 数量后执行；保留备份和计数报告。
