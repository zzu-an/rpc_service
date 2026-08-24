# TASK-007B: 商品公开与管理 API

## 背景

商品用例和 MySQL 实现已验证，现在需要按 v0.1 Spec 暴露公开查询与受权限保护的管理接口。

## 目标

实现商品列表/详情、创建、基础信息更新和上下架 API。

## 非目标

- 不实现 SKU 编辑、库存、图片上传、搜索或缓存。
- 不新增权限模型；复用 `product:write`。
- 不修改 schema。

## 允许修改

- `main.go`
- `internal/handler/product.go`
- `internal/handler/product_test.go`
- 本 TASK 文档

## 禁止修改

- migrations
- product service/repository
- JWT/RBAC 实现
- 订单模块

## 实现约束

- 公开接口不需要 JWT，但只返回已上架商品。
- 管理接口必须依次通过 JWT 和 `product:write`。
- `price_cent` 使用 JSON 整数。
- handler 只做协议转换和错误映射，不执行 SQL。
- 分页复用 service 的默认值和最大值。

## 验收标准

- [x] 公开列表和详情只返回已上架商品。
- [x] 管理员可以创建、更新和上下架。
- [x] 普通用户写商品返回 403。
- [x] 金额以整数分返回。
- [x] handler、真实 API、全量 race 和 vet 通过。

## 验证命令

```bash
go test ./internal/handler
go test -race ./...
go vet ./...
```

## 回滚点

恢复 `main.go` 并删除商品 handler 文件；不涉及 schema 回退。

## 完成记录

### 修改文件
- `main.go`：装配 product repository/service 并注册路由。
- `internal/handler/product.go`：实现公开查询与受 `product:write` 保护的管理接口。
- `internal/handler/product_test.go`：验证创建、详情和整数金额序列化。
- 本 TASK 文档：记录范围与验证结论。

### 测试结果
- `go test ./internal/handler`：PASS。
- 真实 API：创建商品状态为 inactive，公开详情返回 404；上架后返回 200 和 `price_cent=1999`。
- RBAC middleware 的普通用户 403 已由 TASK-006B 真实 API 和当前 handler suite 覆盖。
- `SERVICE_RPC_MYSQL_TEST_DSN=... go test -race -timeout 120s ./...`：PASS。
- `go vet ./...`：PASS。
- handler 库存/浮点字段扫描：PASS，无匹配。

### 遗留问题
- 本地测试库保留已上架商品 ID 3，供订单 TASK 使用。
- SKU 编辑、库存和缓存明确不在 v0.1。
