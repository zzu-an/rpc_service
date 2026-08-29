# TASK-047: 冻结 v0.4.2 Stream 契约

## 目标
接受 ADR-004，冻结模式、key/slot、消息、PEL、DLQ、结果查询和可靠性边界。

## 允许修改
- `vibe_go_project_guardrails/{ROADMAP.md,ARCHITECTURE.md}`
- `vibe_go_project_guardrails/docs/{specs,decisions,tasks}`

## 验收标准
- [x] 明确 `async-stream`，Kafka 对照隔离在独立分支。
- [x] 明确不新增 schema、不宣称 exactly-once。
- [x] 明确同 item hash tag 与单热点限制。

## 验证命令
```bash
git diff --check
```
