# TASK-043: 装配 v0.4 Worker 与优雅退出

## 目标

用独立进程启动 relay、主/retry 消费者，并在信号到达时有限等待在途任务。

## 非目标

- 不拆业务微服务、不引入 RPC/etcd/容器编排。

## 允许修改

- `cmd/seckill-worker/main.go`、`main_test.go`（新增）
- `internal/seckill/mq/runtime.go`、`runtime_test.go`（新增）
- 必要的配置装配文件

## 实现约束

- 停止顺序：停止拉取→取消 relay→等待在途→关闭 producer/DB。
- 超时未完成消息不得伪装确认，允许重启重投。
- API/worker 共享领域和数据库，但不是 RPC 服务拆分。

## 验收标准

- [x] SIGTERM 后不接新消息，在途任务有上限地结束。
- [x] 未完成任务重启后可重新处理。

## 验证命令

```bash
go test -race ./cmd/seckill-worker ./internal/seckill/mq
go test ./...
```

## 回滚点

停止 worker；sync 模式仍可运行。
