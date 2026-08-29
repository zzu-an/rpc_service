# Redis 分布式锁实验

## 它解决什么问题

`SET key token NX PX ttl` 能在单个 Redis 主节点上提供一个带过期时间的竞争入口。适合保护短小、可容忍租约失效且被保护资源还能做幂等/版本校验的临界区。

它不提供跨 Redis/MySQL 事务，也不保证业务执行期间永远只有一个逻辑持有者。

## 正确释放

token 必须代表一次唯一获取。释放使用 Lua：

```text
if GET(key) == token then DEL(key)
```

不能先 `GET` 再 `DEL`。两条命令之间旧锁可能过期，新持有者已经写入；旧请求随后执行 DEL 会删除新锁。

## TTL 过期但业务未完成

进程 GC pause、调度停顿、网络分区或下游慢请求都可能让业务超过 TTL：

```text
holder A 获取 token=10
  → A 暂停，锁过期
  → holder B 获取 token=11
  → A 恢复并继续写数据
```

此时 Redis 中只有 B 的 token，但 A 和 B 的业务代码可能同时运行。自动续租只能降低概率，不能消除网络分区和进程暂停的不确定性。

## fencing token

更强的做法是每次获取得到单调递增 fencing token，由被保护的数据库/存储记录最后接受的 token，并拒绝更小 token。关键在于“资源端拒绝旧写”，而不是客户端自称仍持有锁。

普通 Redis 随机 token 只能防误删，不能充当 fencing token；要生成并可靠校验单调序号，需要把资源端协议一起设计。

## Redlock 边界

Redlock 尝试在多个独立 Redis 节点取得多数派租约。它依赖时钟漂移、网络时延、节点独立失败和租约时间等假设。即使多数派获取成功，过期旧持有者写入的问题仍需要 fencing/幂等解决。

是否使用 Redlock 取决于故障模型和业务后果，不能把“部署了 5 个 Redis”直接等价为线性一致锁。

## 为什么秒杀生产链路不用它

- Lua 已能原子完成单 item 状态转换。
- 给整个下单接口加锁会把热点请求串行化，放大 P99。
- 锁不能把 Redis 预留与 MySQL 提交合并成事务。
- 超时后自动释放/回补仍可能放出重复资格。

## 运行

```bash
TEST_REDIS_ADDR='...' TEST_REDIS_PASSWORD='...' go test -race ./labs/distributed-lock
```

测试会证明即时竞争只有一个赢家，同时也会故意证明 TTL 过期后可能存在两个逻辑持有者。后者是预期实验结果，不应被“修成永远互斥”。
