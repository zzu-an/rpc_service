# 阶段 0：Go 并发与 RPC 实验室

这里的实验不是孤立代码片段。每个目录都包含：

- 可直接运行的 `main.go`
- 可复用的核心实现
- 自动化测试或 benchmark
- `README.md`：问题、错误写法、检测方式、生产方案

## 实验清单

| 实验 | 重点 | 运行 |
| --- | --- | --- |
| `context-cancel` | 超时、主动取消、取消传播 | `go run ./labs/context-cancel` |
| `worker-pool` | 有界并发、背压、取消、panic 隔离 | `go run ./labs/worker-pool` |
| `rate-limiter` | 令牌桶、突发流量、并发安全 | `go run ./labs/rate-limiter` |
| `mutex-vs-atomic` | Mutex、atomic、数据竞争、benchmark | `go test -bench=. ./labs/mutex-vs-atomic` |
| `graceful-shutdown` | 信号、停止接流量、在途请求、资源关闭 | `go run ./labs/graceful-shutdown` |
| `grpc-deadline` | 原生 gRPC、流、deadline、metadata、拦截器 | `go test -v ./labs/grpc-deadline` |
| `goroutine-leak` | 泄漏复现、取消、goroutine profile | `go run ./labs/goroutine-leak -mode=fixed` |
| `distributed-lock` | Redis lease、唯一 token、原子释放、TTL 失效与 fencing | `go test -v ./labs/distributed-lock` |

## 一键验收

从仓库根目录执行：

```bash
make labs-test
make labs-race
make labs-bench
```

`goroutine-leak` 和 `mutex-vs-atomic` 中的错误版本不会被普通测试主动执行；这是为了让正常的 `go test -race ./...` 保持通过。README 中给出了单独复现错误的命令。
