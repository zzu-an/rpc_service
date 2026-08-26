# TASK-012: 定义秒杀领域与应用服务边界

## 背景

Schema 已冻结，需要先建立不依赖数据库和 HTTP 框架的业务规则，避免把参数校验、SQL 和路由耦合在一起。

## 目标

定义活动、秒杀商品、购买结果、领域错误和 Repository 契约，并完成纯单元测试。

## 非目标

- 不实现 MySQL Repository。
- 不注册 HTTP 路由。
- 不实现库存锁策略。

## 允许修改

- `internal/seckill/seckill.go`
- `internal/seckill/seckill_test.go`
- 本 TASK 文档

## 验收标准

- [x] 领域包不依赖 MySQL、handler 或 go-zero。
- [x] 活动时间、库存、状态、用户和商品 ID 有明确校验。
- [x] Purchase 只向 Repository 传递一个稳定 UTC 时间点。
- [x] 单元测试、全量测试和 vet 通过。

## 验证命令

```bash
go test ./internal/seckill
go test ./...
go vet ./...
```

## 回滚点

删除新增领域包和本 TASK 文档；没有 schema 或公共 HTTP 影响。

## 完成记录

### 修改文件

- `internal/seckill/seckill.go`：领域类型、错误、服务和持久化边界。
- `internal/seckill/seckill_test.go`：纯领域单元测试。
- 本 TASK 文档：记录范围与验收。

### 测试结果

- `go test ./internal/seckill`：PASS。
- `go test ./...`：PASS。
- `go vet ./...`：PASS。

### 遗留问题

- Repository 尚未实现，服务尚未接线。
