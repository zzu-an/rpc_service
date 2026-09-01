# TASK-058: inventory 数据所有权 migration

## 背景
现有 `Purchase` 事务和外键跨 user/product/order/seckill 边界，无法由 inventory-rpc 独立拥有。

## 目标
新增 inventory reservation 账本、冻结秒杀商品快照、回填历史 claim，并移除跨服务外键。

## 非目标
- 不实现 reservation repository/RPC。
- 不物理分库。
- 不新增补偿或状态机。

## 允许修改
- 新增 migration up/down 文件
- migration 集成测试
- schema 边界检查脚本

## 禁止修改
- Go 领域/handler/runtime 代码。
- Kafka/Redis 消息格式。

## 实现约束
- 新表唯一键覆盖 `order_no` 和 `(activity_id,seckill_item_id,user_id)`。
- `seckill_items` 冻结名称、编码、整数分价格；禁止浮点金额。
- 历史 claim 通过 orders/order_items 回填，回填前后计数与抽样字段必须核对。
- 移除跨服务 FK，但保留服务内部 FK；down 必须说明数据前置条件。
- SQL 中文 COMMENT/相邻文档解释为什么 FK 被移除不等于放弃业务校验，以及残余一致性窗口。

## 验收标准
- [x] 非空历史库执行 up 成功，reservation 数等于有效历史 claim 数。
- [x] up/down/up 通过，唯一键能阻止重复 reservation。
- [x] schema 扫描不存在 user↔order、product↔order/inventory 的跨服务 FK。

## 验证命令
```bash
go test -race ./cmd/migrate/... ./internal/seckill/mysqlrepo/...
make verify-migrations-v05
```

## 回滚点
仅在记录 reservation/order/claim 计数后执行 down；不得用清库代替回滚验证。

## 完成记录

### 修改文件

- `migrations/000008_inventory_ownership.up.sql`
- `migrations/000008_inventory_ownership.down.sql`
- `cmd/migrate/v05_migration_test.go`
- `Makefile`（`verify-migrations-v05` 与 schema boundary）

### 测试结果

- 一次性本地 MySQL 8.4、非空历史 claim：up/down/up PASS。
- reservation 总数等于可由 order/order_items 回填的有效 claim 数；抽样快照一致。
- 两个唯一键与跨服务 FK 扫描：PASS；整数分 schema 扫描：PASS。

### 遗留问题

- 删除跨服务 FK 不代表放弃校验；外部 ID 由 RPC、唯一键和服务内事务负责。物理分库仍不属于 v0.5。
- inventory reservation 成功而 order 失败仍会留下 `reserved_without_order`，本阶段不自动补偿。
