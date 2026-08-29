# TASK-038: 接入异步秒杀 HTTP

## 目标

在 async 模式把 Redis 预留转为持久化 job，并仅在提交后返回 HTTP 202。

## 非目标

- 不直接发送 Kafka、不实现 worker/retry/result GET。

## 允许修改

- `internal/config/config.go`、`config_test.go`
- `internal/seckill/seckill.go`、`seckill_test.go`
- `internal/handler/seckill_order.go`、`seckill_order_test.go`
- `main.go`、`etc/store-api.yaml`

## 实现约束

- OrderMode 仅允许 sync/async；运行期不回退。
- Redis 成功而 EnsureJob 失败时保留资格并返回 503。
- 关键注释解释 202 语义和跨存储窗口。

## 验收标准

- [x] async 返回稳定 order_no/QUEUED/replayed；不执行 Purchase。
- [x] sync 回归保持兼容；并发重放只创建一个 job。

## 验证命令

```bash
go test -race ./internal/config ./internal/seckill ./internal/handler
go test ./...
```

## 回滚点

配置切回 sync；删除 async 装配和响应分支。
