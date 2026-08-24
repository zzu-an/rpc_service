# TASK-009: v0.1 阶段验收与文档收口

## 背景

v0.1 的用户、认证、RBAC、商品与基础订单任务已经逐项实现，需要用独立测试数据库执行最终质量门禁，并留下可重复的本地运行步骤。

## 目标

提供单一阶段验证入口，完成 v0.1 Spec 的最终验收记录。

## 非目标

- 不新增或修改业务 API。
- 不修改 schema、依赖或架构。
- 不实现库存、Redis、MQ、RPC、etcd 或任何 v0.2 能力。

## 允许修改

- `Makefile`
- `docs/v0.1-acceptance.md`
- `internal/platform/database/mysql_test.go`（仅允许移除测试库名硬编码）
- 本 TASK 文档

## 禁止修改

- `main.go`、除上述测试外的 `internal/`、`migrations/`
- `go.mod`、`go.sum`
- 当前 v0.1 Spec

## 实现约束

- 阶段验证入口必须要求显式传入隔离的 MySQL 测试库 DSN，不能静默跳过集成测试。
- 验收记录必须包含 migration、race、vet、真实 API 与禁用技术扫描的结果。
- 本地运行步骤不得依赖 Docker Compose。

## 验收标准

- [x] 独立空测试库可依次应用全部 migration，再次执行为安全无操作。
- [x] `make verify-v01 TEST_DSN=...` 同时运行 MySQL 集成测试、race 与 vet。
- [x] 核心 API 主链路通过真实本地端口验证。
- [x] 商城业务未引入后续阶段技术或库存语义。
- [x] 验收与本地运行步骤已记录。

## 验证命令

```bash
make verify-v01 TEST_DSN='service_rpc:service_rpc_dev@tcp(127.0.0.1:3307)/service_rpc_test?charset=utf8mb4&parseTime=true&loc=UTC'
```

## 回滚点

删除新增文档，并恢复 `Makefile` 中新增的阶段验证 targets；不涉及业务代码与数据库 schema 变更。

## 完成记录

### 修改文件

- `Makefile`：新增快速测试、race、vet 与必须显式提供隔离测试 DSN 的 `verify-v01` 门禁。
- `docs/v0.1-acceptance.md`：记录本地启动方式、阶段门禁、已验收链路和下一阶段问题。
- `internal/platform/database/mysql_test.go`：移除开发库名硬编码，按传入 DSN 验证实际连接库名。
- 本 TASK 文档：固定最终验收证据。

### 测试结果

- 独立 `service_rpc_test`：首次 `up` PASS；再次 `up` 返回 `no change`；版本为 `5 dirty=false`。
- 单步回退与恢复：`5 -> 4 dirty=false -> 5` PASS。
- `make verify-v01 TEST_DSN=...`：PASS；MySQL 集成测试、全部包 race 与 `go vet ./...` 全部通过。
- 不提供 `TEST_DSN`：按设计 FAIL，并提示必须使用隔离测试数据库。
- 真实 API：注册/登录/当前用户、实时 RBAC、商品管理与公开查询、订单创建与归属查询均在对应 TASK 通过本地端口验收。
- 最终路由清单与 v0.1 Spec 一致；后续技术/库存扫描仅命中三处明确声明“无库存”的注释。
- Docker 容器 `service-rpc-mysql`：healthy，映射 `127.0.0.1:3307 -> 3306`。

### 遗留问题

- v0.1 不证明并发库存正确性、请求幂等或数据库热点承载能力；这些是 v0.2 及后续里程碑的问题。
- 本地配置仅用于学习与开发，进入任何共享环境前必须替换数据库密码和 JWT Secret。
