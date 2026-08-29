# TASK-025: 提供 MySQL 预热快照

## 背景

Redis 不能自行猜测活动时间、状态和库存。预热必须从 MySQL 事实源读取一个明确、可测试的只读快照，同时不能把 SQL 细节泄漏给应用层。

## 目标

为指定活动返回预热所需的活动信息和全部秒杀商品快照。

## 依赖

- TASK-024 已冻结准入边界与 Redis key 契约。

## 非目标

- 不写 Redis。
- 不修改活动或库存。
- 不读取普通订单详情或实现对账。
- 不增加数据库表、索引或 migration。

## 允许修改

- `internal/seckill/seckill.go`
- `internal/seckill/seckill_test.go`
- `internal/seckill/mysqlrepo/repository.go`
- `internal/seckill/mysqlrepo/repository_test.go`
- 本 TASK 文档

## 禁止修改

- `migrations/**`
- `internal/handler/**`
- `main.go`
- `internal/seckill/redisgate/**`

## 实现约束

- 使用独立只读接口表达预热快照，不扩大 `Purchase` 的事务职责。
- 快照至少包含 activity ID/status/start/end 和 item ID/SKU/available stock。
- 查询必须有稳定排序，避免测试和预热报告随机漂移。
- 活动不存在与活动没有商品必须是不同、可诊断的结果。
- 预热资格（已启用、尚未开始）由应用层使用同一次 `now` 判断；SQL 只负责读取事实。
- 不使用 `FOR UPDATE`，不长时间持有事务，不把预热变成库存热点锁。
- 关键中文注释解释为何预热只读 MySQL、为何不锁库存、为何在线活动不允许覆盖 buyers。

## 验收标准

- [x] 返回完整、稳定排序的活动及 item 快照。
- [x] 活动不存在、空活动和查询错误语义明确。
- [x] 读取不会改变库存、版本或订单。
- [x] 现有三种 MySQL Purchase 策略测试保持通过。

## 验证命令

```bash
TEST_DSN='...' go test -race ./internal/seckill/mysqlrepo
go test ./internal/seckill
go vet ./...
git diff --check
```

## 回滚点

移除只读快照接口和查询方法；v0.2 购买链路不受影响。

## 完成记录

### 修改文件

- `internal/seckill/seckill.go`：只读快照模型、接口和空活动错误。
- `internal/seckill/mysqlrepo/repository.go`：短只读 MVCC 事务和稳定排序查询。
- `internal/seckill/mysqlrepo/repository_test.go`：空活动、缺失活动、多 item 排序与库存不变验证。

### 测试结果

- 领域/mysqlrepo 编译测试：PASS。
- 开发库唯一 fixture 的真实 MySQL race 集成及原子/悲观/乐观回归：PASS。
- `go test ./...`、`go vet ./...`、`git diff --check`：PASS。

### 遗留问题

- 独立 `service_rpc_test` 账号当前无访问权限；本次在开发库用唯一 fixture 精确清理完成验证。
- 快照写入 Redis 与 HTTP 入口分别由 TASK-026A、TASK-026B 完成。
