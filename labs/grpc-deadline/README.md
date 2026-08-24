# 原生 gRPC、流与 Deadline

## 覆盖内容

- `echo.proto` 与 Protobuf 编解码
- unary RPC、server streaming、client streaming
- deadline/cancellation 向服务端传播
- metadata header/trailer
- unary/stream interceptor
- `codes.InvalidArgument`、`codes.Unavailable`、`codes.DeadlineExceeded`
- 客户端和服务端 keepalive 参数
- 带强制超时兜底的 graceful stop

运行测试：

```bash
go test -v ./labs/grpc-deadline
go run ./labs/grpc-deadline -listen=:50051
```

## 1. 解决什么问题？

用强类型契约和 HTTP/2 长连接完成低开销的服务间调用；流式 RPC 允许逐条消费；deadline 把调用方的延迟预算传到下游。

一次调用路径是：业务对象 → Protobuf 编码 → HTTP/2 frame → 服务端解码 → handler → 响应编码 → 客户端解码。`service.go` 是通常由 `protoc-gen-go-grpc` 生成的薄适配层，本实验手写它是为了无需安装系统 `protoc` 也可运行。

## 2. 错误写法会怎样？

- 不设 deadline：慢下游会长期占用 goroutine 和连接。
- 客户端超时后，服务端代码若忽略 context，仍会继续执行并产生副作用。
- 对所有错误重试：非幂等调用可能重复执行，故障时还会形成重试风暴。
- keepalive 过于频繁：增加服务端和网络负担，甚至被代理断开。
- `GracefulStop` 无超时：坏连接/长流可能阻止进程退出。

## 3. 怎么检测？

- 测试明确断言 gRPC status code、metadata、deadline 和 stream EOF。
- 监控每个 method 的 Rate/Error/Duration，尤其 P95/P99、deadline exceeded、cancelled。
- trace 中检查预算在哪一跳耗尽；观察 active streams、连接数和 goroutine profile。
- 故障注入慢 handler、Unavailable、断连和滚动重启。

## 4. 生产环境如何处理？

- 从入口总预算向下分配更短的每跳 deadline，并在 handler 的阻塞点检查 context。
- 只重试瞬时错误且要求操作幂等，采用指数退避+jitter+重试预算。
- metadata 放 request/trace/auth 等请求域信息；业务字段仍放 message。
- 区分业务状态和传输状态码，保持 Proto 字段号兼容且不复用。
- keepalive 必须与服务端、LB/代理和网络空闲超时协同配置。
