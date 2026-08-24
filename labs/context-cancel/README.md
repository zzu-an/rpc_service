# Context 取消传播

## 1. 解决什么问题？

让客户端断开、上游取消或 deadline 到期后，数据库/RPC/计算任务尽快停止，释放 goroutine、连接和内存。

## 2. 错误写法会怎样？

`WrongProcess` 接收了 context 却不检查它，`time.Sleep` 期间无法退出。真实服务中会形成“请求已经失败，后台工作仍在累积”的幽灵任务。

## 3. 怎么检测？

- 测试取消后是否在有限时间内返回。
- 观察超时数与 goroutine 数是否一起持续上涨。
- 查看 `go tool pprof http://host/debug/pprof/goroutine`。
- `errors.Is(err, context.DeadlineExceeded)` 区分 deadline 与业务错误。

## 4. 生产环境如何处理？

- `context.Context` 放第一个参数，不保存到长期存活的 struct。
- 每一层透传同一个 context；派生 context 后立刻 `defer cancel()`。
- 所有阻塞点都监听 `ctx.Done()`，数据库和 RPC 使用带 context 的 API。
- context 只携带请求域元数据，不携带可选业务参数。
