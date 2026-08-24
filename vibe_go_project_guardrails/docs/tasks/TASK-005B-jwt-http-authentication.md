# TASK-005B: JWT、登录 API 与当前用户

## 背景

凭证验证用例已经独立通过测试，现在需要把它接入 HTTP，并用短期 JWT 在后续请求中传递用户身份。

## 目标

实现登录签发 Access Token、Bearer Token 身份校验和当前用户查询。

## 非目标

- 不实现 Refresh Token、主动撤销、Token 黑名单或多设备会话。
- 不把角色或权限写入 JWT。
- 不实现 RBAC 权限中间件。
- 不修改数据库 schema。

## 允许修改

- `main.go`
- `etc/store-api.yaml`
- `internal/config/config.go`
- `go.mod`
- `go.sum`
- `internal/auth/token.go`
- `internal/auth/token_test.go`
- `internal/handler/login.go`
- `internal/handler/login_test.go`
- 本 TASK 文档

## 禁止修改

- migrations
- 用户注册逻辑
- `labs/`
- 商品、订单和 RBAC 模块

## 实现约束

- JWT 使用 HS256、固定 issuer、`sub=user_id`、签发时间和过期时间。
- JWT 不包含角色或权限，避免权限变更后旧 Token 保留旧授权。
- Secret 至少 32 字节，TTL 必须为正数。
- 缺失、格式错误、签名错误和篡改 Token 返回 401 `UNAUTHENTICATED`。
- 过期 Token 返回 401 `TOKEN_EXPIRED`。
- 登录对不存在用户、错误密码和禁用用户返回相同响应。
- 日志和响应不得输出用户密码、密码哈希或完整 Token（登录成功响应本身除外）。

## 验收标准

- [x] 正确凭证返回短期 Access Token 和有效期。
- [x] 错误凭证统一返回 401 `UNAUTHENTICATED`。
- [x] JWT 只包含标准声明和用户 ID，不包含角色/权限。
- [x] 缺失、篡改和过期 Token 被正确拒绝。
- [x] 有效 Token 可以读取自己的 `/v1/users/me`。
- [x] 单元测试、真实 API 验证、全量 race 和 vet 通过。

## 验证命令

```bash
go test ./internal/auth ./internal/handler
go test -race ./...
go vet ./...
```

## 回滚点

恢复入口、配置和依赖文件，删除本任务新增的 auth 与登录 handler 文件；不涉及数据库回退。

## 完成记录

### 修改文件

- `main.go`：装配 AuthService、TokenManager 和认证路由。
- `internal/config/config.go`、`etc/store-api.yaml`：新增 Access Token Secret 与 TTL 配置。
- `internal/auth/token.go`：实现 HS256 签发、标准声明验证和过期语义。
- `internal/auth/token_test.go`：验证声明白名单、篡改、过期和配置边界。
- `internal/handler/login.go`：实现登录、Bearer 身份中间件和当前用户接口。
- `internal/handler/login_test.go`：覆盖登录、当前用户及无效 Token。
- `go.mod`、`go.sum`：加入 JWT v5 依赖。
- 本 TASK 文档：记录范围和验证结论。

### 测试结果

- `go test ./internal/auth`：PASS。
- `go test ./internal/handler`：PASS。
- 本地 API 登录及 `/v1/users/me`：PASS，返回 900 秒有效期，Token 未写入验证日志。
- `SERVICE_RPC_MYSQL_TEST_DSN=... go test -race -timeout 120s ./...`：PASS。
- `go vet ./...`：PASS。

### 遗留问题

- 当前用户的 `roles` 暂为空数组；角色查询与权限授权属于 TASK-006。
- 本地开发 Secret 存在 YAML 中，仅适合回环开发环境；部署阶段必须外部注入。
