# TASK-004: 用户注册

## 背景

MySQL 与 migration 基础已验证，v0.1 需要首先建立安全、可测试的用户写入路径。注册是后续登录和 RBAC 的前置能力，但本任务只负责创建用户。

## 目标

实现 `POST /v1/auth/register`，规范化邮箱、校验密码并只持久化 bcrypt 哈希。

## 非目标

- 不实现登录、JWT、当前用户信息或角色分配。
- 不实现验证码、找回密码、OAuth 或 Refresh Token。
- 不引入缓存、消息队列或 RPC。
- 不修改已有 migration。

## 允许修改

- `main.go`
- `go.mod`
- `go.sum`
- `internal/user/`
- `internal/handler/register.go`
- `internal/handler/register_test.go`
- `migrations/000002_create_users.up.sql`
- `migrations/000002_create_users.down.sql`
- 本 TASK 文档

## 禁止修改

- `labs/`
- 现有 migration 文件
- 商品、订单、RBAC 模块
- 其他版本 Spec

## 实现约束

- 邮箱去除首尾空格并转换为小写后再持久化。
- 密码长度为 8～72 字节；72 是 bcrypt 的输入上限。
- 响应和日志不得包含明文密码或密码哈希。
- “先查询再插入”不能作为重复注册保护；MySQL 唯一约束是并发下的最终防线。
- MySQL duplicate-key 错误转换为稳定的 `USER_ALREADY_EXISTS`。
- handler 不直接执行 SQL，service 不依赖 MySQL driver。

## 验收标准

- [x] 合法注册返回 HTTP 200 和用户 ID、规范化邮箱。
- [x] 密码以 bcrypt 哈希保存，响应不泄漏密码或哈希。
- [x] 非法邮箱、短密码和超过 bcrypt 上限的密码返回 400。
- [x] 重复邮箱返回 HTTP 409 和 `USER_ALREADY_EXISTS`。
- [x] 并发重复注册最多只有一个数据库写入成功。
- [x] migration 可 up、重复 up、down、再 up。
- [x] 单元测试、MySQL 集成测试、全量 race 和 vet 通过。

## 验证命令

```bash
go run ./cmd/migrate -f etc/store-api.yaml up
SERVICE_RPC_MYSQL_TEST_DSN='...' go test ./internal/user/...
go test ./internal/handler
go test -race ./...
go vet ./...
```

## 回滚点

先回退 migration 000002，再恢复本任务代码和依赖。回退会删除 `users` 表及其中的本地测试数据，执行前必须确认目标数据库。

## 完成记录

### 修改文件

- `main.go`：装配用户 repository、service 和注册路由。
- `internal/user/user.go`：定义用户领域值、repository 边界和注册用例。
- `internal/user/user_test.go`：验证邮箱规范化、bcrypt 哈希和密码边界。
- `internal/user/mysqlrepo/repository.go`：实现 MySQL 写入和 duplicate-key 映射。
- `internal/user/mysqlrepo/repository_test.go`：验证并发重复邮箱最多一个成功。
- `internal/handler/register.go`：实现注册 HTTP 协议与稳定错误响应。
- `internal/handler/register_test.go`：验证成功、冲突和敏感字段不泄漏。
- `migrations/000002_create_users.*.sql`：创建/回退 users 表及唯一约束。
- `go.mod`、`go.sum`：加入 bcrypt 依赖。
- 本 TASK 文档：记录范围与验证结论。

### 测试结果

- migration `up → no change → down → up`：PASS。
- `SERVICE_RPC_MYSQL_TEST_DSN=... go test ./internal/user/... ./internal/handler`：PASS。
- 本地 API 注册：PASS，邮箱规范化并返回 HTTP 200；重复注册返回 HTTP 409 和 `USER_ALREADY_EXISTS`。
- 数据库检查：PASS，密码前缀为 bcrypt `$2a$`，与明文不相等。
- `SERVICE_RPC_MYSQL_TEST_DSN=... go test -race -timeout 120s ./...`：PASS。
- `go vet ./...`：PASS。

### 遗留问题

- 注册成功后尚未自动授予角色；该行为由 RBAC TASK 定义。
- 测试库保留一个 `task004-e2e-20260822@example.com` 验收用户，用于后续登录验证。
