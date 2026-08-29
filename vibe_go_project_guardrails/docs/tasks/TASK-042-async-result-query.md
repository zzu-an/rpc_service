# TASK-042: 实现异步结果查询

## 目标

提供仅限所属用户查询的 QUEUED/SUCCEEDED/FAILED 结果接口。

## 非目标

- 不增加进度百分比、支付状态或人工修复入口。

## 允许修改

- `internal/seckill/seckill.go`、`seckill_test.go`
- `internal/seckill/mysqlrepo/job_repository.go`、测试
- `internal/handler/seckill_order.go`、`seckill_order_test.go`

## 实现约束

- 已存在订单优先于滞后的 job 状态。
- 不存在与不属于当前用户统一 404。
- pending/published/retry 对外统一 QUEUED。

## 验收标准

- [x] 三种状态映射正确，SUCCEEDED 包含订单。
- [x] 其他用户不能枚举 order_no。

## 验证命令

```bash
go test -race ./internal/seckill ./internal/handler ./internal/seckill/mysqlrepo
```

## 回滚点

移除 GET 路由；异步处理不受影响。
