# TASK-027: 接入 Redis 准入下单

## 背景

Redis 已能原子预留资格，但生产购买服务尚未使用它。需要在不改变 MySQL 最终事务和成功响应的前提下，把 Redis 放到 Purchase 之前。

## 目标

让 redis admission mode 的下单请求先通过 Lua，再使用同一个 orderNo 执行现有 MySQL Purchase。

## 依赖

- TASK-026A、TASK-026B 完成。

## 非目标

- 不异步下单、不返回排队中。
- 不自动回补 Redis。
- 不删除 MySQL 条件更新或唯一索引。
- 不实现对账修复、限流、熔断或降级。

## 允许修改

- `internal/seckill/seckill.go`
- `internal/seckill/seckill_test.go`
- `internal/handler/seckill_order.go`
- `internal/handler/seckill_order_test.go`
- `main.go`
- 本 TASK 文档

## 禁止修改

- `internal/seckill/mysqlrepo/**`（除非测试证明现有 orderNo 恢复接口不足，届时先更新 ADR）
- `migrations/**`
- 其他业务模块

## 实现约束

- 应用层只读取一次 now，并把同一 UTC 时间传给 Redis gate 与 MySQL Purchase。
- 先生成候选 orderNo；Lua reserved/replayed 后必须使用 Lua 返回的 orderNo，不能为重试再生成。
- Lua NOT_READY/UNAVAILABLE/SOLD_OUT 或基础设施错误时不得调用 MySQL Purchase。
- Redis 调用结果未知时返回 503，不以“可能没执行”为理由重试脚本或回退 MySQL。
- Lua 成功后，无论 MySQL 返回明确错误、context 取消还是 commit 未知，都不自动删除 buyer 或增加 Redis stock。
- mysql admission mode 保留为对照和进程级回滚路径；不能由 HTTP 请求选择。
- 新增 `SECKILL_CACHE_NOT_READY` 与 `SECKILL_TEMPORARILY_UNAVAILABLE`，现有成功 payload 不变。
- 关键中文注释解释事实源、同 orderNo 恢复、fail closed 和不自动回补的正确性取舍。

## 验收标准

- [x] Redis 首次预留创建订单；重放返回同一订单和 `replayed=true`。
- [x] 售罄/缺 key/Redis 错误时 MySQL Purchase 调用次数为 0。
- [x] MySQL 失败后 Redis 资格保持，后续同用户使用同一 orderNo 安全重试。
- [x] 1000 并发/100 库存时订单、DB 库存、Redis 库存和 buyers 满足 spec 不变量。
- [x] 同一用户 100 并发只消耗一个 Redis 资格且只产生一个订单。
- [x] mysql/redis 两种启动模式行为可区分且日志不泄漏凭据。

## 验证命令

```bash
TEST_DSN='...' TEST_REDIS_ADDR='127.0.0.1:6379' go test -race ./internal/seckill/... ./internal/handler
go test -race ./...
go vet ./...
git diff --check
```

## 回滚点

配置切回 mysql admission mode 并重启；不逐请求绕过 Redis，不修改已创建订单。

## 完成记录

### 修改文件

- `internal/seckill/seckill.go`、`seckill_test.go`：准入下单、同一 now/orderNo、fail closed 和 spy 断言。
- `internal/handler/seckill_order.go`、`seckill_order_test.go`：两个 Redis 503 错误码。
- `internal/seckill/mysqlrepo/repository.go`：同 orderNo 唯一冲突进入幂等赢家读取。
- `internal/seckill/mysqlrepo/redis_purchase_test.go`：真实 Redis+MySQL 双存储并发验收。
- `main.go`：生产服务显式装配 AdmissionGate。
- ADR-002：记录 orderNo 唯一冲突恢复语义。

### 测试结果

- 领域/handler/mysqlrepo race：PASS。
- 真实 Redis+MySQL 1000 并发/100 库存：100 成功、900 Redis 售罄，双库存为 0、buyers/claims 为 100。
- 同用户 100 并发：只扣一个资格、只创建一个订单，所有响应返回同一订单 ID。
- `go test ./...`、`go vet ./...`、`git diff --check`：PASS。

### 遗留问题

- Redis/DB 差值只读诊断由 TASK-028 完成。
