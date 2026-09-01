# TASK-065: Order Outbox 与 Relay

## 背景
订单提交后 best-effort 发送 Kafka 会在进程崩溃或 broker 故障时永久丢失下游事件。

## 目标
让 order-rpc 在订单本地事务中写 Outbox，并由独立 relay 可靠发布 `order.created.v1`。

## 非目标
- 不发布秒杀 command，不实现 Stream Bridge。
- 不修改支付状态或实现通用 CDC 平台。

## 允许修改
- 新增 order outbox migration
- `internal/order/*`、`internal/order/mysqlrepo/*`
- `cmd/order-outbox-relay/*`
- relay 配置与测试

## 禁止修改
- Redis Stream/inventory/notification repository。

## 实现约束
- order/order_items/outbox 必须同事务，普通和秒杀订单统一产生事件。
- relay 只扫描到期未发布事件；broker ack 后条件更新 published。
- ack 后、标记前崩溃允许重复发布，不允许先标记再发。
- Outbox payload 版本化，错误只保存稳定分类。
- 中文注释解释本地事务边界、重复窗口和为什么 Outbox 不是全局事务。

## 验收标准
- [x] Kafka 停止时订单成功且 Outbox 保留，恢复后发布。
- [x] broker ack 后注入崩溃可重复事件但不丢事件。
- [x] migration up/down/up、Outbox 并发领取和优雅退出通过。

## 完成记录

- migration v9 新增 order-owned Outbox；订单、明细与版本化事件 payload 在一个 MySQL 本地事务提交，order-rpc 不连接 Kafka。
- relay 使用短租约 + `FOR UPDATE SKIP LOCKED` 领取，网络发布在事务外；broker ack 后才按 worker lease 条件标 published。
- ack 后、标记前崩溃会让同 event_id 重发；broker 失败增加 attempts/next_attempt 并只保存低基数 `KAFKA_PUBLISH_FAILED` 分类。
- `make verify-order-outbox-v05` 已建立；真实 MySQL 8.4 migration up/down/up、10 事件双 worker 不重叠领取/租约恢复、真实 Kafka 4.3 恢复发布与 backlog 清零均通过。

## 验证命令
```bash
go test -race ./internal/order/... ./cmd/order-outbox-relay/...
make verify-order-outbox-v05
```

## 回滚点
停止 relay并保留未发布 Outbox；不得删除或伪造 published 状态。
