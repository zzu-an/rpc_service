# TASK-069: 多进程生命周期与 backlog 诊断

## 背景
v0.5 增加多个 RPC、orchestrator、relay 和 consumer 进程；若启动/关闭顺序、在途任务和 backlog
不明确，故障测试会产生假成功或丢消息。

## 目标
统一各进程优雅退出，并提供 Stream PEL、Order Outbox、Kafka 双 consumer-group lag、RPC endpoint
和在途处理的只读诊断。

## 非目标
- 不引入 Docker Compose/Kubernetes、Prometheus 或自动伸缩。
- 不把全部 RPC、MySQL、Redis、Kafka 或 etcd 复制成集群。
- 不自动排空/删除 DLQ。

## 允许修改
- 各 `cmd/*` 生命周期装配
- `cmd/v05check/*`
- 本地 15/17 实例启动、停止脚本或 Makefile 目标
- Makefile 诊断目标
- lifecycle/diagnostic 测试与文档

## 禁止修改
- 业务 schema/契约/核心事务。

## 实现约束
- gateway/RPC 先停止接新请求，再等待在途。
- Stream orchestrator、Outbox relay 和 Kafka consumers 先停止拉取，再有限等待；未确认任务保留给
  PEL/Outbox/offset 重投。
- 诊断只读，输出稳定分类和计数，不打印凭据/完整消息。
- 中文注释解释为何 shutdown timeout 后不能假装消息已完成，以及 consumer 数为何不等于有效并行度。

## 验收标准
- [x] SIGTERM 下无新消息被拉取，已完成消息确认，未完成消息可恢复。
- [x] 诊断能显示 etcd endpoint、PEL、Outbox backlog、两个 consumer group 的 Kafka lag、
  reservation/order 差值。
- [x] 日常 15 实例拓扑可重复启动/停止；治理拓扑中 order-rpc×2、notification-consumer×2。
- [x] kill/慢化一个 order-rpc 后服务发现和有界失败符合 Spec；停止一个 notification consumer 后
  partition 可 rebalance。
- [x] 不执行 FLUSH、topic delete 或自动业务修复。

## 验证命令
```bash
go test -race ./cmd/...
make verify-lifecycle-v05
```

## 回滚点
恢复各进程原启动器；保留 backlog 数据。

## 完成记录（2026-08-29）

- `cmd/v05check` 只读输出六个 etcd service endpoint、逐 item Stream PEL、Outbox pending/claimed、projector/notification group lag、reservation/order 数与 `reserved_without_order`。
- `scripts/v05-apps.sh` 提供 daily 11 应用与 governance 13 应用的 list/start/stop；加上四个基础设施分别是 15/17 实例。SIGTERM 后最多等待 10 秒，未确认消息保留在 PEL/Outbox/Kafka offset。
- 冷启动实际发现并修复 seckill-rpc 内嵌配置字段冲突：业务 Redis 改名 `QueueRedis`，避免被 go-zero 的 RedisKey 配置抢占。
- 全新临时 MySQL/Redis/Kafka/etcd 上，11 应用启动、诊断、优雅停止成功；13 应用治理拓扑启动成功。停止第二个 order-rpc 与 notification consumer 后，主实例继续存活，etcd 诊断只剩健康 order endpoint。
- 所有测试进程和临时数据已清理，默认端口释放；cmd 全量 race、vet、只读/无自动修复扫描与 diff check 通过。
