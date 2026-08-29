# ARCHITECTURE.md

## 目标

构建一个从单体商城逐步演进到秒杀微服务系统的 Go 项目。

核心原则：

- 每个阶段只解决当前阶段的问题
- 技术由问题驱动，不由“技术栈清单”驱动
- 所有关键架构变化可追溯
- 每个阶段都可独立运行、测试和压测

## 推荐代码组织

```text
cmd/
  api/
internal/
  user/
  product/
  order/
  inventory/
  auth/
  platform/
    db/
    cache/
    mq/
    rpc/
    observability/
pkg/
api/
docs/
```

注意：不要在 v0.1 就把所有目录全部建出来。仅在实际需要时创建。

## 依赖方向

```text
transport / handler
        ↓
application / service
        ↓
domain
        ↓
repository interfaces
        ↓
infrastructure implementations
```

禁止：

```text
domain → go-zero handler
domain → MySQL driver
domain → Redis client
service A → service B database
```

## 数据边界

每个业务模块只通过明确接口访问自己的数据。

微服务化后：

- 服务之间不得共享 repository
- 服务之间不得直接访问对方数据库
- 跨服务交互通过 RPC / MQ
- 跨服务事务默认不存在，必须显式处理一致性

## 阶段性架构原则

### v0.1
单体。先把业务流跑通。

### v0.2
仍然单体。重点学习：
- 库存竞争
- MySQL 事务
- 行锁 / 乐观锁
- 幂等
- 并发安全

### v0.3
引入 Redis，目标是削峰和减少 DB 热点。

### v0.4.1（Kafka 分支）
使用 MySQL Outbox、Kafka consumer、retry/DLQ 学习通用可靠消息，代码位于
`codex/v0.4.1-kafka`。

### v0.4.2（Redis Stream 分支）
秒杀入口使用同 slot 的 Redis Lua 同时完成库存预扣、buyer 幂等标记和 Stream `XADD`，
Stream worker 直接调用既有 MySQL Purchase 事务。当前工作树只包含该实现；Kafka 代码、
job migration 和配置被隔离在另一分支，避免两套队列语义互相干扰。

### v0.5
拆微服务，引入 RPC、服务发现、超时、熔断。

### v0.6
处理分布式一致性。

### v0.7
处理高可用、限流、降级、隔离。

### v1.0
工程化、观测、容器化、压测、故障演练。

## 变更规则

任何架构级变化必须记录在 `docs/decisions/`。

命名：

```text
ADR-001-xxx.md
ADR-002-xxx.md
```
