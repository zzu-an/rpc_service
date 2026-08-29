# TASK-051: Stream 诊断与双模式压测口径

## 目标
记录 Stream length、PEL、DLQ、排空时间，并让压测报告区分同步与 Stream 模式。

## 允许修改
- `cmd/loadtest/*`
- `cmd/v042check/*`
- `docs/v0.4.2-*`

## 验收标准
- [x] 诊断只读且不打印凭据。
- [x] 报告不把入口 202 当订单成功。
- [x] 不在无数据时宣称性能提升。

## 验证命令
```bash
go test -race ./cmd/loadtest ./cmd/v042check
```
