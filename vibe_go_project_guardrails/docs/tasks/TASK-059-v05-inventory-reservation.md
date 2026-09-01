# TASK-059: inventory 幂等预留

## 背景
拆分后不能再用一个事务同时扣库存和建订单，需要先建立独立、可重试的库存事实。

## 目标
实现 `ReserveSeckillStock` 领域能力与 MySQL repository：条件扣减和 reservation 插入在一个本地事务。

## 非目标
- 不调用 order-rpc。
- 不释放 reservation，不做补偿/对账。
- 不修改 Redis 准入 Lua。

## 允许修改
- `internal/seckill/*`
- `internal/seckill/mysqlrepo/*`
- 对应测试

## 禁止修改
- HTTP handler、Kafka runtime、order repository。

## 实现约束
- 首次成功返回冻结 snapshot；同 order_no 同载荷返回原结果。
- 同 order_no 不同 user/item/reserved_at 返回冲突。
- 用户-item 唯一冲突读取赢家，不重复扣库存。
- commit 结果未知时只能使用相同 order_no 重试，禁止盲目加库存。
- 中文注释必须解释两个唯一键各自防什么、为什么 reservation 成功不等于订单成功。

## 验收标准
- [x] 1000 并发/库存 100 不超卖，reservation=100。
- [x] 同消息重复 100 次只减一次库存。
- [x] 注入 commit 结果未知后用相同 order_no 恢复。
- [x] race 和真实 MySQL 集成测试通过。

## 验证命令
```bash
go test -race ./internal/seckill/... ./internal/seckill/mysqlrepo/...
```

## 回滚点
恢复旧 Purchase repository；不自动回补已创建 reservation。

## 完成记录

### 修改文件

- `internal/seckill/inventory.go`、`internal/seckill/inventory_test.go`
- `internal/seckill/mysqlrepo/reservation.go`、`reservation_test.go`

### 测试结果

- 一次性 MySQL 8.4 + v8 schema，race 下 1000 并发/库存 100：100 成功、900 售罄、库存 0、reservation 100。
- 同消息并发重放 100 次：首次 1、replayed 99、只扣一次。
- order_no 载荷冲突、user-item 赢家读取、commit 后响应丢失再用同 order_no 恢复：PASS。

### 遗留问题

- reservation 成功只代表 inventory 本地事实，不代表 order 已创建；跨服务缺口留给诊断和 v0.6。
- 不提供释放库存或删除 reservation API，避免提交结果未知时盲目回补造成超卖。
