# TASK-008A: 订单 schema、事务 repository 与用例

## 背景

商品已可公开查询。v0.1 需要一个基础订单证明服务端金额计算、历史快照和本地事务正确性，但库存竞争必须留给 v0.2。

## 目标

建立订单/明细表，实现按有效 SKU 创建订单、保存快照，并按当前用户查询订单。

## 非目标

- 不查询、锁定或扣减库存。
- 不实现支付、关闭、状态机、幂等或重复提交保护。
- 不实现 HTTP 路由。

## 允许修改

- `internal/order/`
- `migrations/000005_create_orders.up.sql`
- `migrations/000005_create_orders.down.sql`
- 本 TASK 文档

## 禁止修改

- `main.go`
- `internal/handler/`
- 商品、用户、RBAC 模块
- 已有 migration

## 实现约束

- 客户端输入只包含 SKU ID 和数量；价格从 MySQL 有效 SKU 读取。
- 订单头和全部明细在一个本地事务内写入。
- 任一 SKU 无效时不产生订单头或部分明细。
- 保存商品名、SKU 名、SKU code、单价、数量和小计快照。
- 数量 1～100，同一请求不允许重复 SKU。
- 用户只能按自己的 user ID 查询；越权与不存在统一为 `ORDER_NOT_FOUND`。
- 代码、SQL 和表中禁止库存语义。

## 验收标准

- [x] migration 可 up、重复 up、down、再 up。
- [x] 总金额完全由数据库 SKU 单价计算。
- [x] 多明细任一无效时完整回滚。
- [x] 商品改名/改价不影响订单快照。
- [x] 其他用户不能读取订单。
- [x] MySQL 集成、全量 race、vet 和库存扫描通过。

## 验证命令

```bash
go run ./cmd/migrate -f etc/store-api.yaml up
SERVICE_RPC_MYSQL_TEST_DSN='...' go test ./internal/order/...
go test -race ./...
go vet ./...
```

## 回滚点

先回退 migration 000005，再删除 order 代码。回退会删除本地订单测试数据。

## 完成记录

### 修改文件
- `internal/order/order.go`：定义基础订单、输入边界和用户归属查询。
- `internal/order/order_test.go`：验证数量、重复 SKU 和空订单输入。
- `internal/order/mysqlrepo/repository.go`：实现服务端价格读取、快照和本地事务。
- `internal/order/mysqlrepo/repository_test.go`：验证金额、快照、越权和多明细回滚。
- `migrations/000005_create_orders.*.sql`：创建/回退订单与明细表，无库存字段。
- 本 TASK 文档：记录范围与验证结论。

### 测试结果
- migration `up → no change → down → up`：PASS。
- `SERVICE_RPC_MYSQL_TEST_DSN=... go test ./internal/order/...`：PASS。
- `SERVICE_RPC_MYSQL_TEST_DSN=... go test -race -timeout 120s ./...`：PASS。
- `go vet ./...`：PASS。
- migration 与订单 SQL 库存语义扫描：PASS，无匹配。

### 遗留问题
- v0.1 不提供幂等保证；重复请求可能创建多笔订单。
- 库存正确性、锁和防超卖属于 v0.2。
- HTTP 接入属于 TASK-008B。
