# TASK-026A: 原子发布活动预热快照

## 背景

购买路径不能在缓存 miss 时临时回源，否则活动开始瞬间会发生击穿。HTTP 接入前，先完成可独立测试的预热应用用例和 Redis 原子发布。

## 目标

把合法的 MySQL 活动快照幂等发布为 Redis ready state/buyers，并让活动状态切换按安全顺序关闭旧准入快照。

## 依赖

- TASK-023、TASK-024、TASK-025 完成。

## 非目标

- 不增加 HTTP 路由或修改主程序装配。
- 不允许活动进行中在线重建。
- 不自动启动活动，不改变 MySQL 库存。
- 不实现购买路径。

## 允许修改

- `internal/seckill/seckill.go`
- `internal/seckill/seckill_test.go`
- `internal/seckill/redisgate/gate.go`
- `internal/seckill/redisgate/gate_test.go`
- 本 TASK 文档

## 禁止修改

- `internal/handler/**`
- `main.go`
- `internal/seckill/mysqlrepo/**`
- `migrations/**`

## 实现约束

- 预热只接受“活动 enabled 且 now < start_at”；其他状态返回明确业务错误。
- 每个 item 的 state/buyers 发布必须原子；失败 item 不得暴露半写 ready 状态。
- 同一活动开始前重复预热结果一致，不累减库存、不累加 TTL、不残留旧 generation。
- TTL 延伸到 end_at 后的固定 grace，并对 item 使用确定性有界 jitter；state 和 buyers 必须同生命周期。
- Redis 不保存空 Hash；buyers 可在第一次预留时创建，但 reserve Lua 必须读取 state 的绝对过期时间并立即设置相同 TTL。
- 多 item 可 Pipeline 降低往返，但 Pipeline 不提供事务语义，注释必须明确。
- 部分 item 失败时返回可定位结果，未完成 item 保持 fail closed；禁止用宽泛 key 扫描做回滚。
- 重新启用前先按快照中的精确 item ID 关闭旧 Redis state，再更新 MySQL；关闭活动时先更新 MySQL，再尽力关闭 Redis state。后者即使失效失败也只会让请求到达并被 MySQL 拒绝，不能创建订单。
- 状态切换不能伪装成跨 Redis/MySQL 事务；部分成功必须返回可重试、可诊断错误。
- 关键中文注释解释显式预热、防击穿、状态切换顺序、部分失败、TTL jitter 和活动中禁止重建的原因。

## 验收标准

- [x] 合法活动的全部 item 可发布并返回 item 数与过期信息。
- [x] 重复预热幂等，库存和 buyers 不漂移。
- [x] disabled、已开始、已结束、空活动均返回稳定错误。
- [x] Redis 写入中途失败时，未完成 item 不会被购买脚本视为 ready。
- [x] disable 后 Redis 不再正常放行；失效传播失败时 MySQL 仍拒绝订单。
- [x] re-enable 不复用旧 ready 快照，必须重新预热。
- [x] 除既有活动 status 更新外，不修改 MySQL 库存、订单、schema 和购买链路。

## 验证命令

```bash
TEST_REDIS_ADDR='127.0.0.1:6379' go test -race ./internal/seckill/redisgate ./internal/seckill
go test ./...
go vet ./...
git diff --check
```

## 回滚点

移除预热用例和发布方法；测试 key 按精确命名清理或等待 TTL，不能使用 `FLUSHALL`。

## 完成记录

### 修改文件

- `internal/seckill/seckill.go`、`seckill_test.go`：预热用例、缓存接口及启停安全顺序。
- `internal/seckill/redisgate/gate.go`、`gate_test.go`：单 item 原子发布、TTL jitter、重复预热和精确失效。

### 测试结果

- 领域/redisgate race：PASS。
- 指定真实 Redis 的重复预热、dirty buyers 清除、精确失效：PASS。
- 部分发布失败、TTL 边界、启停顺序单元测试：PASS。
- `go test ./...`、`go vet ./...`、`git diff --check`：PASS。

### 遗留问题

- HTTP 入口与装配由 TASK-026B 完成。
