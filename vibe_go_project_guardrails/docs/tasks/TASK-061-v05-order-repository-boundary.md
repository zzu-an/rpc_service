# TASK-061: order repository 去跨域查询与幂等建单

## 背景
当前普通订单/秒杀 Purchase 会在 order/seckill repository 中查询商品与库存表，违反 v0.5 数据所有权。

## 目标
让 order repository 只接收已验证的订单快照，并实现按稳定 order_no 幂等创建秒杀订单。

## 非目标
- 不实现 order-rpc transport。
- 不扣秒杀库存，不查询商品表。
- 不改变支付状态。

## 允许修改
- `internal/order/*`
- `internal/order/mysqlrepo/*`
- 必要的旧 seckill Purchase 调用适配/删除
- 对应测试

## 禁止修改
- Kafka/Redis runtime、HTTP handler、inventory repository。

## 实现约束
- repository 本地事务只写 `orders`、`order_items`。
- 普通订单 snapshot 来自 product-rpc adapter；秒杀 snapshot 来自 inventory reservation。
- 同 order_no 同载荷返回原订单；不同 user/SKU/价格/数量返回冲突。
- 不用“先查再插”替代 `orders.order_no` 唯一键。
- 中文注释解释快照来源信任边界、数据库提交未知和载荷冲突检查。

## 验收标准
- [x] order repository SQL 不访问 product/seckill/user 表。
- [x] 同秒杀创建重复 100 次只有一笔订单及一组 item。
- [x] 伪造同 order_no 不同载荷被拒绝且原订单不变。

## 完成记录

- repository 改为接收上游已验证的冻结快照，本地事务只读写 `orders`、`order_items`，金额全程使用整数分并检查乘法/求和溢出。
- 幂等创建先依赖 `uk_orders_order_no` 竞争唯一赢家；重复 key 会读回订单并比较 user、SKU、名称、价格、数量和小计，异载荷返回 `ErrOrderConflict`。
- test-verifier：race、vet、SQL 所有权扫描、diff check 通过；真实 MySQL 8.4 下 100 并发得到 1 次创建、99 次重放、仅一笔订单/一组 item，四类伪造载荷均未改变原单。

## 验证命令
```bash
go test -race ./internal/order/... ./internal/order/mysqlrepo/...
```

## 回滚点
恢复原 order repository；不得删除已生成订单。
