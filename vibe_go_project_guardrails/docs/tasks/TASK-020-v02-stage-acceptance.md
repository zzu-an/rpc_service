# TASK-020: v0.2 阶段并发验收与文档收口

## 背景

v0.2 功能已逐项实现，需要用高并发矩阵、race 和边界扫描证明阶段完成。

## 目标

建立单一 `verify-v02` 入口，并记录可重复的 v0.2 验收证据。

## 非目标

- 不引入下一阶段基础设施。
- 不根据当前正确性测试宣称性能提升。
- 不修改业务协议或 schema。

## 允许修改

- `Makefile`
- `internal/seckill/mysqlrepo/purchase_atomic_test.go`（优化 fixture 清理并补齐活动时间窗口验收）
- `internal/seckill/mysqlrepo/stage_v02_test.go`
- `docs/v0.2-acceptance.md`
- 本 TASK 文档

## 验收标准

- [x] 100 并发覆盖原子、悲观、乐观三种策略。
- [x] 默认原子方案通过 1000 并发。
- [x] 同一用户 100 并发只创建一个订单并扣一次库存。
- [x] 门禁要求显式测试 DSN，不能静默跳过集成测试。
- [x] `make verify-v02`、migration 往返和 `git diff --check` 通过。

## 验证命令

```bash
make verify-v02 TEST_DSN='...'
git diff --check
```

## 回滚点

移除阶段测试、Makefile target 和验收文档；业务实现不变。

## 完成记录

### 修改文件

- `internal/seckill/mysqlrepo/stage_v02_test.go`：100/1000 并发和重放风暴测试。
- `internal/seckill/mysqlrepo/purchase_atomic_test.go`：批量清理 fixture，控制大并发测试耗时。
- `Makefile`：v0.2 单一质量门禁与阶段边界扫描。
- `docs/v0.2-acceptance.md`：运行步骤、验证证据和学习结论。
- 本 TASK 文档：记录范围与验收。

### 测试结果

- 隔离测试库 migration `5 → 6 → 5 → 6`：PASS，均为 `dirty=false`。
- `make verify-v02 TEST_DSN=...`：PASS，包含全量 race、100/1000 并发、vet 与技术边界扫描。
- 真实 API 注册、登录、管理权限、商品、活动、首次购买与幂等重放：PASS。
- 验收临时数据清理：PASS，用户、活动和订单剩余计数均为 0。
- `git diff --check`：PASS。

### 遗留问题

- 数据库热点和吞吐瓶颈留给下一 milestone；当前不提前实现。
