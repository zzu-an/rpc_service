# TASK-015: 在测试中稳定复现先查后改导致的超卖

## 背景

引入正确方案前，需要用可重复证据证明旧方案的问题，而不是只背诵“会超卖”。

## 目标

在真实 MySQL 集成测试中，让多个 goroutine 同时读取同一库存，再无条件写回，证明业务放行数超过初始库存。

## 非目标

- 错误方案不得出现在非测试文件。
- 不创建订单，不实现正确扣减策略。

## 允许修改

- `internal/seckill/mysqlrepo/oversell_test.go`
- 本 TASK 文档

## 验收标准

- [x] 所有 worker 在更新前完成库存读取，结果确定而非依赖概率。
- [x] 初始库存为 1 时错误放行数大于 1。
- [x] 测试解释最终库存不为负也可能已经发生业务超卖。
- [x] 集成测试和 race detector 通过。
- [x] 正式代码不引用错误方案。

## 验证命令

```bash
SERVICE_RPC_MYSQL_TEST_DSN='...' go test -race -run TestUnsafeCheckThenSetDemonstratesOversell ./internal/seckill/mysqlrepo
rg -n "故意保留在测试里的错误写法" internal --glob '!**/*_test.go'
```

## 回滚点

删除测试文件；不影响正式程序。

## 完成记录

### 修改文件

- `internal/seckill/mysqlrepo/oversell_test.go`：真实 MySQL 错误方案复现。
- 本 TASK 文档：记录范围与验收。

### 测试结果

- 隔离 MySQL `go test -race -count=3 -run TestUnsafeCheckThenSetDemonstratesOversell`：PASS。
- 正式代码错误方案扫描：无命中，PASS。

### 遗留问题

- 正确库存事务由后续 TASK 实现。
