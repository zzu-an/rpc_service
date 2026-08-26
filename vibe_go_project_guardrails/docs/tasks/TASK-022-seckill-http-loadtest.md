# TASK-022: 实现秒杀 HTTP 并发压测与 JSON 报告

## 背景

服务端已经能在启动时选择三种库存策略，需要通过相同 HTTP 工作负载记录吞吐、延迟和业务结果。

## 目标

重写 `cmd/loadtest`，自动准备独立测试活动和用户，并输出逐请求样本及汇总 JSON 报告。

## 非目标

- 不自动修改或重启服务端策略。
- 不采集服务端 CPU、MySQL 慢查询或硬件指标。
- 不对任何策略作预设性能结论。
- 不清理生产数据；自动准备模式只能用于隔离测试库。

## 允许修改

- `cmd/loadtest/main.go`
- `cmd/loadtest/main_test.go`
- `docs/v0.2-loadtest.md`
- 本 TASK 文档

## 实现约束

- setup 时间和正式压测时间分开。
- unique 场景每个请求使用独立用户，replay 场景共享一个用户。
- 请求总数、并发、库存、超时和报告路径可配置。
- JSON 必须保留逐请求耗时，便于后续重新统计。
- strategy 只能作为报告标签，文档必须要求与服务端启动日志核对。

## 验收标准

- [x] 自动创建商品、活动、库存和测试用户。
- [x] 支持已有 item + tokens-file。
- [x] 汇总 QPS、结果分类和 min/avg/P50/P90/P95/P99/max。
- [x] JSON 包含逐请求状态、业务码、重放标记和延迟。
- [x] 参数校验阻止明显无效的压测。
- [x] 单元测试、race、全量测试和 vet 通过。
- [x] 对本地模拟 HTTP 服务完成一次工具自测。

## 验证命令

```bash
go test -race ./cmd/loadtest
go test ./...
go vet ./...
go run ./cmd/loadtest -h
```

## 回滚点

恢复原 v0.1 order loadtest，删除新增测试和文档；服务端业务不受影响。

## 完成记录

### 修改文件

- `cmd/loadtest/main.go`：秒杀数据准备、并发请求、统计和报告。
- `cmd/loadtest/main_test.go`：参数、HTTP 请求、统计和报告测试。
- `docs/v0.2-loadtest.md`：三种策略的可重复测试步骤。
- 本 TASK 文档：记录范围与验收。

### 测试结果

- `go test -race ./cmd/loadtest`：PASS。
- `go test -race -timeout 180s ./...`：PASS。
- `go vet ./...`：PASS。
- `go run ./cmd/loadtest -h`：PASS。
- `httptest` 模拟真实秒杀响应，状态、业务码、重放和耗时记录：PASS。

### 遗留问题

- 服务端 CPU、数据库锁等待和资源利用率需要配合外部观测工具分析。
