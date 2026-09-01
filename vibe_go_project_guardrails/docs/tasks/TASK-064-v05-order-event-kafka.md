# TASK-064: order.created Kafka 契约与运行时

## 背景
Kafka 应服务真实跨服务事件，而不是无条件插入 Redis Stream 与 RPC 之间。

## 目标
建立版本化 `order.created.v1`、可靠 publisher/consumer、独立 consumer group、retry/DLQ 基础。

## 非目标
- 不实现 Outbox relay 或具体消费者。
- 不实现 Stream→Kafka Bridge。
- 不恢复 `seckill_order_jobs`。

## 允许修改
- `internal/platform/mq/*`
- `internal/order/events/*`
- Kafka 配置
- `go.mod`、`go.sum`
- 对应测试

## 禁止修改
- Redis Stream runtime、业务 repository、HTTP handler。

## 实现约束
- event_id 稳定且 key=order_no；字段只包含下游需要的最小订单事实。
- retry/DLQ 必须先确认目标 topic，再允许提交源 offset。
- 不同 consumer group 均收到事件；同 group 才做分区负载均衡。
- 中文注释解释 fan-out、offset 非幂等标记、partition/consumer 并行度和消息脱敏。

## 验收标准
- [x] 编解码、未知版本、event_id、attempt 测试通过。
- [x] 两个真实 consumer group 均收到同一测试事件。
- [x] retry/DLQ 和 poison event 行为通过。
- [x] 边界扫描不存在 Stream→Kafka Bridge 和旧 job runtime。

## 完成记录

- 新增最小化 `order.created.v1` JSON 契约，event_id 由 `event_type + version + order_no` 确定性生成，Kafka key 固定为 order_no；未知版本明确作为 poison event。
- Kafka runtime 使用 franz-go、AllISR ack、默认幂等 producer、禁用自动 offset 提交；retry/DLQ 必须收到 broker ack 后才提交源 offset。
- attempt 使用消息 header 且复制时不修改源消息；永久错误直接 DLQ，临时错误在 MaxAttempts 内 retry，offset 明确不作为业务幂等键。
- `make verify-kafka-v05` 已建立；真实 Kafka 4.3 KRaft + 2 partition topic 下，projector/notification 两个独立 consumer group 均收到同一事件，Stream→Kafka Bridge 与旧 job 扫描通过。

## 验证命令
```bash
go test -race ./internal/platform/mq/... ./internal/order/events/...
make verify-kafka-v05
```

## 回滚点
移除事件运行时和依赖；订单/Stream 路径尚未接入。
