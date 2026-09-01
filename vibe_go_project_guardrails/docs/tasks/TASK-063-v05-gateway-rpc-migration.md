# TASK-063: gateway RPC 化与 HTTP 契约回归

## 背景
业务 RPC 服务完成后，现有 HTTP 入口仍直接装配全部 repository，尚未形成真实服务边界。

## 目标
将现有 HTTP handler 依赖替换为 RPC client adapter，并建立独立 `gateway-api` 入口。

## 非目标
- 不改变公开路径、JWT 格式或业务响应字段。
- 不新增 API 网关产品、中间件体系或系统级限流。

## 允许修改
- `cmd/gateway-api/*`
- `internal/handler/*`
- gateway 配置
- `main.go`（只做迁移/退役）
- HTTP 契约与集成测试

## 禁止修改
- 业务 repository、migration、Kafka/Stream runtime。

## 实现约束
- gateway 不能打开 MySQL/Redis；只装配 JWT 与 RPC clients。
- 身份来自 JWT；只向下游传最小 user ID/request ID，不转发原始 token。
- RPC deadline/unavailable 映射为稳定 HTTP 504/503；业务错误保持现有状态码。
- handler 不感知生成 client 细节，通过小接口便于测试。
- 中文注释解释身份边界、错误映射及为何依赖失败不能回退本地 repository。

## 验收标准
- [x] 所有现有 HTTP route/鉴权/响应契约测试保持通过。
- [x] gateway 生产代码不导入 MySQL/Redis repository。
- [x] 任一 RPC 不可用时请求在预算内失败，其他不相关路由仍可工作。
- [x] notification 查询接口由 TASK-067A 接入同一 gateway 身份和错误映射边界。

## 完成记录

- 新增独立 `cmd/gateway-api`，只装配 JWT 与 user/product/inventory/seckill/order RPC adapters；根单体入口已退役，不再持有数据库或 Redis 凭据。
- handler 通过用例级小接口接收 RPC adapter；JWT 只在 gateway 解析为最小 `user_id`，不向内网转发原 token。
- deadline/unavailable 统一映射 HTTP 504/503，禁止本地 repository fallback；秒杀结果严格先查 order-rpc，只有 NotFound 才读 Redis 投影。
- `make verify-http-contract-v05` 已建立：现有 handler race 回归、gateway 生产数据依赖扫描、diff check；另有测试证明单个 order-rpc 故障不影响 product 路由。
- notification 列表/已读路由已由 TASK-067A 接入；user_id 只取 JWT context，并复用统一 RPC 错误映射。

## 验证命令
```bash
go test -race ./internal/handler/... ./cmd/gateway-api/...
make verify-http-contract-v05
```

## 回滚点
恢复单体入口配置；不改变数据库或队列数据。
