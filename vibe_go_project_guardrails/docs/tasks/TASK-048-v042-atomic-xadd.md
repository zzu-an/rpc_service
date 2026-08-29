# TASK-048: Redis Lua 原子预扣与 XADD

## 目标
同一 Lua 原子写 state、buyers、results 和 item Stream，重复用户不重复入队。

## 允许修改
- `internal/seckill/redisgate/*`
- `internal/seckill/seckill.go`

## 验收标准
- [x] 所有 key 共享 `{item:<id>}`。
- [x] 并发库存和消息数不变量通过真实 Redis 测试。
- [x] Stream order_no 可安全恢复 itemID。

## 验证命令
```bash
go test -race ./internal/seckill ./internal/seckill/redisgate
```
