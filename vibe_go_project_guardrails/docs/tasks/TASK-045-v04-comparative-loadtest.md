# TASK-045: v0.3/v0.4 同负载对照压测

## 目标

比较 sync 与 async 的入口延迟、Purchase 峰值并发、lag 和排空时间。

## 非目标

- 不为压测改生产正确性，不伪造固定 QPS 容量承诺。

## 允许修改

- `cmd/loadtest/main.go`、`main_test.go`
- `docs/v0.4-loadtest.md`（新增）
- `docs/benchmark/v04-*.json`（真实运行新增）

## 实现约束

- 相同硬件/数据/库存/并发；原始样本保留。
- 明确总 SQL 可能增加，结论只针对重事务峰值和入口延迟。
- lag/排空不可测时标 unavailable。

## 验收标准

- [x] 两模式最终订单正确，async Purchase 峰值受配置约束。
- [x] 报告包含环境、原始 JSON 与边界说明。

## 验证命令

```bash
go test -race ./cmd/loadtest
```

## 回滚点

删除新增场景和报告，生产行为不变。

## 完成记录（2026-08-29）

- 相同 1000 请求/100 并发/100 库存真实运行完成；sync 与 async 最终均为 100 单、900 售罄。
- async 100 个 202 全部 SUCCEEDED，排空 4532.38ms，无 FAILED/PENDING/poll error。
- 原始 JSON 与口径说明见 `docs/benchmark/v04-*.json`、`docs/v0.4-loadtest.md`。
