# TASK-023: 建立 Redis 运行时基础

## 背景

v0.3 首次引入 Redis。业务代码开始使用前，需要先冻结配置、连接生命周期、超时和启动失败语义，避免后续任务各自创建客户端或静默回退 MySQL。

## 目标

提供一个可验证、可关闭、配置非法时启动失败的 Redis 客户端基础设施边界。

## 依赖

- ADR-002 已接受。
- v0.2 验收保持通过。

## 非目标

- 不定义秒杀 key。
- 不执行 Lua、预热或下单。
- 不增加 Redis 健康检查 HTTP 接口。
- 不引入 Redis Cluster、Sentinel、分布式锁或第三方 mock 库。

## 允许修改

- `internal/config/config.go`
- `internal/config/config_test.go`（若不存在可新增）
- `internal/platform/cache/redis.go`（新增）
- `internal/platform/cache/redis_test.go`（新增）
- `etc/store-api.yaml`
- `go.mod` / `go.sum`（仅当现有 go-zero Redis 客户端无法直接使用时）
- 本 TASK 文档

## 禁止修改

- `internal/seckill/**`
- `internal/handler/**`
- `main.go`
- `migrations/**`

## 实现约束

- 配置至少包含地址、认证、DB、连接超时和单次操作超时；所有时间必须有正数上限。
- `Seckill.AdmissionMode` 只允许 `mysql` / `redis`，未知值启动失败；不能根据单次 Redis 错误动态切换。
- 客户端创建必须校验连通性，并向调用方返回错误，不能在基础设施包中 `log.Fatal`。
- 密码不得出现在错误、日志或测试快照。
- 关键中文注释解释“为什么 Redis 模式不能静默回退 MySQL”和“为什么客户端超时必须小于 HTTP 总预算”。
- 优先复用当前 go-zero 依赖；若必须新增依赖，先停下并更新 ADR 理由。

## 验收标准

- [x] 合法 mysql/redis 模式解析正确，大小写和空白处理有明确契约。
- [x] redis 模式缺少地址、超时非法或连接失败时返回可诊断错误。
- [x] mysql 模式不强制连接 Redis。
- [x] 客户端关闭安全且不会泄漏凭据。
- [x] 未改业务路由、schema 或秒杀事务。

## 验证命令

```bash
go test -race ./internal/config ./internal/platform/cache
go test ./...
go vet ./...
git diff --check
```

## 回滚点

移除 Redis 配置和 cache 基础设施目录，恢复 v0.2 配置；业务行为未接入 Redis。

## 完成记录

### 修改文件

- `internal/config/config.go`、`internal/config/config_test.go`：Redis 配置、准入模式和校验。
- `internal/platform/cache/redis.go`、`redis_test.go`：客户端创建、PING、关闭和凭据防泄漏。
- `etc/store-api.yaml`：开发 Redis 与 redis admission 配置。
- `go.mod`、`go.sum`：将 go-zero 已使用的 go-redis/v9 提升为直接依赖。
- ADR-002：记录直接客户端依赖的生命周期理由。

### 测试结果

- 配置/cache 包 race：PASS。
- 指定真实 Redis 的认证、PING 与 Close 集成测试：PASS。
- `go test ./...`：PASS（需要允许本机测试绑定临时端口）。
- `go vet ./...`、`git diff --check`：PASS。

### 遗留问题

- Redis key、Lua 和业务装配由后续 TASK 完成。
