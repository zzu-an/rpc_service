# Mutex、Atomic 与数据竞争

## 1. 解决什么问题？

比较互斥锁和原子操作在共享状态同步中的语义与代价。Atomic 适合独立计数/标志；Mutex 更适合多个字段或“检查再修改”的复合不变量。

## 2. 错误写法会怎样？

`UnsafeCounter` 的 `value++` 是读、加、写三步，并发执行会丢更新，且根据 Go 内存模型结果未定义。把复合业务规则拆成多个 atomic 操作也可能逻辑竞态。

## 3. 怎么检测？

```bash
go run -race ./labs/mutex-vs-atomic -unsafe
go test -bench=. -benchmem ./labs/mutex-vs-atomic
```

Race detector 找正确性问题；benchmark 只比较特定机器和竞争度下的成本，不能证明业务语义正确。

## 4. 生产环境如何处理？

- 先选择最容易证明正确的同步方案，再用 profile/benchmark 决定是否优化。
- Mutex 临界区要短，不在持锁时做网络或磁盘 I/O。
- 共享的读写都必须采用同一种同步纪律。
- 锁竞争用 mutex profile；阻塞用 block profile；不要凭感觉把锁换成 atomic。
