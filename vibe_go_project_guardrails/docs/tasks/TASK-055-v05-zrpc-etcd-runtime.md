# TASK-055: zRPC、etcd 与配置基础

## 背景
四个 RPC 服务需要统一但最小的启动、发现、deadline 和错误分类基础，不能各自复制不一致配置。

## 目标
建立 zRPC server/client、etcd 配置校验、RPC 错误映射和受 context 约束的调用预算基础。

## 非目标
- 不实现具体业务 RPC。
- 不实现业务重试策略或故障注入。
- 不接 Prometheus/OpenTelemetry。

## 允许修改
- `internal/config/*`
- `internal/platform/rpc/*`
- `etc/*-rpc.yaml`
- `go.mod`、`go.sum`
- 对应单元测试

## 禁止修改
- 业务 repository、HTTP 路由、Kafka/Stream runtime。

## 实现约束
- 每个进程只加载所需配置；空 etcd key/host、非正 timeout 必须启动失败。
- helper 不得用 `context.Background()` 替换调用方 context。
- 业务错误与依赖错误使用稳定分类；禁止按错误字符串随意重试。
- 中文注释说明总预算/子预算、服务发现与负载均衡发生位置、熔断为何仍需要 timeout。

## 验收标准
- [x] 最小 echo 测试通过 etcd 发现两个实例并完成调用。
- [x] etcd 不可用时在预算内失败，配置不会回退静态直连或数据库直连。
- [x] 日志不打印 DSN、JWT、etcd/Kafka/Redis 凭据。

## 验证命令
```bash
go test -race ./internal/config/... ./internal/platform/rpc/...
go vet ./internal/config/... ./internal/platform/rpc/...
```

## 回滚点
移除 RPC 平台包和新增配置；尚无业务调用方。

## 完成记录

### 修改文件

- `internal/config/rpc.go`、`internal/config/rpc_test.go`
- `internal/platform/rpc/{budget,runtime,errors}.go` 及测试
- `etc/{user,product,seckill,inventory,order,notification}-rpc.yaml`
- `go.mod`、`go.sum`（启用 go-zero zRPC resolver 所需依赖闭包）

### 测试结果

- 真实临时 etcd + 两个同 key zRPC echo 实例：PASS，客户端观察到两个实例响应。
- etcd 不可用：PASS，启动 context 在 150ms 预算内返回，不回退静态 endpoint。
- 配置、预算、错误分类/脱敏单测：PASS。

### 遗留问题

- go-zero v1.10.3 的 resolver 初始化使用内部固定拨号 context；平台层通过显式启动 context
  有界返回，并异步回收迟到连接。后续升级 go-zero 时应复查是否已有原生 context API。
- 本任务只建立治理基础；具体业务 server/client 和业务重试策略由后续 TASK 实现。
