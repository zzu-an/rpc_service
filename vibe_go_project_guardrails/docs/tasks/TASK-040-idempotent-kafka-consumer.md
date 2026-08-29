# TASK-040: 实现幂等 Kafka 消费

## 目标

消费 v1 消息并使用 reserved_at 调用现有 Purchase，重复消息返回同一订单。

## 非目标

- 不实现 retry/DLQ；错误先返回上层。

## 允许修改

- `internal/seckill/seckill.go`、`seckill_test.go`
- `internal/seckill/mq/consumer.go`、`consumer_test.go`（新增）
- `internal/seckill/mysqlrepo/job_repository.go`、测试

## 实现约束

- 数据库提交成功后才标记 SUCCEEDED；迟到 job 状态不影响订单事实。
- 重投依赖 orders/claim 唯一约束，不能以 offset/内存 map 代替。
- 注释解释 reserved_at 与提交/offset 崩溃窗口。

## 验收标准

- [x] 重复处理 100 次仅一笔订单。
- [x] backlog 跨活动结束仍按预留时刻落单。

## 验证命令

```bash
go test -race ./internal/seckill ./internal/seckill/mq ./internal/seckill/mysqlrepo
```

## 回滚点

停止消费者并删除适配器；job/消息保留。
