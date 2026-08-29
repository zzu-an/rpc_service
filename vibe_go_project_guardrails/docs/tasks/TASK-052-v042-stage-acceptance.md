# TASK-052: v0.4.2 阶段验收

## 目标
建立强制远程 MySQL/Redis 的 `verify-v042`，验证原子入队、PEL、幂等和 Kafka 隔离边界。

## 允许修改
- `Makefile`
- `internal/seckill/streamqueue/*_test.go`
- `docs/v0.4.2-acceptance.md`

## 验收标准
- [x] 全量 race、vet、边界和 diff 通过。
- [x] 真实 Redis/MySQL Stream 端到端通过。
- [x] Kafka 生产代码、配置、migration 和 Go 依赖均不存在。

## 验证命令
```bash
make verify-v042 TEST_DSN='...' TEST_REDIS_ADDR='...'
```
