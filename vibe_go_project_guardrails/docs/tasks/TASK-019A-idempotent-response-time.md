# TASK-019A: 保证首次与幂等重放订单时间一致

## 背景

真实 API 验收发现首次响应使用应用时间，重放响应读取数据库默认时间，导致同一订单的 `created_at` 相差约 10ms。

## 目标

以 MySQL DATETIME(6) 的微秒精度显式写入创建时间，使首次和重放响应字段一致。

## 非目标

- 不修改 HTTP/schema。
- 不改变库存和幂等策略。

## 允许修改

- `internal/seckill/mysqlrepo/repository.go`
- `internal/seckill/mysqlrepo/purchase_atomic_test.go`
- 本 TASK 文档

## 验收标准

- [x] 写入前将时间转 UTC 并截断到微秒。
- [x] 首次响应和重放响应的订单 ID、订单号、created_at 一致。
- [x] 集成测试、真实 API 和阶段门禁通过。

## 验证命令

```bash
SERVICE_RPC_MYSQL_TEST_DSN='...' go test -race -run TestAtomicPurchaseAndIdempotentReplay ./internal/seckill/mysqlrepo
make verify-v02 TEST_DSN='...'
```

## 回滚点

恢复订单 INSERT 使用数据库默认创建时间，并移除一致性断言。

## 完成记录

### 修改文件

- `internal/seckill/mysqlrepo/repository.go`：显式写入微秒精度订单时间。
- `internal/seckill/mysqlrepo/purchase_atomic_test.go`：增加重放时间一致性断言。
- 本 TASK 文档：记录真实 API 发现的问题。

### 测试结果

- 隔离 MySQL `go test -race -run TestAtomicPurchaseAndIdempotentReplay`：PASS。
- `make verify-v02 TEST_DSN=...`：PASS。
- 真实 API 首次/重放返回相同订单 ID、订单号和 `created_at`：PASS。

### 遗留问题

- 无。
