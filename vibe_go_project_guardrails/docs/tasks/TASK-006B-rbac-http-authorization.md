# TASK-006B: 权限中间件与角色管理 API

## 背景

RBAC 数据与用例已完成，但 HTTP 目前只验证身份，尚不能区分已登录无权限与有权限用户，也不能通过受保护接口管理角色。

## 目标

实现权限码中间件、管理员角色替换 API，并让当前用户接口返回 MySQL 中的最新角色。

## 非目标

- 不增加权限缓存。
- 不把角色或权限写入 JWT。
- 不实现角色/权限 CRUD 或多租户策略。
- 不修改 schema。

## 允许修改

- `main.go`
- `internal/handler/login.go`
- `internal/handler/login_test.go`
- `internal/handler/rbac.go`
- `internal/handler/rbac_test.go`
- 本 TASK 文档

## 禁止修改

- migrations
- JWT claims
- user service/repository
- 商品和订单模块
- `labs/`

## 实现约束

- 认证先于授权：无有效 JWT 返回 401，身份有效但无权限返回 403。
- 管理接口要求 `rbac:manage`，不得硬编码 `role == admin`。
- 每个请求实时查询 MySQL 权限，不增加缓存。
- 角色替换复用 TASK-006A 的事务用例。
- `/users/me` 的角色来自 MySQL，不来自 Token。

## 验收标准

- [x] 未认证请求角色管理接口返回 401。
- [x] 普通用户返回 403 `PERMISSION_DENIED`。
- [x] 具备 `rbac:manage` 的用户可以原子替换目标用户角色。
- [x] 非法角色返回 409 且原角色不变。
- [x] `/users/me` 返回最新角色，角色变化无需重新签发 Token。
- [x] handler、真实 API、全量 race 和 vet 通过。

## 验证命令

```bash
go test ./internal/handler
go test -race ./...
go vet ./...
```

## 回滚点

恢复入口和 login handler，删除 RBAC HTTP 文件；不涉及数据库 schema 回退。测试 fixture 的管理员关联可从本地数据库删除。

## 完成记录

### 修改文件

- `main.go`：装配 RBAC repository/service 并注册授权路由。
- `internal/handler/login.go`：当前用户实时读取 MySQL 角色。
- `internal/handler/login_test.go`：验证角色输出不再来自固定值。
- `internal/handler/rbac.go`：实现权限码中间件和角色替换 API。
- `internal/handler/rbac_test.go`：覆盖 403 和授权成功场景。
- 本 TASK 文档：记录范围和验证结论。

### 测试结果

- `go test ./internal/handler`：PASS。
- 真实 API：admin 角色替换返回 HTTP 200，普通用户返回 HTTP 403。
- 真实 API：同一 JWT 在角色更新后 `/users/me` 立即由 `[admin]` 变为 `[admin, customer]`。
- `SERVICE_RPC_MYSQL_TEST_DSN=... go test -race -timeout 120s ./...`：PASS。
- `go vet ./...`：PASS。

### 遗留问题

- 首个管理员仍通过受控本地 fixture 授予；未增加可被滥用的 HTTP 自举后门。
- 测试库保留 `task006b-customer-20260822@example.com` 和管理员角色 fixture，供后续商品权限验收使用。
