# TASK-035: 建立 Kafka 运行时基础

## 目标

提供可校验、可确认发布、可关闭且不泄露凭据的 Kafka 配置和适配器。

## 非目标

- 不定义业务消息、不启动消费者、不改 HTTP。

## 允许修改

- `internal/config/config.go`、`config_test.go`
- `internal/platform/mq/kafka.go`、`kafka_test.go`（新增）
- `etc/store-api.yaml`、`go.mod`、`go.sum`

## 实现约束

- broker/topic/group/超时/消费者并发显式配置；非法值启动失败。
- 生产接口接收 key/value，只在 broker ack 后返回 nil。
- 关键中文注释解释 ack 结果未知与为什么客户端必须关闭。

## 验收标准

- [x] 默认开发 broker 为 `192.168.0.107:9092`。
- [x] 配置/发布单测通过，真实 Kafka 测试由显式地址启用。

## 验证命令

```bash
go test -race ./internal/config ./internal/platform/mq
go vet ./...
```

## 回滚点

删除 mq 包和 Kafka 配置/直接依赖，业务尚未接入。
