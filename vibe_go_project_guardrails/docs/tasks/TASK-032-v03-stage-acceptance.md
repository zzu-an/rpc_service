# TASK-032: v0.3 阶段验收与文档收口

## 背景

v0.3 的功能、失败语义和实验已分别完成，需要用单一门禁证明 Redis 原子性、双存储不变量、缓存风险边界和阶段技术隔离。

## 目标

建立 `verify-v03` 入口并记录可重复的阶段验收证据。

## 依赖

- TASK-023 至 TASK-031 全部完成。

## 非目标

- 不在验收任务修复大范围业务设计。
- 不引入 MQ、RPC、对账补偿、限流或观测平台。
- 不把 benchmark 数字写成无环境约束的容量承诺。

## 允许修改

- `Makefile`
- `internal/seckill/redisgate/stage_v03_test.go`（新增）
- `docs/v0.3-acceptance.md`（新增）
- 本 TASK 文档

## 实现约束

- `verify-v03` 必须要求显式 `TEST_DSN` 和 `TEST_REDIS_ADDR`；缺失时失败并说明如何提供，不能静默 skip。
- 测试数据使用唯一前缀/ID，清理只能命中本轮 fixture，禁止 `FLUSHALL`、`FLUSHDB` 或宽泛删除。
- 验收至少覆盖 1000 并发/100 库存、同用户 100 并发、Redis 不可用、key 丢失、MySQL 失败后保守预留。
- 同时执行全量 race、vet、migration `up/down/up` 和 `git diff --check`。
- 技术边界扫描确认没有 MQ、业务 RPC/etcd、分布式事务、生产分布式锁、系统级限流实现。
- 报告引用 TASK-031 原始 JSON，不复制或手改指标。
- 注释质量审查按 spec 的“为什么/边界”清单逐项确认，不能只统计注释行数。

## 验收标准

- [x] Lua 并发原子性和 buyers 上界通过真实 Redis 验证。
- [x] MySQL 订单、唯一购买记录和库存不变量通过真实双存储验证。
- [x] Redis 售罄、缺 key、断连时 MySQL Purchase 调用为 0。
- [x] 保守差值可诊断，工具不在线修复。
- [x] 分布式锁 lab 完成且生产零引用。
- [x] v0.2/v0.3 对照报告可复现、环境说明完整。
- [x] 全量质量门禁与阶段扫描通过。
- [x] 能清楚回答“v0.3 解决了什么、引入了什么、为什么可能少卖”。

## 验证命令

```bash
make verify-v03 TEST_DSN='...' TEST_REDIS_ADDR='127.0.0.1:6379'
git diff --check
```

## 回滚点

移除阶段测试、Makefile target 和验收文档；业务实现不变。

## 完成记录

### 修改文件

- `Makefile`：新增强制外部设施、migration 往返、全量 race、vet、边界与 diff 的 `verify-v03`。
- `internal/seckill/redisgate/stage_v03_test.go`：增加防假绿环境门禁、1000/100 Lua 并发和隔离库 migration 往返。
- `docs/v0.3-acceptance.md`：记录正确性、性能原始报告、缓存风险、注释审查和阶段边界。

### 测试结果

- 一次性 MySQL 8 隔离库 + 真实 Redis 执行 `make verify-v03 ...`：PASS；migration 最终 latest、`dirty=false`。
- 全量 race（含 1000/100 双存储、100 用户重放）、vet、边界扫描、`git diff --check`：PASS。
- mysql/redis HTTP unique 同为 100 created、900 sold out；redis replay 为 1 created、99 replayed，末态满足 spec。

### 遗留问题

- MQ 削峰留给 v0.4；自动补偿/对账留给 v0.6；完整热点治理留给 v0.7。
