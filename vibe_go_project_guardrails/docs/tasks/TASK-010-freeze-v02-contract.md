# TASK-010: 冻结 v0.2 秒杀契约与库存事务模型

## 背景

v0.2 原始 Spec 只列出了学习目标和验收数字，尚未规定业务接口、数据不变量、事务边界及幂等语义。直接编码会导致不同任务对“成功”和“重复请求”产生不同理解。

## 目标

冻结 v0.2 的 HTTP 契约、业务不变量和库存事务架构，为后续逐任务实现提供唯一依据。

## 非目标

- 不创建数据库表。
- 不注册 HTTP 路由。
- 不编写秒杀业务代码或测试代码。

## 允许修改

- `vibe_go_project_guardrails/docs/specs/v0.2.md`
- `vibe_go_project_guardrails/docs/decisions/ADR-001-v02-inventory-transaction-model.md`
- 本 TASK 文档

## 禁止修改

- `main.go`
- `internal/`
- `migrations/`
- `go.mod`、`go.sum`

## 实现约束

- 不得提前引入 v0.3 及后续技术。
- 必须明确正式链路和教学对比方案的边界。
- 必须说明公共接口、schema 和库存扣减模型的变化。

## 验收标准

- [x] v0.2 HTTP 契约、错误语义和幂等返回已冻结。
- [x] 库存、订单和购买记录的不变量已定义。
- [x] 原子更新、悲观锁和乐观锁的用途已区分。
- [x] ADR 已记录选择、代价与被否决方案。
- [x] 中文关键注释标准已写入 Spec。

## 验证命令

```bash
git diff --check
rg -n "Redis|MQ|RPC|etcd" vibe_go_project_guardrails/docs/specs/v0.2.md vibe_go_project_guardrails/docs/decisions/ADR-001-v02-inventory-transaction-model.md
```

## 回滚点

删除 ADR 和本 TASK 文档，并恢复 v0.2 Spec。

## 完成记录

### 修改文件

- `docs/specs/v0.2.md`：冻结阶段契约、事务不变量、错误语义和注释要求。
- `docs/decisions/ADR-001-v02-inventory-transaction-model.md`：记录库存扣减与本地事务决策。
- 本 TASK 文档：记录范围和验收证据。

### 测试结果

- `git diff --check`：PASS。
- ADR、Spec、TASK 文件存在性检查：PASS。
- 禁用技术扫描只命中阶段边界与被否决方案说明：PASS。

### 遗留问题

- 数据库结构和业务实现由后续 TASK 完成。
