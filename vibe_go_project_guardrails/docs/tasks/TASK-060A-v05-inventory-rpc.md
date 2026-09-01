# TASK-060A: inventory-rpc

## 背景
秒杀 item、冻结商品快照和 MySQL reservation 需要形成独立库存数据边界。

## 目标
实现 inventory-rpc server/client，覆盖 item 创建、快照读取和幂等库存预留。

## 非目标
- 不管理 Redis 准入、活动或订单。
- 不消费 Stream/Kafka，不释放 reservation。

## 允许修改
- `cmd/inventory-rpc/*`
- inventory v1 IDL 及生成代码
- inventory 领域/RPC adapter
- inventory-rpc 配置与测试

## 禁止修改
- order/user repository、gateway handler、Redis Stream runtime。

## 实现约束
- AddItem 通过 product-rpc 读取并冻结 SKU snapshot，不直接查商品表。
- Reserve RPC 以 order_no 为幂等键，同 key 不同载荷返回冲突。
- server 只装配 item/reservation repository 和 product RPC client。
- 中文注释解释冻结快照、唯一键和 RPC timeout 后为何不能盲目回补。

## 验收标准
- [x] inventory SQL 不访问 users/products/orders 表。
- [x] 重复 Reserve 100 次只减一次库存。
- [x] product-rpc 不可用时 AddItem 有界失败，不伪造 snapshot。

## 验证命令
```bash
go test -race ./internal/seckill/... ./cmd/inventory-rpc/...
```

## 回滚点
停止 inventory-rpc；不自动修改已有 reservation。

## 完成记录

### 修改文件

- `internal/seckill/inventory_item.go`、`mysqlrepo/item.go` 及测试
- `internal/seckill/inventoryrpc/*`、`inventoryclient/*`
- `cmd/inventory-rpc/*`、`etc/inventory-rpc.yaml`

### 测试结果

- product snapshot 冻结与真实 MySQL 落库：PASS，SQL 不访问 product/users/orders。
- 重复 Reserve 100 次：PASS，只扣一次。
- product 依赖阻塞：30ms context 内返回 DeadlineExceeded，repository 未被调用。
- inventory RPC DTO/错误映射、配置子预算、race/vet：PASS。
- 补充 `ListActivityItems` 只读契约，供 seckill-rpc 预热读取 inventory 自有数据，避免跨服务 SQL。

### 遗留问题

- activity_id 是 seckill-service 外部引用；本任务按冻结决策不跨库校验活动表。
- timeout 后禁止回补库存；调用方只能用同 order_no 与同载荷查询/重试。
