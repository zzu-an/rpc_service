# TASK-068: deadline、幂等重试与熔断故障实验

## 背景
“框架内置熔断”不能替代明确预算和故障验证；多层重试还可能放大依赖故障。

## 目标
冻结各链路预算/重试矩阵，验证 timeout、cancel、幂等重试、熔断和服务发现故障行为。

## 非目标
- 不做系统级限流、隔离仓、降级页面或自动扩容。
- 不接完整 metrics/tracing 平台。

## 允许修改
- RPC client policy/config
- 最小故障注入 hook（仅测试/dev build）
- integration/fault tests
- `docs/v0.5-rpc-faults.md`

## 禁止修改
- 业务语义、schema、消息契约。

## 实现约束
- 文档列出 HTTP/worker 总预算、每跳预算、最大尝试数与最坏放大倍数。
- 业务错误不触发熔断；依赖 timeout/unavailable 才计失败。
- retry 必须受剩余 context、次数、backoff+jitter 三重限制。
- 禁止故障时回退本地 repository/直连其他服务 DB。
- 中文注释说明 timeout、retry、breaker 的不同职责及慢调用堆积风险。

## 验收标准
- [x] order-rpc 延迟 3s 时调用按配置 deadline 返回，goroutine 不持续增长。
- [x] inventory 50% 临时失败时尝试数符合矩阵且结果幂等。
- [x] 服务停止、慢实例、etcd 暂时不可用均有可复现结论。
- [x] 售罄等业务错误不会打开熔断。

## 验证命令
```bash
go test -race ./tests/rpcfault/...
make verify-rpc-faults-v05
```

## 回滚点
移除测试故障注入和 client policy；保留默认 zRPC timeout 配置。

## 完成记录（2026-08-29）

- 新增共享 RPC Policy：剩余 context、最大尝试、指数退避+jitter 三重约束；breaker 只统计 DeadlineExceeded/Unavailable，open 后只放一个 half-open 探针。
- seckill orchestrator 的 inventory/order 两个稳定幂等写调用各有独立 policy；普通无幂等键写调用不自动重试。
- `docs/v0.5-rpc-faults.md` 冻结 HTTP/worker/Kafka 预算、尝试数和最坏放大倍数，并明确禁止数据库 fallback。
- 故障测试：3 秒慢 RPC 的 100 次请求均在 20ms deadline 返回且 goroutine 不线性增长；服务停止有界失败；50% 首次 Unavailable 得到严格 150 次尝试/100 个幂等事实；100 次售罄不打开 breaker。
- 真实 etcd + 两个 zRPC 实例完成发现/负载验证，不可达 etcd 在预算内失败；race、vet、fallback 扫描与 diff check 通过。
