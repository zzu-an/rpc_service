# TASK-033: 冻结 v0.4 契约与 ADR

## 目标

冻结 Kafka 异步落单的阶段边界、消息/HTTP/schema 契约、失败语义和 TASK 顺序。

## 非目标

- 不修改 Go、SQL migration 或依赖。

## 允许修改

- `docs/specs/v0.4.md`
- `docs/decisions/ADR-003-v04-kafka-async-order.md`（新增）
- `docs/tasks/TASK-033..046`（新增）

## 实现约束

- 明确至少一次而非 exactly-once。
- 自动补偿、支付状态机、RPC、系统级治理继续留在后续版本。
- 注释清单必须成为后续 TASK 的验收项。

## 验收标准

- [x] ADR 已接受且无消息可靠性未决项。
- [x] Spec 包含流程、契约、失败表、测试和阶段扫描。
- [x] 每个后续 TASK 只有一个明确能力。

## 验证命令

```bash
rg -n "Kafka|202|至少一次|DLQ|注释要求|TASK-046" vibe_go_project_guardrails/docs/specs/v0.4.md vibe_go_project_guardrails/docs/decisions/ADR-003-v04-kafka-async-order.md
git diff --check
```

## 回滚点

恢复原 v0.4 草案并删除新增规划文档；生产行为不变。
