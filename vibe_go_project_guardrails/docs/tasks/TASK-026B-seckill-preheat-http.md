# TASK-026B: 暴露管理端活动预热接口

## 背景

预热核心能力已经可测试，需要通过现有认证/RBAC 管理面显式触发，并在生产进程中装配同一个 Redis 客户端。

## 目标

增加 `POST /v1/admin/seckill/activities/:activityId/preheat` 并完成预热依赖装配。

## 依赖

- TASK-026A 完成。

## 非目标

- 不修改 Lua 或 MySQL 快照查询。
- 不增加新中间件、定时预热或后台 goroutine。
- 不切换用户购买路径。

## 允许修改

- `internal/handler/seckill_admin.go`
- `internal/handler/seckill_admin_test.go`
- `main.go`
- 本 TASK 文档

## 禁止修改

- `internal/seckill/mysqlrepo/**`
- `internal/seckill/redisgate/**`
- `migrations/**`
- 其他业务 handler

## 实现约束

- 复用现有 authenticate + `seckill:write` 权限，不创建新中间件。
- 成功响应延续 code/message/data/request_id，并至少包含 activity ID、item 数和过期时间范围。
- 领域/基础设施错误必须映射为稳定、无内部地址泄漏的 HTTP 错误。
- mysql admission mode 可以不初始化业务 Redis gate，但调用预热接口必须明确返回不可用，不能 panic。
- Redis 客户端生命周期与 MySQL 一样在 main 中显式创建和关闭。
- 中文注释解释为什么预热属于管理面、为什么不能在用户请求 miss 时自动触发。

## 验收标准

- [x] 管理员可预热，普通用户和无 Token 请求被拒绝。
- [x] 合法/重复/过晚/Redis 失败响应可区分。
- [x] HTTP payload 与现有项目风格一致。
- [x] main 在 redis 模式正确装配并安全关闭客户端。
- [x] 用户购买路径仍未切换。

## 验证命令

```bash
go test -race ./internal/handler
go test ./...
go vet ./...
git diff --check
```

## 回滚点

移除预热路由与 main 装配；核心预热能力仍可独立测试。

## 完成记录

### 修改文件

- `internal/handler/seckill_admin.go`、`seckill_admin_test.go`：预热路由、payload、错误映射和认证/RBAC 测试。
- `main.go`：进程级 admission mode、Redis 客户端/gate 生命周期和缓存服务装配。

### 测试结果

- handler race（含无 Token、无权限、管理员）：PASS。
- 主程序编译、全量测试、vet、diff check：PASS。
- 真实配置启动已成功初始化 MySQL 与 Redis并输出 redis admission；监听阶段因本机 `127.0.0.1:8888` 已被其他进程占用而退出。

### 遗留问题

- 用户下单仍走 v0.2；TASK-027 才切换准入路径。
