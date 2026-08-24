# TASK-008B: 订单创建与查询 API

## 背景

订单事务与归属查询已验证，需要把它们暴露给已认证用户，同时确保客户端金额不进入用例。

## 目标

实现登录用户创建基础订单和查询自己的订单详情。

## 非目标

- 不实现库存、支付、订单列表、取消或幂等。
- 不允许管理员越权读取用户订单。
- 不修改 schema。

## 允许修改

- `main.go`
- `internal/handler/order.go`
- `internal/handler/order_test.go`
- 本 TASK 文档

## 禁止修改

- migrations
- order service/repository
- JWT、RBAC、商品模块

## 实现约束

- 两个接口都必须先通过 JWT 身份认证。
- 创建请求只解析 `sku_id` 和 `quantity`，不接受可信价格或总金额。
- 查询必须使用 Token 中的 user ID 和 path order ID。
- 越权和不存在统一返回 404 `ORDER_NOT_FOUND`。
- 响应金额均为整数分和服务端快照。

## 验收标准

- [x] 未认证创建/查询返回 401。
- [x] 客户端伪造金额不会影响订单金额。
- [x] 创建成功返回订单和快照明细。
- [x] 本人可查询，其他用户得到 404。
- [x] handler、真实 API、全量 race、vet 和库存扫描通过。

## 验证命令

```bash
go test ./internal/handler
go test -race ./...
go vet ./...
```

## 回滚点

恢复 `main.go` 并删除订单 handler 文件；不涉及 schema 回退。

## 完成记录

### 修改文件

- `main.go`：装配订单服务并注册订单路由。
- `internal/handler/order.go`：实现创建订单与按归属查询订单的 JWT API。
- `internal/handler/order_test.go`：覆盖身份校验、服务端金额、订单归属与统一 404。
- 本 TASK 文档：记录验收结果。

### 测试结果

- `go test ./internal/handler -run TestOrder -count=1`：PASS。
- `go test -race -timeout 120s ./...`：PASS。
- `go vet ./...`：PASS。
- 真实 API：伪造总额 `1` 被忽略，SKU 单价 `1999`、数量 `2` 得到总额 `3998`；本人查询 200，其他用户查询 404 `ORDER_NOT_FOUND`。
- Redis、MQ、etcd、分布式锁、库存实现扫描：PASS；仅命中两处声明“不实现库存”的注释。

### 遗留问题

- v0.1 按规格不包含库存扣减、支付、取消、订单列表与创建幂等；这些能力留待后续里程碑。
