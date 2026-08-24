# TASK-003: MySQL 与 migration 基础

## 背景

API 骨架已经可运行，但还没有持久化能力。用户注册等后续任务需要一个隔离的本地 MySQL、统一的连接生命周期和可重复的 schema migration 入口。

## 目标

建立 MySQL 连接基础、migration CLI 和本地 Docker MySQL 测试实例，并证明 migration 可以安全重复执行和回退一步。

## 非目标

- 不创建用户、角色、商品或订单业务表。
- 不实现 repository 或业务 API。
- 不引入 Redis、MQ、RPC、etcd 或分布式事务。
- 不加入 Docker Compose 或 Kubernetes 文件。
- 不进行无 benchmark 依据的连接池性能调优。

## 允许修改

- `main.go`
- `etc/store-api.yaml`
- `Makefile`
- `go.mod`
- `go.sum`
- `internal/config/config.go`
- `internal/platform/database/mysql.go`
- `internal/platform/database/mysql_test.go`
- `cmd/migrate/main.go`
- `migrations/000001_migration_baseline.up.sql`
- `migrations/000001_migration_baseline.down.sql`
- 本 TASK 文档
- Docker 容器 `service-rpc-mysql` 和持久卷 `service-rpc-mysql-data`

## 禁止修改

- `labs/`
- HTTP API 契约和 handler
- 其他版本 Spec
- 已存在的其他 Docker 容器和卷

## 实现约束

- MySQL 容器只绑定回环地址 `127.0.0.1:3307`。
- 应用使用独立的 `service_rpc` 数据库和普通用户，不使用 root 连接业务。
- DSN 不得写入日志。
- 连接创建后必须在有界超时内 Ping；失败时应用快速失败。
- 连接池数值只使用保守默认值，并注明后续需要基于指标调整。
- baseline migration 不创建无调用方的业务表，只验证 migration 历史和回退机制。
- down 命令每次只回退一步，避免默认清空全部 schema。

## 验收标准

- [x] `service-rpc-mysql` 容器健康，且不影响其他容器。
- [x] 应用可以读取 MySQL 配置、连接并关闭数据库。
- [x] 空数据库执行 migration up 成功。
- [x] 第二次 up 返回 no change，视为成功。
- [x] down 一步后可以再次 up。
- [x] 隔离数据库连接测试、全量测试、race detector 和 vet 通过。
- [x] 没有提前创建业务表或引入后续阶段技术。

## 验证命令

```bash
docker ps --filter name=service-rpc-mysql
SERVICE_RPC_MYSQL_TEST_DSN='...' go test ./internal/platform/database
go run ./cmd/migrate -f etc/store-api.yaml up
go run ./cmd/migrate -f etc/store-api.yaml up
go run ./cmd/migrate -f etc/store-api.yaml down
go run ./cmd/migrate -f etc/store-api.yaml up
go test ./...
go test -race ./...
go vet ./...
```

## 回滚点

先运行 migration down 一步，再恢复本任务修改的代码和依赖。本地容器可停止保留；只有用户明确要求清理时才删除容器或持久卷。

## 完成记录

### 修改文件

- `main.go`：启动时建立有界超时的 MySQL 连接，并在退出时关闭连接池。
- `etc/store-api.yaml`：加入本地 MySQL DSN 和保守连接池配置。
- `internal/config/config.go`：定义 API 与 migration 命令共享配置。
- `internal/platform/database/mysql.go`：封装连接、配置校验、Ping 和池参数。
- `internal/platform/database/mysql_test.go`：覆盖配置失败和真实 Docker MySQL 连接。
- `cmd/migrate/main.go`：提供 up、单步 down 和 version 命令。
- `migrations/000001_migration_baseline.*.sql`：建立不创建业务表的 migration 基线。
- `Makefile`：增加 migration 开发命令。
- `go.mod`、`go.sum`：加入 MySQL driver 和 golang-migrate。
- 本 TASK 文档：记录范围与验证结论。

### 测试结果

- `docker ps --filter name=service-rpc-mysql`：PASS，容器 healthy，绑定 `127.0.0.1:3307`。
- `SERVICE_RPC_MYSQL_TEST_DSN=... go test -v ./internal/platform/database`：PASS。
- migration `up → up(no change) → down → up`：PASS。
- 启动 API 并请求 `GET /healthz`：PASS；进程连接 MySQL 后返回 HTTP 200，并能响应 SIGINT 退出。
- `SERVICE_RPC_MYSQL_TEST_DSN=... go test -race -timeout 120s ./...`：PASS。
- `go vet ./...`：PASS。

### 遗留问题

- 本地开发凭据只适用于回环地址上的 Docker MySQL；进入部署阶段前必须改为外部密钥注入。
- baseline migration 只验证机制，业务表由各自拥有者 TASK 单独创建。
