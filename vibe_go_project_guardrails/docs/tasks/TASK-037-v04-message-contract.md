# TASK-037: 实现版本化消息契约

## 目标

实现 v1 JSON 事件的确定性 event_id、编码、解码和严格必填校验。

## 非目标

- 不发布/消费 Kafka、不引入 Protobuf。

## 允许修改

- `internal/seckill/mq/message.go`、`message_test.go`（新增）

## 实现约束

- 未知新增字段可忽略，未知 schema version/event type 必须拒绝。
- reserved_at 使用 UTC 毫秒且不得为零。
- 注释解释 order_no key、分区并发与不要求 item 全序。

## 验收标准

- [x] roundtrip 稳定，坏消息有可分类错误。
- [x] 消息不含商品快照、密码或 Token。

## 验证命令

```bash
go test -race ./internal/seckill/mq
```

## 回滚点

删除尚无生产调用方的消息包。
