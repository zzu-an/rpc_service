# TASK-050: async-stream 配置、HTTP 与 worker 装配

## 目标
增加 `sync`/`async-stream` 两种订单模式和独立 Stream worker，保持现有 HTTP 契约。

## 允许修改
- `internal/config/*`
- `main.go`
- `cmd/seckill-stream-worker/*`
- `etc/store-api.yaml`
- `internal/handler/seckill_order*`

## 验收标准
- [x] async-stream 原子入队后返回 202，生产代码不包含 MySQL job。
- [x] 结果查询订单优先、Redis 状态兜底。
- [x] 旧 async 值兼容为 async-stream，async-kafka 在本分支被拒绝。

## 验证命令
```bash
go test -race ./internal/config ./internal/handler ./cmd/seckill-stream-worker
```
