# TASK-060B: seckill-rpc

## 背景
秒杀活动、预热、Redis Lua+Stream 和结果投影属于秒杀边界，不应与 MySQL inventory repository 混装。

## 目标
实现 seckill-rpc server/client，覆盖活动管理、预热、原子入队和结果状态读取。

## 非目标
- 不直接扣 MySQL 库存或创建订单。
- 不消费 Kafka，不实现 Stream orchestrator。

## 允许修改
- `cmd/seckill-rpc/*`
- seckill v1 IDL 及生成代码
- `internal/seckill/*` 的 RPC adapter/必要小改动
- seckill-rpc 配置与测试

## 禁止修改
- inventory/order repository、gateway handler、Kafka runtime。

## 实现约束
- HTTP 202 的业务边界仍是 Lua+XADD 成功，不等待后续 RPC/Kafka。
- seckill-rpc 只装配活动 repository 与 Redis，不获得订单数据权限。
- 结果读取提供 Redis 投影；订单事实优先逻辑由 gateway/结果 adapter 组合。
- 中文注释解释 Stream 是服务内部队列、Redis 202 语义及为何本阶段不加 Bridge。

## 验收标准
- [x] v0.4.2 Lua 1000/100 和同用户重放语义通过 RPC 路径保持。
- [x] seckill-rpc 不导入 order/inventory repository。
- [x] Redis 不可用时有界失败且不回退 MySQL。

## 完成记录

- seckill-rpc 仅装配 `seckill_activities` repository、Redis gate 和 inventory-rpc client；活动商品查询通过 inventory RPC 契约完成。
- Redis Stream 仍是秒杀服务内部队列，Lua 原子完成资格预扣、buyer 幂等标记、结果初始化与 XADD；RPC 返回 QUEUED 不代表订单落库。
- 结果 RPC 只读 Redis 投影且不伪造 `order_id`；gateway 在 TASK-063 组合 order-rpc 事实优先语义。
- test-verifier：全量相关包 race、vet、Buf lint/breaking/生成漂移、边界扫描、真实 MySQL 8.4 活动读写，以及真实 Redis 8 的 1000/100、100 次重放和 SUCCEEDED 投影均通过。

## 验证命令
```bash
go test -race ./internal/seckill/... ./cmd/seckill-rpc/...
```

## 回滚点
停止 seckill-rpc；v0.4.2 基线分支仍保留原单体路径。
