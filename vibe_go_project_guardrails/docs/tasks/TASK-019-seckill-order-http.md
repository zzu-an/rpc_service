# TASK-019: 接入同步秒杀下单 HTTP API

## 背景

活动管理和三种库存策略已经完成，需要提供用户入口并把秒杀模块接入主程序。

## 目标

实现认证用户同步下单接口，返回订单与 replayed 标记，并接入管理和购买路由。

## 非目标

- 不增加策略选择配置。
- 不实现异步下单、查询排队结果或 Redis/MQ。
- 不修改 schema。

## 允许修改

- `main.go`
- `internal/handler/seckill_order.go`
- `internal/handler/seckill_order_test.go`
- 本 TASK 文档

## 验收标准

- [x] user_id 只来自认证上下文。
- [x] 成功和幂等重放均返回订单，replayed 语义明确。
- [x] 不可用、售罄和竞争繁忙使用不同错误码。
- [x] 主程序接入管理和购买路由，默认原子更新。
- [x] handler、全量测试、race 和 vet 通过。

## 验证命令

```bash
go test ./internal/handler
go test -race ./...
go vet ./...
```

## 回滚点

移除秒杀路由接线和新增 handler；数据库及 Repository 保留但不可从 HTTP 访问。

## 完成记录

### 修改文件

- `main.go`：装配默认原子库存 Repository 和秒杀路由。
- `internal/handler/seckill_order.go`：用户购买入口和错误映射。
- `internal/handler/seckill_order_test.go`：身份、重放和错误测试。
- 本 TASK 文档：记录范围与验收。

### 测试结果

- `go test ./internal/handler`：PASS。
- `go test -race -timeout 120s ./...`：PASS。
- `go vet ./...`：PASS。

### 遗留问题

- 100/1000 并发和真实 API 阶段验收由 TASK-020 完成。
