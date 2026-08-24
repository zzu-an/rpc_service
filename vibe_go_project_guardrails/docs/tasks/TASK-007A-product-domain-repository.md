# TASK-007A: 商品 schema、repository 与用例

## 背景

认证和 RBAC 已完成。商品 HTTP 之前需要先固定商品/SKU 业务规则、整数金额及 MySQL 事务边界。

## 目标

建立商品与 SKU 表，实现商品创建、基础信息更新、上下架、公开分页和详情查询用例。

## 非目标

- 不实现 HTTP 路由或权限接入。
- 不实现库存、秒杀价、图片上传、搜索或缓存。
- 不更新 SKU 集合；v0.1 只在创建时写入初始 SKU。

## 允许修改

- `internal/product/`
- `migrations/000004_create_products.up.sql`
- `migrations/000004_create_products.down.sql`
- 本 TASK 文档

## 禁止修改

- `main.go`
- `internal/handler/`
- RBAC、用户和认证模块
- 已有 migration

## 实现约束

- 金额使用 `int64` 整数分和 MySQL `BIGINT`，禁止浮点数。
- 创建商品及全部初始 SKU 必须在一个本地事务中完成。
- 商品至少有一个 SKU；SKU code 唯一，价格非负。
- 公开查询只返回已上架商品和有效 SKU。
- 分页默认 1/20，`page_size` 最大 100。
- 不出现任何库存字段或查询。

## 验收标准

- [x] migration 可 up、重复 up、down、再 up。
- [x] 商品与初始 SKU 原子创建。
- [x] 非法输入不会产生部分数据。
- [x] 公开列表/详情只返回已上架商品。
- [x] 分页边界和整数金额有测试。
- [x] MySQL 集成、全量 race 和 vet 通过。

## 验证命令

```bash
go run ./cmd/migrate -f etc/store-api.yaml up
SERVICE_RPC_MYSQL_TEST_DSN='...' go test ./internal/product/...
go test -race ./...
go vet ./...
```

## 回滚点

先回退 migration 000004，再删除新增 product 代码。回退会删除本地商品测试数据。

## 完成记录

### 修改文件
- `internal/product/product.go`：定义商品/SKU、整数金额、分页与上下架规则。
- `internal/product/product_test.go`：验证 SKU、价格与分页边界。
- `internal/product/mysqlrepo/repository.go`：实现商品/SKU 事务创建和公开查询。
- `internal/product/mysqlrepo/repository_test.go`：验证未上架不可见、上架后详情/列表可见。
- `migrations/000004_create_products.*.sql`：创建/回退商品与 SKU 表，无库存列。
- 本 TASK 文档：记录范围与验证结论。

### 测试结果
- migration `up → no change → down → up`：PASS。
- `SERVICE_RPC_MYSQL_TEST_DSN=... go test ./internal/product/...`：PASS。
- `SERVICE_RPC_MYSQL_TEST_DSN=... go test -race -timeout 120s ./...`：PASS。
- `go vet ./...`：PASS。
- 库存/浮点字段扫描：PASS，无匹配。

### 遗留问题
- SKU 集合只在创建时写入；v0.1 不实现 SKU 编辑和库存。
- HTTP 与 `product:write` 权限接入属于 TASK-007B。
