# ADR-004: v0.4.2 秒杀热路径使用 Redis Stream

## 状态

Accepted

## 背景

Kafka 方案已经在 `codex/v0.4.1-kafka` 验证，但其 HTTP 热路径仍写 MySQL job。为了让学习者
能独立理解两种方案，Redis Stream 版本不能继续在同一工作树保留 Kafka worker、配置和
job 状态机。秒杀成功任务数量通常接近库存而不是总请求数，适合用 Redis Stream 承载
从资格到正式订单的短期工作队列。

## 决策

1. 新增 `async-stream`：一个 Lua 在相同 `{item:<itemID>}` slot 内完成库存减一、buyer
   标记、结果索引和 `XADD`，成功后直接返回 HTTP 202。
2. Stream worker 使用 consumer group 读取任务，复用既有 MySQL `Purchase` 事务；订单事务
   成功后才 `XACK`，失败消息保留在 PEL，并通过 `XAUTOCLAIM` 恢复。
3. 超限或不可重试消息原子转入同 item DLQ Stream 后再 ACK，避免 poison message 永久阻塞。
4. 当前分支只保留 `sync` 与 `async-stream`；Kafka 代码、依赖和 migration 隔离到
   `codex/v0.4.1-kafka`，通过 Git 切换版本。
5. 不新增数据库表；MySQL 订单和 claim 唯一索引仍是重复投递下的最终幂等防线。

## 候选方案

### 方案 A：Stream 直接到 MySQL（选择）

优点：入口不写 MySQL；预扣与入队原子；链路短；Kafka 故障不影响秒杀落单。

缺点：Redis 同时承担准入与短期队列；需要治理 PEL、过期与 Redis 持久化；单个热点 item
仍固定在一个 shard。

### 方案 B：Stream Bridge 到 Kafka，再到 MySQL

优点：Kafka 更适合长积压、跨团队订阅和长期保留。

缺点：多一跳和一套进度；Bridge 前 Redis 数据丢失窗口仍存在；当前只有一个订单消费者，
复杂度高于收益。

### 方案 C：同一工作树同时保留 Kafka 与 Stream

优点：无需切换分支即可做运行时对照。

缺点：两套 worker、重试、DLQ、结果查询和配置同时出现，增加学习与排障难度；用户已明确
要求方案隔离，因此不选择。

## 为什么选择当前方案

- Lua 与 Stream 位于同一 Redis shard，可真正消除“已扣资格但未产生任务”的应用级双写窗口。
- 新消息数量受 Redis 库存约束，10 万请求抢 100 件时通常只产生约 100 条首次任务。
- 当前分支只学习秒杀内部短链路；Kafka/Outbox 的学习成果保留在独立分支，不在此重复出现。
- 不提前引入 RPC、etcd、支付状态机、自动补偿或完整观测平台。

## 代价

- Redis 未持久化写丢失、主从异步复制丢失仍可能造成少卖；生产部署需要 AOF、主从和故障演练。
- 同一 item 的 state、buyers、results、Stream 都在同一 slot，单个超级热点 item 无法跨 shard
  并行；热点分桶属于 v0.7，当前通过多个 item 自然散列。
- `XACK` 不删除条目，worker 必须在成功路径显式删除；失败任务必须有 PEL reclaim 和 DLQ。
- 结果查询依赖自描述 order_no 中的 itemID 定位同 slot 结果索引，不能接受客户端提供 itemID。

## 验证方式

- 真实 Redis 并发验证“stock、buyers、Stream 消息数”同时等于库存且重复用户只产生一条消息。
- 停止 worker 制造 PEL，恢复后由 `XAUTOCLAIM` 重新处理。
- 注入 MySQL 提交后、ACK 前失败，重复处理只产生一笔订单和 claim。
- poison message 超限进入 DLQ，后续合法消息继续处理。
- 当前分支比较 sync 与 async-stream；跨方案对照时分别切换两个分支并保存报告。

## 回滚策略

- 单进程内可切回 `sync`；要回到 Kafka 方案时执行 `git switch codex/v0.4.1-kafka`。
- 停止 Stream worker 前先排空 PEL。保留 Stream/DLQ 供诊断，不执行 `FLUSHALL` 或库存回补。
