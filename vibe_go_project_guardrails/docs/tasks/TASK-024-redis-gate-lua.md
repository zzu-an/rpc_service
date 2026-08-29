# TASK-024: 实现 Redis key 契约与 Lua 准入

## 背景

多条 `GET`/`HSET`/`DECR` 命令会在并发请求间交错。v0.3 需要把资格检查、库存预扣和购买标记写入收敛为一次 Redis 原子状态转换。

## 目标

实现与存储无关的准入接口及 Redis Lua 适配器，返回稳定的 reserved/replayed/rejected 结果。

## 依赖

- TASK-023 完成。

## 非目标

- 不读取 MySQL。
- 不接入 HTTP 或生产下单 Service。
- 不实现预热接口、库存回补或在线修复。
- 不使用分布式锁。

## 允许修改

- `internal/seckill/seckill.go`
- `internal/seckill/seckill_test.go`
- `internal/seckill/redisgate/gate.go`（新增）
- `internal/seckill/redisgate/gate_test.go`（新增）
- 本 TASK 文档

## 禁止修改

- `internal/seckill/mysqlrepo/**`
- `internal/handler/**`
- `main.go`
- `migrations/**`

## 实现约束

- 领域接口不能暴露 go-zero/Redis 具体类型，只表达 item、user、候选 orderNo、now 和准入结果。
- key 必须严格使用 `seckill:v03:{item:<id>}:state|buyers`，两个 key 共享 hash tag。
- Lua 先检查重复用户再检查库存；重复时返回第一次保存的 orderNo。
- 脚本只做 O(1) Hash 操作，禁止 `KEYS`、扫描或不受控循环。
- 脚本源码可通过常量或 embed 管理，但必须和 Go 结果码测试绑定，避免两边漂移。
- `EVALSHA` 遇到 `NOSCRIPT` 只重载并重试一次；其他超时/断连不能推断脚本未执行。
- Redis 业务拒绝与基础设施错误必须使用不同类型，handler 后续才能映射为稳定 HTTP 语义。
- 关键中文注释充分解释检查顺序、同一 orderNo、Lua 阻塞 shard、hash tag 与结果未知边界。

## 验收标准

- [x] 未预热、未开始、已结束、停用、售罄结果可区分。
- [x] 首次用户只扣一次库存并写 buyer；重复用户取回相同 orderNo。
- [x] 并发 N 请求时库存不为负，buyers 数不超过初始库存。
- [x] `NOSCRIPT` 恢复次数有测试，普通错误不无限重试。
- [x] 真实 Redis 集成测试要求显式 `TEST_REDIS_ADDR`，不能静默 skip 阶段门禁。

## 验证命令

```bash
TEST_REDIS_ADDR='127.0.0.1:6379' go test -race ./internal/seckill/redisgate
go test ./internal/seckill
go vet ./...
git diff --check
```

## 回滚点

删除 redisgate 适配器并恢复领域接口；尚未接入生产链路。

## 完成记录

### 修改文件

- `internal/seckill/seckill.go`：新增 Redis 准入领域边界、预留输入/结果和错误语义。
- `internal/seckill/redisgate/gate.go`：key 契约、O(1) Lua、EVALSHA/NOSCRIPT 与结果解析。
- `internal/seckill/redisgate/gate_test.go`：fake runner、错误重试上限和真实 Redis 并发测试。

### 测试结果

- 领域/redisgate 包 race：PASS。
- 指定真实 Redis 的 100 并发/25 库存 Lua 测试及重放：PASS。
- `go test ./...`、`go vet ./...`、`git diff --check`：PASS。

### 遗留问题

- state/buyers 数据由 TASK-026A 的预热用例发布。
