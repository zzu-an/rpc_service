# TASK-011A: 修正健康路由真实端口测试

## 背景

全量验证发现生产代码注册 `/health`，真实端口测试却请求 `/healthz`，导致基线测试稳定返回 404。

## 目标

让真实端口测试请求已经冻结的 `/health` 路由。

## 非目标

- 不修改生产路由。
- 不修改健康检查语义。

## 允许修改

- `internal/handler/health_test.go`
- 本 TASK 文档

## 验收标准

- [x] `TestRegisteredHealthRoute` 请求 `/health`。
- [x] handler 测试和全量测试通过。

## 验证命令

```bash
go test ./internal/handler
go test ./...
```

## 回滚点

恢复测试 URL；不会影响生产代码。

## 完成记录

### 修改文件

- `internal/handler/health_test.go`：使真实端口测试与注册路由一致。
- 本 TASK 文档：记录基线修复。

### 测试结果

- `go test ./internal/handler`：PASS。
- `go test ./...`：PASS。

### 遗留问题

- 无。
