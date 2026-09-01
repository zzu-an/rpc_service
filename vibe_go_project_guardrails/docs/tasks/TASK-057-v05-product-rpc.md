# TASK-057: product-rpc 与冻结快照读取

## 背景
inventory 创建秒杀 item、普通订单创建都需要商品事实，但拆分后禁止直接 JOIN 商品表。

## 目标
实现 product-rpc server/client，并提供当前调用方需要的商品 CRUD、公开查询和 SKU 订单快照读取。

## 非目标
- 不在商品服务中管理秒杀库存或订单。
- 不实现商品缓存。

## 允许修改
- `cmd/product-rpc/*`
- product v1 IDL 及生成代码
- `internal/product/*` 的 RPC adapter/必要小改动
- product-rpc 配置与测试

## 禁止修改
- inventory/order repository。
- HTTP 路由。

## 实现约束
- snapshot 包含稳定 SKU ID、名称、编码、整数分价格和状态。
- 只读查询才允许由上层按 TASK-068 策略重试；写 RPC 不自动重试。
- 中文注释解释 snapshot 是跨服务值对象而不是共享数据库实体。

## 验收标准
- [x] 现有商品领域测试保持通过，RPC CRUD/公开查询契约一致。
- [x] 非激活 SKU 不可被用于新秒杀 item/普通订单。
- [x] product-rpc 只装配 product repository。

## 验证命令
```bash
go test -race ./internal/product/... ./cmd/product-rpc/...
```

## 回滚点
停止 product-rpc；单体路径尚未删除。

## 完成记录

### 修改文件

- product v1 IDL/生成代码（仅追加 description、SKU 与 status 字段）
- `internal/product/product.go`、`internal/product/mysqlrepo/repository.go` 及测试
- `internal/product/rpcserver/*`、`internal/product/rpcclient/*`
- `cmd/product-rpc/main.go`、`etc/product-rpc.yaml`

### 测试结果

- 现有 product 领域/repository 测试：PASS。
- RPC 创建、更新、状态、列表、详情、active SKU snapshot：PASS。
- inactive 商品/SKU snapshot 稳定返回 NotFound；整数分价格保持不变。

### 遗留问题

- v1 初版 `CreateProductRequest.status` 已冻结但不应由创建端控制，现标记 deprecated 并由 server 忽略；
  字段号不能复用，未来 major v2 才能移除。
- 上层只读重试策略留到 TASK-068；client 本任务不暗中自动重试。
