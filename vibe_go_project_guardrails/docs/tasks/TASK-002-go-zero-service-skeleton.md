# TASK-002: go-zero 单体服务骨架

## 背景

v0.1 契约已经冻结，但根入口仍为空，尚不能启动商城 API。需要先建立一个不依赖数据库的最小 go-zero REST 服务，让后续任务拥有稳定的配置、启动和路由承载点。

## 目标

建立可启动的 go-zero REST 单体入口，并提供 `GET /healthz` 进程存活检查。

## 非目标

- 不连接 MySQL。
- 不实现注册、登录、JWT、RBAC、商品或订单。
- 不增加业务中间件。
- 不引入 Redis、MQ、RPC 或 etcd。
- 不预建后续模块的空目录和接口。

## 允许修改

- `main.go`
- `go.mod`
- `go.sum`
- `etc/store-api.yaml`
- `internal/handler/health.go`
- `internal/handler/health_test.go`
- 本 TASK 文档

## 禁止修改

- `labs/`
- `migrations/`
- 其他版本 Spec
- 其他业务 package

## 实现约束

- 只引入 go-zero REST 服务所需依赖。
- 配置文件路径通过命令行参数提供；配置无效时必须快速失败。
- `/healthz` 只表示进程正在服务，不探测尚不存在的数据库或其他下游。
- 响应遵守 v0.1 统一外壳，当前没有 request ID 时返回空字符串。
- 注释解释存活检查边界，不逐行翻译框架调用。

## 验收标准

- [x] 使用配置文件可以启动 API 服务。
- [x] `GET /healthz` 返回 HTTP 200、`code=OK` 和 `status=ok`。
- [x] 缺少配置文件时进程快速失败。
- [x] 没有引入 MySQL、JWT、Redis、MQ、RPC 或 etcd 业务依赖。
- [x] 单元测试、race detector 和静态检查通过。

## 验证命令

```bash
go test ./internal/handler
go test ./...
go test -race ./...
go vet ./...
go run . -f etc/store-api.yaml
curl -i http://127.0.0.1:8888/healthz
```

## 回滚点

恢复 `main.go`、`go.mod` 和 `go.sum`，删除本任务新增的配置、handler 和测试文件。

## 完成记录

### 修改文件

- `main.go`：加载 go-zero REST 配置、注册当前路由并启动单体服务。
- `go.mod`、`go.sum`：加入 go-zero v1.10.3 及其传递依赖。
- `etc/store-api.yaml`：提供本地 API 服务配置。
- `internal/handler/health.go`：注册并实现进程存活检查。
- `internal/handler/health_test.go`：验证响应、配置构造和真实临时端口路由。
- 本 TASK 文档：记录范围与验证结论。

### 测试结果

- `go test -timeout 20s ./internal/handler`：PASS，真实 go-zero Server 在临时端口返回预期 `/healthz` 响应。
- 缺失配置启动检查：PASS，进程输出配置文件不存在并以非零状态退出。
- `go test -race -timeout 120s ./...`：PASS。
- `go vet ./...`：PASS。

### 遗留问题

- `/healthz` 当前只表达进程存活，不检查 MySQL；这是 TASK-002 的刻意边界，数据库 readiness 由后续任务另行决定。
