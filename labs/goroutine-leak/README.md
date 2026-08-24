# Goroutine 泄漏

## 1. 解决什么问题？

理解 goroutine 也需要明确的所有权和退出协议。`Generator` 允许消费者通过 context 表达“我不再读取”，生产者因此能退出。

## 2. 错误写法会怎样？

`LeakyGenerator` 在消费者提前离开后永久阻塞在发送。请求不断进入时，泄漏的 goroutine、引用对象和相关连接都会累积。

## 3. 怎么检测？

```bash
go run ./labs/goroutine-leak -mode=leak -count=100
go run ./labs/goroutine-leak -mode=fixed -count=100
```

错误模式会打印 goroutine profile。生产中结合 goroutine 数趋势、`/debug/pprof/goroutine?debug=1`、阻塞栈和压测前后基线判断；单看某一刻的数量不够。

## 4. 生产环境如何处理？

- 启动 goroutine 的代码要说明由谁取消、由谁等待。
- channel 由拥有发送权且知道“不会再发送”的一方关闭，接收方通常通过 context 请求停止。
- channel send/receive、锁、网络调用和 timer 等所有阻塞点都必须能退出。
- 使用 `WaitGroup`/`errgroup` 汇合生命周期；长期后台任务挂在进程级 context 下。
