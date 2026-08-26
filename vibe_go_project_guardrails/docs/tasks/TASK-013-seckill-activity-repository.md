# TASK-013: 实现秒杀活动 MySQL Repository

## 背景

领域契约已经冻结，需要先实现活动配置能力，购买事务仍留给后续独立任务。

## 目标

实现创建活动、添加秒杀 SKU 和更新活动状态的 MySQL 持久化。

## 非目标

- 不实现 Purchase。
- 不注册 HTTP 路由。
- 不实现库存扣减策略。

## 允许修改

- `internal/seckill/mysqlrepo/repository.go`
- `internal/seckill/mysqlrepo/repository_test.go`
- 本 TASK 文档

## 验收标准

- [x] 活动默认以 disabled 状态创建。
- [x] 只能配置存在且启用的商品 SKU。
- [x] 重复活动 SKU 映射为领域冲突。
- [x] 重复设置相同状态幂等成功。
- [x] 隔离 MySQL 集成测试、全量测试和 vet 通过。

## 验证命令

```bash
SERVICE_RPC_MYSQL_TEST_DSN='...' go test ./internal/seckill/mysqlrepo
go test ./...
go vet ./...
```

## 回滚点

删除新增 Repository 包；不会影响 schema 和 HTTP。

## 完成记录

### 修改文件

- `internal/seckill/mysqlrepo/repository.go`：活动配置 MySQL 实现。
- `internal/seckill/mysqlrepo/repository_test.go`：真实 MySQL 生命周期测试。
- 本 TASK 文档：记录范围与验收。

### 测试结果

- 隔离 MySQL `go test ./internal/seckill/mysqlrepo`：PASS。
- `go test ./...`：PASS。
- `go vet ./...`：PASS。

### 遗留问题

- Purchase 尚未实现，因此 Repository 暂未接入主程序。
