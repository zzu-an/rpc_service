# TASK-036: 实现 Job 领域与 MySQL 仓储

## 目标

实现 Ensure、到期扫描、条件状态更新和所属用户查询的持久化边界。

## 非目标

- 不发 Kafka、不改 Purchase、不改 HTTP。

## 允许修改

- `internal/seckill/seckill.go`、`seckill_test.go`
- `internal/seckill/mysqlrepo/job_repository.go`、`job_repository_test.go`（新增）

## 实现约束

- Ensure 以 order_no 幂等，冲突时验证 user/item 不漂移。
- 状态更新必须条件化，终态不能被迟到更新覆盖。
- last_error 只保存稳定代码。

## 验收标准

- [x] 并发 Ensure 只生成一行。
- [x] 非法状态转换被拒绝，所属用户查询防越权。

## 验证命令

```bash
go test -race ./internal/seckill ./internal/seckill/mysqlrepo
```

## 回滚点

删除新仓储文件和领域接口；同步 Purchase 不变。
