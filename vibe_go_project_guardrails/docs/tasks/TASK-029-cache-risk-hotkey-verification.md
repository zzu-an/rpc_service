# TASK-029: 验证缓存风险与热 key 边界

## 背景

接入 Redis 不等于自动解决穿透、击穿、雪崩和热点。v0.3 需要用测试与 benchmark 证明当前策略能处理什么、仍留下什么，而不是提前堆叠复杂治理组件。

## 目标

为购买准入增加缓存风险测试、单热 item benchmark 和可复现实验说明。

## 依赖

- TASK-027 完成。
- TASK-028 可并行，但其诊断字段可用于报告。

## 非目标

- 不实现系统级限流、熔断、自适应保护或多级缓存。
- 不做 Redis 库存分桶或部署 Cluster。
- 不用本地 sold-out 标记掩盖单 shard 热点。
- 不给出脱离硬件环境的固定 QPS 承诺。

## 允许修改

- `internal/seckill/redisgate/gate.go`（仅修复由测试证明的边界）
- `internal/seckill/redisgate/gate_test.go`
- `internal/seckill/redisgate/cache_risks_test.go`（新增）
- `docs/v0.3-cache-risks.md`（新增）
- 本 TASK 文档

## 实现约束

- 穿透测试必须证明不存在 item 不调用 MySQL Purchase，而不是只证明 Redis 返回 nil。
- 击穿测试必须证明购买路径不懒加载，key 生命周期覆盖活动结束。
- 雪崩测试验证不同 item 的 TTL jitter 有界且可重现，同一 item 的 state/buyers 不分离。
- 大 key 测试验证 buyer field 数不超过 initial stock，重复/售罄用户不增加字段。
- 热 key benchmark 固定单 item，记录并发、脚本耗时和错误；不得把 benchmark 当作稳定生产容量。
- 文档必须准确说明 Redis 命令执行串行语义、Lua 阻塞 shard、Pipeline 不保证原子性，以及 Cluster hash tag 的作用。
- 若发现需要新增生产治理机制，停止实现并记录到 v0.7 候选，不在本 TASK 扩大范围。

## 验收标准

- [x] 穿透、击穿、雪崩和大 key 各有自动化断言。
- [x] 单热 item benchmark 可运行并输出原始基准数据。
- [x] 文档列出当前缓解措施、剩余风险、观测方式和后续阶段归属。
- [x] 无新中间件、限流器、库存分桶或本地缓存。

## 验证命令

```bash
TEST_REDIS_ADDR='127.0.0.1:6379' go test -race ./internal/seckill/redisgate
TEST_REDIS_ADDR='127.0.0.1:6379' go test -run '^$' -bench BenchmarkReserveHotItem -benchmem ./internal/seckill/redisgate
go test ./...
go vet ./...
git diff --check
```

## 回滚点

移除风险测试、benchmark 和说明文档；生产准入逻辑保持不变。

## 完成记录

### 修改文件

- `internal/seckill/redisgate/cache_risks_test.go`：真实 Redis 穿透、TTL、buyers 上界和热 key benchmark。
- `docs/v0.3-cache-risks.md`：当前措施、准确原理、剩余风险及阶段归属。

### 测试结果

- 指定真实 Redis 的风险 race 测试：PASS。
- Apple M4 → 局域网 Redis、200 次单热 item 基准：`5811945 ns/op`、`1065 B/op`、`28 allocs/op`；仅作为当前网络环境基线。
- 全量测试、vet、diff check：PASS。

### 遗留问题

- 完整热点治理留给 v0.7，并以本 TASK 数据作为基线。
