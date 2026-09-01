# TASK-056: user-rpc

## 背景
注册、登录、当前用户和 RBAC 目前由 HTTP 进程直接访问 user/RBAC repository。

## 目标
实现 user-rpc server/client，并让其独占用户、凭据和 RBAC repository。

## 非目标
- 不迁移 gateway handler；由 TASK-063 完成。
- 不改变 JWT 格式、密码策略或 RBAC 业务语义。

## 允许修改
- `cmd/user-rpc/*`
- user v1 IDL 及生成代码
- `internal/user/*`、`internal/rbac/*` 中的 RPC adapter/必要小改动
- user-rpc 配置与测试

## 禁止修改
- 商品、库存、订单 repository。
- HTTP 路由。

## 实现约束
- 密码 hash 永不出 RPC 响应；认证只返回最小身份事实。
- role/permission error 映射保持稳定，不暴露 SQL。
- server 只装配 user/RBAC repository。
- 关键中文注释解释为什么 gateway 传身份不等于下游可盲信，以及凭据为何不能写日志。

## 验收标准
- [x] 注册并发唯一性、认证、当前用户、角色更新/鉴权 RPC 测试通过。
- [x] deadline/cancel 能到达 repository。
- [x] server 装配不引用其他领域 repository。

## 验证命令
```bash
go test -race ./internal/user/... ./internal/rbac/... ./cmd/user-rpc/...
```

## 回滚点
停止 user-rpc 并移除 adapter；现有单体 HTTP 尚未切换。

## 完成记录

### 修改文件

- `cmd/user-rpc/main.go`、`cmd/user-rpc/main_test.go`
- `internal/user/rpcserver/*`、`internal/user/rpcclient/*`
- `etc/user-rpc.yaml`

### 测试结果

- 12 路同邮箱并发注册：仅 1 次成功，其余稳定映射 AlreadyExists。
- 认证、当前用户、角色替换/查询、权限检查、取消传播：PASS。
- Register/Authenticate 请求体日志屏蔽配置：PASS。

### 遗留问题

- gateway 尚未切换到该 client，旧 HTTP 进程仍直接装配 repository；迁移统一在 TASK-063 完成。
- 真实 MySQL 并发唯一索引继续由既有 repository 集成测试验证，本任务未改变 schema。
