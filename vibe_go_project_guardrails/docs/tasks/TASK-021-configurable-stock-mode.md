# TASK-021: 支持启动时选择秒杀库存策略

## 背景

v0.2 已实现原子更新、悲观锁和乐观锁，但正式服务固定使用原子更新，无法在同一 HTTP 服务上做可重复的策略对比。

## 目标

通过 `Seckill.StockMode` 在进程启动时选择 `atomic`、`pessimistic` 或 `optimistic`。

## 非目标

- 不允许客户端逐请求选择策略。
- 不修改 HTTP、schema 或事务语义。
- 不声明任何策略性能更优。

## 允许修改

- `internal/config/config.go`
- `internal/seckill/mysqlrepo/repository.go`
- `internal/seckill/mysqlrepo/repository_test.go`
- `main.go`
- `etc/store-api.yaml`（只追加 Seckill 配置，保留现有 DSN）
- 本 TASK 文档

## 实现约束

- 空配置向后兼容为 atomic。
- 未知配置必须阻止服务启动，防止报告标签与实际策略不一致。
- 服务启动日志必须打印实际策略。

## 验收标准

- [x] 三种合法配置均映射到正确内部枚举。
- [x] 配置解析忽略大小写和首尾空格。
- [x] 未知配置返回错误。
- [x] HTTP 客户端没有策略选择字段。
- [x] 单元测试、全量测试和 vet 通过。

## 验证命令

```bash
go test ./internal/seckill/mysqlrepo
go test ./...
go vet ./...
```

## 回滚点

恢复主程序使用 `seckillmysql.New(db)`，删除 Seckill 配置；默认原子策略行为不变。

## 完成记录

### 修改文件

- `internal/config/config.go`：新增秒杀配置。
- `internal/seckill/mysqlrepo/repository.go`：解析及打印策略枚举。
- `internal/seckill/mysqlrepo/repository_test.go`：配置解析测试。
- `main.go`：按配置装配 Repository。
- `etc/store-api.yaml`：增加默认 atomic 配置。
- 本 TASK 文档：记录范围与验收。

### 测试结果

- `go test ./internal/seckill/mysqlrepo`：PASS。
- `go test ./...`：PASS。
- `go vet ./...`：PASS。

### 遗留问题

- 压测流量生成与报告由 TASK-022 实现。
