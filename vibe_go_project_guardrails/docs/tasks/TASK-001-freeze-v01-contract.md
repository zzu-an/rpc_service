# TASK-001: 冻结 v0.1 契约

## 背景

当前 `docs/specs/v0.1.md` 只描述了阶段方向，没有固定 HTTP API、错误语义、数据边界和认证边界。如果直接开始编码，不同实现任务可能对“基础订单”“管理端权限”等概念作出不一致解释，并可能提前引入库存、Redis 或微服务能力。

## 目标

补全 v0.1 Spec，使后续任务可以在不扩大阶段范围的前提下独立实现和验证。

## 非目标

- 不编写或修改 Go 代码。
- 不引入 go-zero、MySQL driver 或其他依赖。
- 不创建或执行数据库 migration。
- 不实现 Redis、MQ、RPC、etcd、库存或秒杀能力。
- 不规划 v0.2 及后续版本的实现细节。

## 允许修改

- `vibe_go_project_guardrails/docs/specs/v0.1.md`
- `vibe_go_project_guardrails/docs/tasks/TASK-001-freeze-v01-contract.md`

## 禁止修改

- `*.go`
- `go.mod`
- `go.sum`
- `migrations/`
- `labs/`
- 其他版本的 Spec

## 实现约束

- 必须遵守 `ROADMAP.md` 的 v0.1 技术边界。
- API 只覆盖注册、登录、用户信息、RBAC、商品和基础订单。
- JWT 只承担身份认证；Refresh Token 和 Token 主动撤销不属于本任务。
- 基础订单只验证商品并保存价格快照，不检查或扣减库存。
- 金额使用整数分，不使用浮点数。
- 需要明确 401、403 和稳定业务错误码的语义。
- 数据表仅作为后续 migration 的规划契约，本任务不改变数据库。

## 验收标准

- [x] Spec 明确列出 v0.1 的 HTTP API。
- [x] Spec 明确统一响应和错误语义。
- [x] Spec 明确计划中的数据表、关键约束和金额单位。
- [x] Spec 明确 JWT 与 RBAC 的职责边界。
- [x] Spec 明确基础订单不涉及库存。
- [x] Spec 明确 Redis、MQ、RPC、etcd 和后续阶段能力仍被禁止。
- [x] Spec 给出可执行的阶段验收场景和验证命令。

## 验证命令

```bash
rg -n "Redis|MQ|RPC|etcd|库存|金额|401|403|go test" \
  vibe_go_project_guardrails/docs/specs/v0.1.md
go test ./...
go vet ./...
```

## 回滚点

恢复 `docs/specs/v0.1.md` 的任务前版本，并删除本 TASK 文件；不会影响代码、依赖或数据库。

## 完成记录

### 修改文件

- `vibe_go_project_guardrails/docs/specs/v0.1.md`：冻结阶段目标、HTTP API、错误语义、数据契约、认证授权边界和验收场景。
- `vibe_go_project_guardrails/docs/tasks/TASK-001-freeze-v01-contract.md`：记录任务范围、质量门禁和完成结果。

### 测试结果

- `rg -n "Redis|MQ|RPC|etcd|库存|金额|401|403|go test" vibe_go_project_guardrails/docs/specs/v0.1.md`：PASS。
- `GOCACHE=/tmp/service_rpc_gocache go test ./...`：PASS。现有 graceful-shutdown 测试需要绑定本机临时端口，因此在允许该能力后完成验证。
- `GOCACHE=/tmp/service_rpc_gocache go vet ./...`：PASS。

### 遗留问题

- 无 TASK-001 范围内遗留问题。go-zero 服务骨架、依赖版本和运行配置属于后续独立 TASK。
