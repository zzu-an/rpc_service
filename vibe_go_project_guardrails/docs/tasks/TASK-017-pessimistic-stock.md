# TASK-017: 实现悲观锁库存策略

## 背景

原子更新已经正确，需要增加 `SELECT ... FOR UPDATE` 变体，用真实并发理解行锁和热点排队。

## 目标

允许内部装配选择悲观锁，在一个事务内锁定库存行、检查库存并扣减。

## 非目标

- 不允许 HTTP 客户端选择策略。
- 不实现乐观锁。
- 不做性能提升结论。

## 允许修改

- `internal/seckill/mysqlrepo/repository.go`
- `internal/seckill/mysqlrepo/purchase_pessimistic_test.go`
- 本 TASK 文档

## 验收标准

- [x] 默认构造器仍使用原子更新。
- [x] 悲观模式使用 `FOR UPDATE`，锁持有到事务结束。
- [x] 20 并发抢 5 库存时恰好 5 成功、15 售罄。
- [x] 库存、订单和 claim 数量一致。
- [x] 隔离 MySQL race、全量测试和 vet 通过。

## 验证命令

```bash
SERVICE_RPC_MYSQL_TEST_DSN='...' go test -race -run TestPessimisticPurchaseSerializesHotInventory ./internal/seckill/mysqlrepo
go test ./...
go vet ./...
```

## 回滚点

移除悲观模式和对应测试；默认原子链路不变。

## 完成记录

### 修改文件

- `internal/seckill/mysqlrepo/repository.go`：库存模式与悲观锁实现。
- `internal/seckill/mysqlrepo/purchase_pessimistic_test.go`：热点库存并发测试及共享断言。
- 本 TASK 文档：记录范围与验收。

### 测试结果

- 隔离 MySQL `go test -race -count=3 -run TestPessimisticPurchaseSerializesHotInventory`：PASS。
- `go test ./...`：PASS。
- `go vet ./...`：PASS。

### 遗留问题

- 未做吞吐结论；v0.2 只证明正确性与锁行为。
