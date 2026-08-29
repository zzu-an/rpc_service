# TASK-041: 实现 Kafka Retry 与 DLQ

## 目标

对消费错误分类并可靠转交 retry/DLQ，避免毒消息永久阻塞。

## 非目标

- 不做支付延迟任务、自动补库存或人工修复 UI。

## 允许修改

- `internal/seckill/mq/consumer.go`、测试
- `internal/seckill/mq/retry.go`、`retry_test.go`（新增）
- `internal/seckill/mysqlrepo/job_repository.go`、测试

## 实现约束

- 目标 topic 发布确认前不得确认源消息。
- 超限时先发 DLQ，再标记 FAILED；重复 DLQ 可接受。
- 错误日志和 last_error_code 不保存原始敏感文本。

## 验收标准

- [x] 可重试错误 attempt 递增，超限进入 DLQ。
- [x] poison message 进入 DLQ 后正常消息继续处理。

## 验证命令

```bash
go test -race ./internal/seckill/mq ./internal/seckill/mysqlrepo
```

## 回滚点

停止消费者；源消息不被错误确认，待修复后重放。
