# 有界 Worker Pool

## 1. 解决什么问题？

限制昂贵任务的并发数，通过队列和背压保护数据库、RPC 下游以及本进程。

## 2. 错误写法会怎样？

“每个请求启动一个 goroutine”没有容量边界。流量突增时 goroutine、内存、连接数和下游请求同时膨胀；结果 channel 无人消费还会让 worker 永久阻塞。

## 3. 怎么检测？

- 测试实际最大并发是否超过 worker 数。
- 监控队列深度、排队时间、执行时间、拒绝数和 goroutine 数。
- goroutine profile 中检查卡在 channel send/receive 的调用栈。

## 4. 生产环境如何处理？

- worker 数、队列容量必须由下游容量和延迟预算推导。
- 队列满时明确选择阻塞、超时或拒绝，不能无限缓存。
- job、取任务和发送结果都响应 context。
- panic 只能在发生 panic 的 goroutine 内 recover，同时记录堆栈并告警。
