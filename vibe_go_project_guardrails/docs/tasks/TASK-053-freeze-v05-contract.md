# TASK-053: 冻结 v0.5 契约与 ADR

## 背景
v0.5 从 Redis Stream 分支接入 RPC 与 Kafka，若不先冻结边界，容易误合并 v0.4.1 的旧 job/outbox 路径。

## 目标
评审并冻结 v0.5 Spec、ADR-005、ADR-006、服务/数据所有权和任务依赖图。

## 非目标
- 不生成 Protobuf。
- 不改 Go、SQL、配置或依赖。

## 允许修改
- `vibe_go_project_guardrails/ARCHITECTURE.md`
- `vibe_go_project_guardrails/ROADMAP.md`
- `vibe_go_project_guardrails/docs/specs/v0.5.md`
- `vibe_go_project_guardrails/docs/decisions/ADR-005-v05-rpc-service-boundaries.md`
- `vibe_go_project_guardrails/docs/decisions/ADR-006-v05-kafka-domain-events.md`
- `vibe_go_project_guardrails/docs/tasks/TASK-053-*.md`

## 禁止修改
- 生产代码、测试、migration、`go.mod`、运行配置。

## 实现约束
- 基线必须是 `b4a7b03`；记录 `git merge-base` 证据。
- ADR 必须明确为什么不整分支 merge/cherry-pick `codex/v0.4.1-kafka`。
- 明确 v0.5 可观察但不自动修复 `reserved_without_order`。

## 验收标准
- [x] 两份 ADR 状态改为 Accepted，且无待定服务/消息/一致性语义。
- [x] Spec 只规划 v0.5，没有补偿、对账、系统级限流或完整观测平台。
- [x] TASK-054～070 均能追溯到一个 Spec 验收项。

## 验证命令
```bash
git merge-base HEAD codex/v0.4.2-redis-stream
git diff --check
```

## 回滚点
只回滚 v0.5 规划文档，不触碰 v0.4.2 代码。

## 完成记录

- 基线证据：`git merge-base HEAD codex/v0.4.2-redis-stream` 输出
  `b4a7b038f9799b5a8b0639aa8768babe7234da57`，与当前 HEAD 一致。
- 已冻结 ADR-005：服务边界、逻辑数据所有权、RPC 编排和
  `reserved_without_order` 的失败语义均无待定项。
- 已冻结 ADR-006：Redis Stream 仅作秒杀内部队列；Kafka 仅承载
  `order.created.v1` 跨服务领域事件，不实现 Stream→Kafka Bridge。
- 阶段边界：自动补偿/对账留到 v0.6，系统级背压/限流留到 v0.7，
  完整观测与容器编排留到 v1.0。
- 面试要点：引入中间件不是目标，必须先说明它解决的故障边界；本阶段 Kafka
  解决的是订单事件可靠发布与多下游 fan-out，而不是给秒杀关键路径增加一跳。
