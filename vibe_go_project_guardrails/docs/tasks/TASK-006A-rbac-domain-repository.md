# TASK-006A: RBAC schema、repository 与用例

## 背景

JWT 已能提供用户身份，但身份不等于权限。需要先用 MySQL 建立角色、权限和关联关系，并提供原子角色替换及实时权限查询，再由独立 HTTP TASK 接入授权。

## 目标

建立 RBAC 四表和 application service，支持查询用户角色、原子替换角色及检查权限码。

## 非目标

- 不增加 HTTP 路由或中间件。
- 不把角色或权限写入 JWT。
- 不增加 Redis 或进程内权限缓存。
- 不实现多租户或行级权限。

## 允许修改

- `internal/rbac/`
- `migrations/000003_create_rbac.up.sql`
- `migrations/000003_create_rbac.down.sql`
- 本 TASK 文档

## 禁止修改

- `main.go`
- `internal/handler/`
- `internal/auth/`
- 已有 migration
- 其他业务模块

## 实现约束

- 角色和权限使用稳定 code，展示名称可变。
- `(user_id, role_id)`、`(role_id, permission_id)` 必须唯一。
- 角色替换在一个 MySQL 本地事务内完成；角色集合无效时原集合保持不变。
- 权限每次从 MySQL 查询，不加入缓存。
- 初始化 `admin`、`customer`、`rbac:manage`、`product:write`；admin 拥有两个权限。
- service 不依赖 MySQL driver。

## 验收标准

- [x] migration 可 up、重复 up、down、再 up。
- [x] 可以原子替换用户角色并按稳定顺序读取。
- [x] 不存在角色导致冲突错误且不会清空原角色。
- [x] admin 权限关系可查询，customer 不自动拥有管理权限。
- [x] MySQL 集成测试、全量 race 和 vet 通过。

## 验证命令

```bash
go run ./cmd/migrate -f etc/store-api.yaml up
SERVICE_RPC_MYSQL_TEST_DSN='...' go test ./internal/rbac/...
go test -race ./...
go vet ./...
```

## 回滚点

先回退 migration 000003，再删除本任务新增代码。回退会删除 RBAC 关联和种子数据，必须仅对确认的本地测试库执行。

## 完成记录

### 修改文件

- `internal/rbac/rbac.go`：定义 RBAC repository 边界和角色/权限用例。
- `internal/rbac/rbac_test.go`：验证角色集合规范化和稳定排序。
- `internal/rbac/mysqlrepo/repository.go`：实现原子角色替换、角色查询和实时权限查询。
- `internal/rbac/mysqlrepo/repository_test.go`：验证合法替换、非法替换回滚和权限关系。
- `migrations/000003_create_rbac.*.sql`：创建/回退 RBAC 四表及最小种子数据。
- 本 TASK 文档：记录范围和验证结论。

### 测试结果

- migration `up → no change → down → up`：PASS。
- `SERVICE_RPC_MYSQL_TEST_DSN=... go test ./internal/rbac/...`：PASS。
- `SERVICE_RPC_MYSQL_TEST_DSN=... go test -race -timeout 120s ./...`：PASS。
- `go vet ./...`：PASS。

### 遗留问题

- 当前 RBAC 尚未接入 HTTP；权限中间件、角色管理接口和 `/users/me` 角色输出属于 TASK-006B。
- 首个管理员仍需由受控测试 fixture 或运维方式授予，不能通过未授权 HTTP 自举。
