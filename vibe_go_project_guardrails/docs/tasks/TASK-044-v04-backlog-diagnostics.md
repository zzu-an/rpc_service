# TASK-044: 增加 Backlog 与一致性诊断

## 目标

提供只读命令输出 job 状态、Kafka lag 和 Redis/MySQL 末态。

## 非目标

- 不在线修复、不接 Prometheus/Grafana、不执行 FLUSH/DELETE。

## 允许修改

- `cmd/v04check/main.go`、`main_test.go`（新增）
- 必要的只读诊断方法和文档

## 实现约束

- 输出声明跨系统读数不是原子快照。
- 密码、DSN、完整 payload 不得输出。
- 不可观测字段必须写 unavailable，不能以替代指标冒充。

## 验收标准

- [x] 输出各 job 状态、最老 pending、partition lag 和订单不变量。
- [x] 缺少依赖返回非零并给出配置提示。

## 验证命令

```bash
go test -race ./cmd/v04check
```

## 回滚点

删除只读命令，不影响生产路径。
