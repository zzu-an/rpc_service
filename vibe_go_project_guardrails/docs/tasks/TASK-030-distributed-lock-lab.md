# TASK-030: 完成 Redis 分布式锁实验

## 背景

v0.3 允许学习分布式锁，但秒杀库存不需要用锁包住整个请求。独立实验可以展示正确解锁和租约失效边界，同时避免污染生产链路。

## 目标

实现一个教学用 Redis lease，并用测试复现 token 误删、TTL 过期双持有者和 fencing token 问题。

## 依赖

- TASK-023 的 Redis 基础可复用。
- 可与 TASK-025～TASK-029 并行，但不修改它们的生产代码。

## 非目标

- 不在秒杀、订单或预热链路使用该锁。
- 不实现可直接用于生产的通用锁库。
- 不声称 Redlock 能解决 Redis/MySQL 原子提交。
- 不新增定时续租后台 goroutine。

## 允许修改

- `labs/distributed-lock/README.md`（新增）
- `labs/distributed-lock/lock.go`（新增）
- `labs/distributed-lock/lock_test.go`（新增）
- `labs/README.md`
- 本 TASK 文档

## 实现约束

- 获取使用 `SET key unique-token NX PX ttl`，ttl 必须为正且有上限。
- 释放必须使用 Lua 先比较 token 再删除，禁止 `GET` 后 `DEL` 两条命令。
- 测试必须证明旧持有者不能删除新持有者的锁。
- 另一个测试故意让业务时间超过 TTL，展示两个持有者可先后进入临界区；测试名称和注释不能误称互斥始终成立。
- README 解释进程暂停、网络分区、续租风险、fencing token 需要受保护资源配合，以及 Redlock 的系统假设。
- 关键中文注释解释 token、原子释放和 lease 与所有权的区别。
- 生产目录扫描不得发现对 lab lock package 的引用。

## 验收标准

- [x] 并发获取只有一个即时赢家。
- [x] 错误 token 无法释放锁，旧 token 无法删除新 lease。
- [x] TTL 超时双持有者场景可重复展示。
- [x] README 能回答“锁过期但业务没完成怎么办”和“为什么需要 fencing token”。
- [x] 秒杀生产链路零引用。

## 验证命令

```bash
TEST_REDIS_ADDR='127.0.0.1:6379' go test -race ./labs/distributed-lock
rg 'distributed-lock' internal main.go
go test ./...
go vet ./...
git diff --check
```

## 回滚点

删除 `labs/distributed-lock` 并从 labs 索引移除；生产行为不受影响。

## 完成记录

### 修改文件

- `labs/distributed-lock/lock.go`、测试：SET NX PX、唯一 token、compare-delete Lua 和 TTL 失效实验。
- `labs/distributed-lock/README.md`：lease、续租、fencing 与 Redlock 边界。
- `labs/README.md`：实验索引。

### 测试结果

- 指定真实 Redis 的 race 测试连续 5 轮：PASS。
- 生产目录对 distributed-lock/distributedlock 引用扫描：PASS（零引用）。
- 全量测试、vet、diff check：PASS。

### 遗留问题

- 本实验不升级为生产通用库。
