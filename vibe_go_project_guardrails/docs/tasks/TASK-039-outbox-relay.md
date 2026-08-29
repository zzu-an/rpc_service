# TASK-039: 实现 Outbox Relay

## 目标

把到期 pending job 可靠发布到 Kafka 主 topic，并记录成功或退避重试。

## 非目标

- 不消费业务消息、不做 retry/DLQ。

## 允许修改

- `internal/seckill/mq/relay.go`、`relay_test.go`（新增）
- `internal/seckill/mysqlrepo/job_repository.go`、测试

## 实现约束

- broker ack 后才标记 PUBLISHED；标记失败允许重复发布。
- 发布失败使用有界指数退避，循环受 context 控制。
- 条件更新不能覆盖 SUCCEEDED/FAILED。

## 验收标准

- [x] pending 可发布；失败可再次到期。
- [x] 注入 ack 后标记失败会重复发布但不丢 job。

## 验证命令

```bash
go test -race ./internal/seckill/mq ./internal/seckill/mysqlrepo
```

## 回滚点

停止并删除 relay；pending job 保留可恢复。
