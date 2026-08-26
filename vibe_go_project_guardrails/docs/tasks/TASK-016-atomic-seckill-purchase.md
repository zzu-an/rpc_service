# TASK-016: 实现原子扣库存的同步秒杀事务

## 背景

错误方案已经证明检查与更新分离会超卖，需要实现正式的 MySQL 条件原子更新和订单事务。

## 目标

使用条件 UPDATE 完成库存扣减，并在同一事务写订单、订单项和抢购记录；重复请求返回已有订单。

## 非目标

- 不实现悲观锁和乐观锁变体。
- 不注册用户 HTTP 路由。
- 不修改 schema。

## 允许修改

- `internal/seckill/mysqlrepo/repository.go`
- `internal/seckill/mysqlrepo/purchase_atomic_test.go`
- 本 TASK 文档

## 验收标准

- [x] 正式扣减使用 `UPDATE ... available_stock > 0` 和 RowsAffected。
- [x] 库存、订单、订单项、抢购记录在同一事务。
- [x] 唯一键冲突回滚竞争事务并返回已存在订单。
- [x] 活动和商品不可用时不产生副作用。
- [x] 隔离 MySQL 集成测试、race、全量测试和 vet 通过。

## 验证命令

```bash
SERVICE_RPC_MYSQL_TEST_DSN='...' go test -race ./internal/seckill/mysqlrepo
go test ./...
go vet ./...
```

## 回滚点

恢复 Repository 到仅支持活动配置的版本，并删除购买测试；schema 不变。

## 完成记录

### 修改文件

- `internal/seckill/mysqlrepo/repository.go`：原子库存事务与幂等读取。
- `internal/seckill/mysqlrepo/purchase_atomic_test.go`：真实 MySQL 正常、重放、售罄和禁用活动测试。
- 本 TASK 文档：记录范围与验收。

### 测试结果

- 隔离 MySQL `go test -race ./internal/seckill/mysqlrepo`：PASS。
- `go test ./...`：PASS。
- `go vet ./...`：PASS。

### 遗留问题

- 悲观锁、乐观锁和高并发矩阵由后续 TASK 完成。
