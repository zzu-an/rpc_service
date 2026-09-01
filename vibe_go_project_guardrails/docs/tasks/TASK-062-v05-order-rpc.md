# TASK-062: order-rpc

## 背景
gateway 的普通订单和 Stream orchestrator 的秒杀订单需要统一进入只拥有订单表的远程服务。

## 目标
实现 order-rpc server/client：普通订单创建/查询、秒杀订单幂等创建、按 user+order_no 查询。

## 非目标
- 不扣库存、不消费 Kafka、不处理支付。
- 不迁移 gateway handler。

## 允许修改
- `cmd/order-rpc/*`
- order v1 IDL 及生成代码
- `internal/order/*` 的 RPC adapter/必要小改动
- order-rpc 配置与测试

## 禁止修改
- inventory/product repository、HTTP 路由、Kafka runtime。

## 实现约束
- 普通订单创建可调用 product-rpc 获取 snapshot，但该非幂等入口默认不自动重试。
- 秒杀创建只接受 inventory 返回的完整 frozen snapshot 和稳定 order_no。
- FindByOrderNo 必须同时校验 user_id，避免可枚举订单号越权。
- server 只装配 order repository 与必要的 product RPC client。
- 中文注释解释普通订单与秒杀订单的不同幂等边界。

## 验收标准
- [x] 现有普通订单行为通过 RPC adapter 保持。
- [x] 秒杀创建重复/载荷冲突/越权查询测试通过。
- [x] order-rpc SQL 不访问 product/inventory/user 表。

## 完成记录

- 普通订单由 order-rpc 调用 product-rpc 获取 active SKU 服务端快照，请求只决定 SKU 与数量；该入口无外部幂等 key，client 不自动重试。
- 秒杀订单只接收 orchestrator 从 inventory reservation 转交的冻结快照，强制数量为 1，并用稳定 `order_no` 调用幂等 repository。
- `FindSeckillOrder` 同时使用 `user_id + order_no`，不存在和越权统一返回 NotFound；进程只装配订单 repository 与 product-rpc client。
- test-verifier：race、vet、Buf 契约/生成漂移、进程依赖与 SQL 所有权扫描、diff check 均通过；真实 MySQL 8.4 的 100 并发重放、异载荷冲突与所有权测试通过。

## 验证命令
```bash
go test -race ./internal/order/... ./cmd/order-rpc/...
```

## 回滚点
停止 order-rpc；尚未切换 gateway/worker。
