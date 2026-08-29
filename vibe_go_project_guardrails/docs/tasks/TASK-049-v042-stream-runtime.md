# TASK-049: Stream 消费、PEL 恢复与 DLQ

## 目标
实现 XREADGROUP、XAUTOCLAIM、成功 ACK/删除、失败计数和 DLQ 原子转交。

## 允许修改
- `internal/seckill/streamqueue/*`
- `internal/seckill/mysqlrepo/repository.go`
- `internal/seckill/seckill.go`

## 验收标准
- [x] commit 前不 ACK，重复处理幂等。
- [x] PEL 可回收，poison message 不永久阻塞。
- [x] 全局消费并发有上限。

## 验证命令
```bash
go test -race ./internal/seckill/streamqueue ./internal/seckill/mysqlrepo ./internal/seckill
```
