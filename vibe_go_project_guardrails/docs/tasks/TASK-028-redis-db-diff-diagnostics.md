# TASK-028: 实现 Redis/DB 差值只读诊断

## 背景

Redis 预留和 MySQL 事务不能原子提交。保守失败策略允许 Redis stock 小于 MySQL available_stock，但必须能区分正常一致、容量预留、缓存缺失和危险反向漂移，不能只说“最终会一致”。

## 目标

提供指定 item 的只读 JSON 诊断，解释 Redis 库存、buyers 与 MySQL 库存/购买记录之间的差值。

## 依赖

- TASK-027 完成。

## 非目标

- 不自动增减库存、删除 key 或补建订单。
- 不扫描全部数据库或 Redis keyspace。
- 不增加 HTTP 管理接口、定时任务或告警系统。
- 不实现 v0.6 对账与补偿。

## 允许修改

- `internal/seckill/mysqlrepo/repository.go`
- `internal/seckill/mysqlrepo/repository_test.go`
- `internal/seckill/redisgate/gate.go`
- `internal/seckill/redisgate/gate_test.go`
- `cmd/v03check/main.go`（新增）
- `cmd/v03check/main_test.go`（新增）
- `docs/v0.3-consistency.md`（新增）
- 本 TASK 文档

## 实现约束

- 输入必须是精确 item ID；禁止 `KEYS *`、全表扫描或默认生产地址。
- 输出至少包含 DB initial/available/claim count、Redis stock/buyer count/generation/TTL 和计算状态。
- 状态必须可枚举：`CONSISTENT`、`RESERVED_AHEAD`、`CACHE_MISSING`、`DANGEROUS_DRIFT`、`UNKNOWN`。
- `RESERVED_AHEAD` 表示 Redis 比 DB 更保守，只能报告少卖风险，不能擅自修复。
- Redis stock 高于 DB 或 buyers 超出上界必须标为危险漂移并非零退出。
- 任一存储读取失败不能被展示为 0，必须标记 UNKNOWN 并保留脱敏错误类别。
- 工具必须只读；代码与文档明确说明为什么结果未知时不能自动回补。
- 中文注释重点解释每个差值公式的含义和它不能证明什么。

## 验收标准

- [x] 一致、保守预留、缺 cache、反向漂移和读取失败均有测试。
- [x] 工具不会修改 Redis 或 MySQL。
- [x] 输出可作为压测报告附件，且不包含 DSN/密码。
- [x] 文档用故障矩阵解释 Lua 成功/MySQL 失败、commit 未知和 Redis 结果未知。

## 验证命令

```bash
go test -race ./cmd/v03check ./internal/seckill/mysqlrepo ./internal/seckill/redisgate
go run ./cmd/v03check -h
go test ./...
go vet ./...
git diff --check
```

## 回滚点

删除诊断命令和只读状态方法；不影响下单路径和任何业务数据。

## 完成记录

### 修改文件

- `internal/seckill/mysqlrepo/repository.go`、测试：精确 item 的 DB 库存/claim 只读状态。
- `internal/seckill/redisgate/gate.go`、测试：精确 key 的 stock/buyers/generation/TTL 状态。
- `cmd/v03check/main.go`、测试：五类状态、脱敏 JSON 和退出码。
- `docs/v0.3-consistency.md`：差值公式、能力边界和故障矩阵。

### 测试结果

- v03check/mysqlrepo/redisgate race：PASS。
- 指定真实 MySQL 与 Redis 的只读检查：PASS。
- `go run ./cmd/v03check -h`、全量测试、vet、diff check：PASS。

### 遗留问题

- 自动修复、补偿和周期对账明确留给 v0.6。
