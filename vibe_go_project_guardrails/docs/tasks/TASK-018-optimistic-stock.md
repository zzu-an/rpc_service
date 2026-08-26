# TASK-018: 实现乐观锁库存策略

## 背景

悲观锁通过排队保证正确性；当前任务用 version CAS 展示另一种冲突处理方式。

## 目标

实现带版本条件的库存扣减，以及有次数上限、context 可取消并带退避抖动的事务重试。

## 非目标

- 不允许 HTTP 客户端选择策略。
- 不宣称乐观锁一定更快。
- 不无限重试。

## 允许修改

- `internal/seckill/mysqlrepo/repository.go`
- `internal/seckill/mysqlrepo/purchase_optimistic_test.go`
- 本 TASK 文档

## 验收标准

- [x] UPDATE 同时检查库存和 version。
- [x] CAS 冲突回滚整个事务后再重试。
- [x] 重试有固定上限、context 取消和退避抖动。
- [x] 20 并发抢 5 库存时恰好 5 成功、15 售罄，version 最终为 5。
- [x] 隔离 MySQL race、全量测试和 vet 通过。

## 验证命令

```bash
SERVICE_RPC_MYSQL_TEST_DSN='...' go test -race -count=3 -run TestOptimisticPurchaseRetriesBoundedContention ./internal/seckill/mysqlrepo
go test ./...
go vet ./...
```

## 回滚点

移除乐观模式、重试逻辑和对应测试；原子及悲观模式不变。

## 完成记录

### 修改文件

- `internal/seckill/mysqlrepo/repository.go`：version CAS 与有界重试。
- `internal/seckill/mysqlrepo/purchase_optimistic_test.go`：高冲突正确性测试。
- 本 TASK 文档：记录范围与验收。

### 测试结果

- 隔离 MySQL `go test -race -count=3 -run TestOptimisticPurchaseRetriesBoundedContention`：PASS。
- `go test ./...`：PASS。
- `go vet ./...`：PASS。

### 遗留问题

- 当前没有基准数据，不能据此判断三种方案的性能优劣。
