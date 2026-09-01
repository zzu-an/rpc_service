# TASK-067A: notification-service

## 背景
需要一个真实、用户可见的 Kafka 下游证明订单事件 fan-out、幂等消费和独立故障隔离。

## 目标
实现 notification domain/repository/RPC/consumer，消费 order.created 并提供用户站内通知查询/已读。

## 非目标
- 不接真实短信、邮件或推送平台。
- 不允许通知失败回滚订单，不实现通用事件平台。

## 允许修改
- notification migration/domain/repository/RPC/consumer
- `cmd/notification-rpc/*`
- `cmd/notification-consumer/*`
- gateway 通知 handler 与测试

## 禁止修改
- order/inventory 核心事务、Redis Stream 入队语义。

## 实现约束
- event_id 唯一 consumption ledger 与 notification 同一本地事务。
- notification 使用独立 consumer group 和 retry/DLQ。
- 只消费事件最小事实，不查询订单数据库；RPC 按 user_id 校验所有权。
- sender 暂只实现站内通知，不创建无调用方的外部 provider 实现。
- 中文注释解释 fan-out、重复消费、通知失败隔离和 poison event 顺序。

## 验收标准
- [x] 一条 order.created 产生一条站内通知。
- [x] 重复事件 100 次仍只有一条通知。
- [x] consumer 停止只增加自己的 lag，订单 API 正常。
- [x] 用户不能查询或标记他人通知。

## 验证命令
```bash
go test -race ./internal/notification/... ./cmd/notification-rpc/... ./cmd/notification-consumer/...
```

## 回滚点
停止 notification consumer/RPC 并保留 Kafka lag；不修改订单事实。

## 完成记录（2026-08-29）

- migration v10 新增 `notifications` 与 `notification_consumptions`，`event_id` 唯一账本和通知在同一 MySQL 本地事务提交。
- notification consumer 使用独立 group、手动 commit、retry/DLQ；poison event 先确认写入 DLQ 再提交源 offset。
- notification-rpc 和 gateway 新增当前用户列表/已读接口；不存在与他人资源统一映射 NotFound，不能枚举通知 ID。
- 真实 MySQL 8.4 + Kafka 4.3 验证：两分区临时 topic 重复投递 100 次，最终通知/ledger 均为 1；migration up/down/up、race、vet、HTTP 身份传播和数据库所有权扫描通过。
- consumer 进程没有订单 RPC/数据库依赖，停止后只留下本 group offset/lag；订单事务和其他 group 没有同步等待点。
