# TASK-031: 完成 v0.2/v0.3 同负载对照压测

## 背景

v0.3 的价值不是“用了 Redis”，而是售罄流量减少数据库访问。需要复用同一 HTTP 工作负载，以原始样本和最终状态证明变化。

## 目标

扩展现有 `cmd/loadtest`，生成 mysql admission 与 redis admission 可比较的 JSON 报告。

## 依赖

- TASK-027、TASK-028、TASK-029 完成。

## 非目标

- 不自动修改配置或重启服务。
- 不清理生产数据库或 Redis。
- 不采集 v1.0 才引入的 Prometheus/trace 指标。
- 不根据一次本机结果宣称普适性能提升。

## 允许修改

- `cmd/loadtest/main.go`
- `cmd/loadtest/main_test.go`
- `docs/v0.3-loadtest.md`（新增）
- 本 TASK 文档

## 实现约束

- setup 与正式压测时间分离；Redis 模式 setup 显式调用预热接口并验证服务启动日志中的实际 mode。
- mysql/redis 两轮必须使用相同请求数、并发、库存、用户模型和客户端超时。
- 报告保留逐请求原始样本，并汇总 QPS、P50/P90/P95/P99、业务码、成功数和重放数。
- 报告追加 Redis 结果码/stock/buyers 与 MySQL available stock/claim count。Purchase 精确调用数由 TASK-027 的 spy 测试证明；HTTP 压测没有专用观测点时必须标为 unavailable，不能用 claim 数冒充调用数。
- unique 场景验证售罄过滤，replay 场景验证同用户幂等；两者分开报告。
- 自动准备模式只允许显式隔离测试环境；Redis key 使用唯一 item ID 精确清理，不用 `FLUSHALL`。
- 报告记录硬件、Go 版本、Redis 部署方式、服务配置摘要和原始文件路径，不记录凭据。
- 中文注释解释 strategy/admission 只是报告标签，必须与进程启动日志核对。

## 验收标准

- [x] mysql/redis 两种 admission 报告字段一致、可机器比较。
- [x] Redis 模式 unique 请求远大于库存时，MySQL 首次资格调用不超过库存。
- [x] 最终订单数、DB stock、Redis stock、buyers 满足 spec 不变量。
- [x] replay 场景所有成功响应引用同一订单且 Redis 只扣一次。
- [x] 文档明确列出环境、执行步骤、结果与不能得出的结论。

## 验证命令

```bash
go test -race ./cmd/loadtest
go run ./cmd/loadtest -h
go test ./...
go vet ./...
git diff --check
```

## 回滚点

恢复 v0.2 loadtest 参数和报告结构；保留已生成的原始报告作为历史证据。

## 完成记录

### 修改文件

- `cmd/loadtest/main.go`：增加 admission 标签、Redis 预热 setup、双存储末态、订单标识、脱敏环境摘要和 Redis 错误分类。
- `cmd/loadtest/main_test.go`：覆盖预热顺序、报告字段、订单标识和凭据不泄漏。
- `docs/v0.3-loadtest.md`：记录同负载执行方式、报告判读方法与结论边界。
- `docs/benchmark/v03-mysql-20260827.json`：mysql unique 的 1000 请求原始样本。
- `docs/benchmark/v03-redis-20260827.json`：redis unique 的同负载原始样本。
- `docs/benchmark/v03-redis-replay-20260827.json`：redis 同用户 100 并发原始样本。

### 测试结果

- `go test -race ./cmd/loadtest`：PASS。
- `go run ./cmd/loadtest -h`：PASS，mysql/redis、unique/replay 和环境参数可发现。
- 1000 并发/100 库存与同用户 100 并发的真实双存储不变量由 TASK-027 集成测试验证；HTTP 工具不伪造内部 Purchase 调用数。
- HTTP 对照：mysql unique 897.71 req/s，redis unique 1487.36 req/s；两轮均为 100 成功、900 售罄。该单轮数据只作验收证据，不作容量承诺。
- HTTP replay：1 created、99 replayed，成功样本订单号去重数量为 1，DB/Redis stock=99、claims/buyers=1。

### 遗留问题

- 服务端 CPU、完整可观测性和故障注入平台留给 v1.0。
- 原始 JSON 已记录硬件、Go 版本、Redis 部署说明、脱敏配置和绝对输出路径；重复测试应新建文件，不能覆盖后手改指标。
