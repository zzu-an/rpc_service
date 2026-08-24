# TASK-005A: 凭证查询与登录用例

## 背景

注册已能安全写入 bcrypt 哈希。登录 API 之前需要先建立与 HTTP/JWT 无关的凭证验证用例，确保不存在用户、错误密码和禁用用户不会泄漏不同的外部语义。

## 目标

实现按规范化邮箱读取凭证、bcrypt 校验和按 ID 查询当前有效用户的 application service。

## 非目标

- 不签发或解析 JWT。
- 不新增 HTTP 路由或中间件。
- 不实现角色、权限或 Refresh Token。
- 不修改 schema。

## 允许修改

- `internal/user/user.go`
- `internal/user/mysqlrepo/repository.go`
- `internal/user/mysqlrepo/repository_test.go`
- `internal/user/auth.go`
- `internal/user/auth_test.go`
- 本 TASK 文档

## 禁止修改

- `main.go`
- `internal/handler/`
- migrations
- 配置和依赖文件

## 实现约束

- 邮箱规范化规则必须与注册一致。
- 不存在用户也执行一次 bcrypt 比较，降低明显的用户枚举时序差异。
- 不存在、错误密码和禁用用户统一返回 `ErrInvalidCredentials`。
- service 不依赖 MySQL driver，MySQL 层负责 `sql.ErrNoRows` 映射。
- 凭证哈希只在 service/repository 边界内流转，不进入 transport 类型。

## 验收标准

- [x] 正确邮箱和密码返回用户。
- [x] 邮箱大小写和首尾空格按注册规则规范化。
- [x] 错误密码、不存在用户和禁用用户返回同一认证错误。
- [x] 按 ID 只能查询有效用户。
- [x] MySQL 集成测试、全量 race 和 vet 通过。

## 验证命令

```bash
SERVICE_RPC_MYSQL_TEST_DSN='...' go test ./internal/user/...
go test -race ./...
go vet ./...
```

## 回滚点

恢复本任务修改的 user/mysqlrepo 文件并删除新增的认证 service 文件；不涉及数据库回退。

## 完成记录

### 修改文件

- `internal/user/user.go`：复用注册与登录一致的邮箱规范化规则。
- `internal/user/auth.go`：新增凭证 repository 边界、认证和当前用户用例。
- `internal/user/auth_test.go`：覆盖正确、错误、不存在、禁用和非法邮箱场景。
- `internal/user/mysqlrepo/repository.go`：实现凭证和有效用户查询。
- `internal/user/mysqlrepo/repository_test.go`：验证真实 MySQL 登录、按 ID 查询和禁用行为。
- 本 TASK 文档：记录范围和验证结论。

### 测试结果

- `go test ./internal/user`：PASS。
- `SERVICE_RPC_MYSQL_TEST_DSN=... go test ./internal/user/...`：PASS。
- `SERVICE_RPC_MYSQL_TEST_DSN=... go test -race -timeout 120s ./...`：PASS。
- `go vet ./...`：PASS。

### 遗留问题

- AuthService 尚未接入 HTTP，也未签发 Token；这是 TASK-005B 的唯一目标。
