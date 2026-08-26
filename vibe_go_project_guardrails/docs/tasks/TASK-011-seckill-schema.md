# TASK-011: 建立 v0.2 秒杀 Schema

## 背景

v0.1 没有活动、库存或抢购记录，无法用数据库事务验证库存与订单的一致性。

## 目标

创建满足 ADR-001 不变量的秒杀表，并增加管理活动所需权限。

## 非目标

- 不实现 Go 领域模型、Repository 或 HTTP API。
- 不实现库存扣减 SQL。

## 允许修改

- `migrations/000006_create_seckill.up.sql`
- `migrations/000006_create_seckill.down.sql`
- 本 TASK 文档

## 禁止修改

- `main.go`
- `internal/`
- `go.mod`、`go.sum`

## 实现约束

- 秒杀库存必须有数据库非负约束。
- 一个活动不能重复配置同一 SKU。
- 一个用户不能对同一活动商品产生多个成功抢购记录。
- down migration 必须先删除权限关联，再删除权限和业务表。

## 验收标准

- [x] migration 5 → 6 成功。
- [x] migration 6 → 5 → 6 成功且状态非 dirty。
- [x] migration SQL 定义三张表、唯一索引、CHECK 和外键。
- [x] migration 为管理员授予 `seckill:write` 权限。
- [x] 原有测试通过。

## 验证命令

```bash
go run ./cmd/migrate -f etc/store-api.yaml up
go run ./cmd/migrate -f etc/store-api.yaml version
go run ./cmd/migrate -f etc/store-api.yaml down 1
go run ./cmd/migrate -f etc/store-api.yaml up
go test ./...
```

## 回滚点

执行 version 6 的 down migration；会删除尚未承载正式数据的秒杀表和新增权限。

## 完成记录

### 修改文件

- `migrations/000006_create_seckill.up.sql`：新增秒杀 schema 与管理权限。
- `migrations/000006_create_seckill.down.sql`：按依赖顺序回滚。
- 本 TASK 文档：记录范围与验收证据。

### 测试结果

- 隔离测试库 migration `5 → 6 → 5 → 6`：PASS，每次 `dirty=false`。
- `go test ./...`：PASS。
- 开发库未执行回退。

### 遗留问题

- 业务写入由后续 TASK 实现。
