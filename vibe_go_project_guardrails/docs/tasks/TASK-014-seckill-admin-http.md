# TASK-014: 实现秒杀管理 HTTP Handler

## 背景

活动领域和持久化能力已经存在，需要冻结并测试管理端 HTTP 转换与错误语义。

## 目标

实现创建活动、添加商品和修改状态的 handler 与受保护路由注册函数。

## 非目标

- 不接入 `main.go`。
- 不实现用户购买接口。
- 不实现 Purchase Repository。

## 允许修改

- `internal/handler/seckill_admin.go`
- `internal/handler/seckill_admin_test.go`
- 本 TASK 文档

## 验收标准

- [x] 时间字段严格按 RFC3339/RFC3339Nano 解析并在领域层转 UTC。
- [x] 管理路由组合认证和 `seckill:write` 权限。
- [x] path、JSON 和领域错误映射明确。
- [x] handler、全量测试和 vet 通过。

## 验证命令

```bash
go test ./internal/handler
go test ./...
go vet ./...
```

## 回滚点

删除新增 handler 文件；主程序未接线，不影响已有路由。

## 完成记录

### 修改文件

- `internal/handler/seckill_admin.go`：管理端请求、响应、路由和错误映射。
- `internal/handler/seckill_admin_test.go`：handler 单元测试。
- 本 TASK 文档：记录范围与验收。

### 测试结果

- `go test ./internal/handler`：PASS。
- `go test ./...`：PASS。
- `go vet ./...`：PASS。

### 遗留问题

- 主程序接线等待 Purchase Repository 完成。
