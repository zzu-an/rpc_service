# TASK-054: RPC 契约规范与兼容门禁

## 背景
微服务拆分前必须先冻结字段、错误和兼容规则，否则 gateway 与四个服务会并行漂移。

## 目标
建立 versioned Protobuf 目录、公共错误约定、生成命令和 breaking-change 门禁。

## 非目标
- 不实现 RPC server/client 业务逻辑。
- 不迁移 HTTP handler。

## 允许修改
- `api/proto/common/v1/*`
- `api/proto/{user,product,seckill,inventory,order,notification}/v1/*.proto`
- Protobuf 生成配置/脚本
- `Makefile`
- 契约测试

## 禁止修改
- 领域逻辑、repository、migration、HTTP handler。

## 实现约束
- 只声明 Spec 中有当前调用方的 RPC。
- enum 0 值为 UNSPECIFIED；字段号不可复用，删除字段必须 `reserved`。
- 稳定 error code 与 gRPC status 分离，禁止透传 SQL/内部错误文本。
- 生成文件只由固定命令产生，不手改。
- `.proto` 中文注释解释字段单位、幂等键和兼容边界；显然字段不做逐行翻译。

## 验收标准
- [x] 同一输入可确定性生成，工作树无生成漂移。
- [x] descriptor/breaking 检查能阻止字段号复用和不兼容类型修改。
- [x] 金额、时间、ID、分页和 error code 规则有契约测试。

## 验证命令
```bash
make proto
make verify-proto
git diff --exit-code -- generated-paths
```

## 回滚点
删除本任务新增 IDL/生成产物并恢复 Makefile，不影响运行代码。

## 完成记录

### 修改文件

- `api/proto/{common,user,product,seckill,inventory,order,notification}/v1/*.proto`
- `api/gen/**`（只由 `make proto` 生成）
- `api/proto/baseline/v0.5.bin`、`api/proto/contract_test.go`
- `buf.yaml`、`Makefile`

### 测试结果

- `make proto`：PASS。
- `make verify-proto`：PASS，包含 Buf lint、FILE 级 breaking 检查、临时目录重新生成 diff 和契约单测。
- `git diff --check`：PASS。

### 遗留问题

- 当前是第一版 v1 baseline；新增兼容字段可以追加，删除/改类型必须新建 major 版本或先走 ADR。
- RPC server/client、deadline 和错误 adapter 属于后续 TASK，不在本任务生成空实现。
