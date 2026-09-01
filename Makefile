.PHONY: test test-race vet proto proto-baseline verify-proto verify-v01 verify-v02 verify-v02-boundary verify-v03 verify-v03-boundary verify-v042 verify-v042-boundary verify-migrations-v05 verify-migrations-v05-boundary verify-http-contract-v05 verify-kafka-v05 verify-order-outbox-v05 verify-stream-rpc-v05 verify-notification-v05 verify-projector-v05 verify-rpc-faults-v05 verify-lifecycle-v05 verify-v05 verify-v05-boundary v05-topology v05-apps-start v05-apps-stop v05-diagnose labs-test labs-race labs-bench migrate-up migrate-down migrate-version

CONFIG ?= etc/store-api.yaml
TEST_DSN ?=
PROTOC ?= protoc
BUF ?= buf
GO_BIN ?= $(shell go env GOPATH)/bin
BUF_CACHE_DIR ?= $(TMPDIR)/service-rpc-buf-cache
GO_CACHE_DIR ?= $(TMPDIR)/service-rpc-go-build
PROTO_FILES := $(shell find api/proto -type f -name '*.proto' | sort)
GENERATED_PROTO_DIR := api/gen

# 生成产物只能由这一条命令更新，不能手改 pb.go。source_relative 容易把 IDL 与生成代码混在一起，
# 因此使用 go_package + module 把所有产物稳定放到 api/gen 下，便于 drift 检查和代码评审。
proto:
	@command -v $(PROTOC) >/dev/null || (echo "protoc is required" && exit 1)
	@test -x "$(GO_BIN)/protoc-gen-go" || (echo "$(GO_BIN)/protoc-gen-go is required" && exit 1)
	@test -x "$(GO_BIN)/protoc-gen-go-grpc" || (echo "$(GO_BIN)/protoc-gen-go-grpc is required" && exit 1)
	@PATH="$(GO_BIN):$$PATH" $(PROTOC) -I api/proto \
		--go_out=. --go_opt=module=service_rpc \
		--go-grpc_out=. --go-grpc_opt=module=service_rpc \
		$(PROTO_FILES)

# baseline 是已评审契约的 descriptor image。后续新增字段允许通过，字段号复用、删除 RPC、
# 修改字段类型等 FILE 级 breaking change 会被 Buf 拒绝；升级 major 版本时应新建 v2，而不是覆盖 v1。
proto-baseline:
	@command -v $(BUF) >/dev/null || (echo "buf is required" && exit 1)
	@mkdir -p api/proto/baseline
	@BUF_CACHE_DIR="$(BUF_CACHE_DIR)" $(BUF) build --exclude-source-info -o api/proto/baseline/v0.5.bin

verify-proto:
	@command -v $(BUF) >/dev/null || (echo "buf is required" && exit 1)
	@BUF_CACHE_DIR="$(BUF_CACHE_DIR)" $(BUF) lint
	@test -f api/proto/baseline/v0.5.bin || (echo "run make proto-baseline after contract review" && exit 1)
	@BUF_CACHE_DIR="$(BUF_CACHE_DIR)" $(BUF) breaking --against api/proto/baseline/v0.5.bin
	@tmp_dir=$$(mktemp -d); \
		trap 'rm -rf "$$tmp_dir"' EXIT; \
		PATH="$(GO_BIN):$$PATH" $(PROTOC) -I api/proto \
			--go_out="$$tmp_dir" --go_opt=module=service_rpc \
			--go-grpc_out="$$tmp_dir" --go-grpc_opt=module=service_rpc \
			$(PROTO_FILES); \
		diff -ru "$(GENERATED_PROTO_DIR)" "$$tmp_dir/api/gen"
	@GOCACHE="$(GO_CACHE_DIR)" go test ./api/proto/...

test:
	go test ./...

test-race:
	go test -race -timeout 120s ./...

vet:
	go vet ./...

# verify-v01 intentionally fails when no test DSN is supplied. Silently
# skipping MySQL integration tests would make a green stage gate misleading.
verify-v01:
	@test -n "$(TEST_DSN)" || (echo "TEST_DSN is required; use an isolated MySQL test database" && exit 1)
	SERVICE_RPC_MYSQL_TEST_DSN="$(TEST_DSN)" go test -race -timeout 120s ./...
	go vet ./...

# verify-v02 使用显式隔离 DSN，并强制执行包括 100/1000 并发在内的 MySQL 集成测试。
# 测试库必须先应用到 migration v6；若 schema 缺失，门禁应失败而不是静默跳过。
verify-v02:
	@test -n "$(TEST_DSN)" || (echo "TEST_DSN is required; use an isolated MySQL test database migrated to v6" && exit 1)
	SERVICE_RPC_MYSQL_TEST_DSN="$(TEST_DSN)" go test -race -timeout 180s ./...
	go vet ./...
	$(MAKE) verify-v02-boundary

verify-v02-boundary:
	@if rg -n 'go-redis|redis\.New|kafka|rocketmq|rabbitmq|etcd|grpc\.New|zrpc' main.go internal --glob '*.go'; then \
		echo "v0.2 boundary violation: later-stage infrastructure found"; exit 1; \
	else \
		echo "v0.2 boundary scan passed"; \
	fi

# v0.3 门禁显式连接真实 MySQL 与 Redis。Redis 密码是可选变量：无密码实例可省略，
# 有密码实例通过 TEST_REDIS_PASSWORD 传入；命令和报告都不会打印其值。
verify-v03:
	@test -n "$(TEST_DSN)" || (echo "TEST_DSN is required; use an isolated MySQL database migrated to the latest version" && exit 1)
	@test -n "$(TEST_REDIS_ADDR)" || (echo "TEST_REDIS_ADDR is required; use a disposable Redis DB" && exit 1)
	@V03_STAGE_VERIFY=1 V03_MIGRATION_VERIFY=1 TEST_DSN="$(TEST_DSN)" TEST_REDIS_ADDR="$(TEST_REDIS_ADDR)" TEST_REDIS_PASSWORD="$(TEST_REDIS_PASSWORD)" go test -timeout 120s -run '^TestV03MigrationRoundTrip$$' -count=1 ./internal/seckill/redisgate
	@V03_STAGE_VERIFY=1 SERVICE_RPC_MYSQL_TEST_DSN="$(TEST_DSN)" TEST_DSN="$(TEST_DSN)" TEST_REDIS_ADDR="$(TEST_REDIS_ADDR)" TEST_REDIS_PASSWORD="$(TEST_REDIS_PASSWORD)" go test -race -timeout 240s ./...
	go vet ./...
	$(MAKE) verify-v03-boundary
	git diff --check

verify-v03-boundary:
	@if rg -n 'segmentio/kafka|shopify/sarama|rocketmq|rabbitmq|amqp091|google\.golang\.org/grpc|grpc\.New|go\.etcd\.io|zrpc|dtm-labs|rate\.NewLimiter|gobreaker' main.go internal --glob '*.go'; then \
		echo "v0.3 boundary violation: post-v0.3 infrastructure found"; exit 1; \
	else \
		echo "v0.3 infrastructure boundary scan passed"; \
	fi
	@if rg -n 'distributed-lock|NewLock\(|Acquire\(|SetNX\(' main.go internal --glob '*.go'; then \
		echo "v0.3 boundary violation: production distributed-lock reference found"; exit 1; \
	else \
		echo "v0.3 production distributed-lock scan passed"; \
	fi

# v0.4.2 是纯 Redis Stream 分支，只要求真实 MySQL/Redis。缺少任一地址必须失败，
# 防止 fake 测试冒充阶段验收；Kafka 由 codex/v0.4.1-kafka 分支单独验证。
verify-v042:
	@test -n "$(TEST_DSN)" || (echo "TEST_DSN is required; use a disposable MySQL database migrated to the latest version" && exit 1)
	@test -n "$(TEST_REDIS_ADDR)" || (echo "TEST_REDIS_ADDR is required" && exit 1)
	@V042_STAGE_VERIFY=1 SERVICE_RPC_MYSQL_TEST_DSN="$(TEST_DSN)" TEST_DSN="$(TEST_DSN)" TEST_REDIS_ADDR="$(TEST_REDIS_ADDR)" TEST_REDIS_PASSWORD="$(TEST_REDIS_PASSWORD)" TEST_REDIS_DB="$(TEST_REDIS_DB)" go test -race -timeout 600s ./...
	go vet ./...
	$(MAKE) verify-v042-boundary
	git diff --check

verify-v042-boundary:
	@if rg -n 'segmentio/kafka|shopify/sarama|go-queue/kq|rocketmq|rabbitmq|amqp091|google\.golang\.org/grpc|go\.etcd\.io|zeromicro/go-zero/zrpc|dtm-labs|rate\.NewLimiter|gobreaker' main.go cmd internal --glob '*.go'; then \
		echo "v0.4.2 boundary violation: queue alternative or later-stage infrastructure found"; exit 1; \
	else \
		echo "v0.4.2 production infrastructure boundary scan passed"; \
	fi
	@if rg -n 'segmentio/kafka|shopify/sarama|go-queue/kq' go.mod go.sum; then \
		echo "v0.4.2 boundary violation: Kafka dependency found"; exit 1; \
	else \
		echo "v0.4.2 Kafka dependency isolation scan passed"; \
	fi
	@if rg -n '^\s*(type|func|var|const)\s+.*(Payment(State|Status)|Compensat|Reconcil)' main.go cmd internal --glob '*.go'; then \
		echo "v0.4.2 boundary violation: payment/compensation/reconciliation production API found"; exit 1; \
	else \
		echo "v0.4.2 business boundary scan passed"; \
	fi
	@if find migrations -maxdepth 1 -type f -name '000008*' | rg .; then \
		echo "v0.4.2 boundary violation: Stream alternative must not contain the Kafka job migration"; exit 1; \
	else \
		echo "v0.4.2 schema boundary scan passed"; \
	fi

verify-migrations-v05:
	@test -n "$(TEST_DSN)" || (echo "TEST_DSN is required; use an isolated MySQL database for v0.5 migration verification" && exit 1)
	@V05_MIGRATION_VERIFY=1 TEST_DSN="$(TEST_DSN)" GOCACHE="$(GO_CACHE_DIR)" go test -race -timeout 180s -run '^TestV05InventoryOwnershipMigration$$' -count=1 ./cmd/migrate
	@$(MAKE) verify-migrations-v05-boundary

verify-migrations-v05-boundary:
	@for fk in fk_orders_user fk_order_items_sku fk_seckill_items_activity fk_seckill_items_sku fk_seckill_claim_activity fk_seckill_claim_item fk_seckill_claim_user fk_seckill_claim_order; do \
		rg -q "DROP FOREIGN KEY $$fk" migrations/000008_inventory_ownership.up.sql || (echo "missing cross-service FK removal: $$fk" && exit 1); \
	done
	@if rg -ni '\b(float|double|decimal)\b' migrations/000008_inventory_ownership.up.sql; then \
		echo "inventory snapshot money must use integer cents"; exit 1; \
	else \
		echo "v0.5 inventory schema boundary scan passed"; \
	fi

# gateway 阶段门禁不需要数据库：HTTP handler 使用 fake RPC 边界验证原 URL/JSON/JWT，
# 同时扫描生产入口，防止为了“临时兼容”重新装配本地 repository 或 Redis fallback。
verify-http-contract-v05:
	@GOCACHE="$(GO_CACHE_DIR)" go test -race -count=1 ./internal/handler/... ./cmd/gateway-api/...
	@if rg -ni 'mysqlrepo|redisgate|platform/database|platform/cache|OpenMySQL|OpenRedis' main.go cmd/gateway-api internal/handler/gateway_rpc.go --glob '*.go'; then \
		echo "v0.5 gateway boundary violation: local data dependency found"; exit 1; \
	else \
		echo "v0.5 gateway data boundary scan passed"; \
	fi
	@git diff --check

verify-kafka-v05:
	@test -n "$(TEST_KAFKA_BROKERS)" || (echo "TEST_KAFKA_BROKERS is required; use a disposable real Kafka broker" && exit 1)
	@V05_KAFKA_VERIFY=1 TEST_KAFKA_BROKERS="$(TEST_KAFKA_BROKERS)" GOCACHE="$(GO_CACHE_DIR)" go test -race -count=1 ./internal/platform/mq/... ./internal/order/events/...
	@GOCACHE="$(GO_CACHE_DIR)" go vet ./internal/platform/mq/... ./internal/order/events/...
	@if rg -n 'franz-go|segmentio/kafka|sarama|internal/platform/mq' cmd/seckill-stream-worker internal/seckill/streamqueue --glob '*.go'; then \
		echo "v0.5 boundary violation: Stream-to-Kafka bridge found"; exit 1; \
	else \
		echo "v0.5 Stream/Kafka boundary scan passed"; \
	fi
	@if rg -n 'seckill_order_jobs' main.go cmd internal migrations --glob '*.go' --glob '*.sql'; then \
		echo "v0.5 boundary violation: legacy seckill job runtime found"; exit 1; \
	else \
		echo "v0.5 legacy job boundary scan passed"; \
	fi
	@git diff --check

verify-order-outbox-v05:
	@test -n "$(TEST_DSN)" || (echo "TEST_DSN is required" && exit 1)
	@test -n "$(TEST_KAFKA_BROKERS)" || (echo "TEST_KAFKA_BROKERS is required" && exit 1)
	@test -n "$(TEST_ORDER_CREATED_TOPIC)" || (echo "TEST_ORDER_CREATED_TOPIC must name a pre-created disposable topic" && exit 1)
	@V05_OUTBOX_MIGRATION_VERIFY=1 TEST_DSN="$(TEST_DSN)" GOCACHE="$(GO_CACHE_DIR)" go test -race -count=1 -run '^TestV05OrderOutboxMigrationRoundTrip$$' ./cmd/migrate
	@V05_ORDER_OUTBOX_VERIFY=1 SERVICE_RPC_MYSQL_TEST_DSN="$(TEST_DSN)" TEST_KAFKA_BROKERS="$(TEST_KAFKA_BROKERS)" TEST_ORDER_CREATED_TOPIC="$(TEST_ORDER_CREATED_TOPIC)" GOCACHE="$(GO_CACHE_DIR)" go test -p 1 -race -count=1 ./internal/order/... ./cmd/order-outbox-relay/...
	@GOCACHE="$(GO_CACHE_DIR)" go vet ./internal/order/... ./cmd/order-outbox-relay/...
	@git diff --check

# Stream 是秒杀域内部削峰队列，orchestrator 只能通过 RPC 写 inventory/order。
# 门禁用真实 MySQL/Redis 验证 100 次至少一次投递和“提交后、ACK 前崩溃”的恢复语义。
verify-stream-rpc-v05:
	@test -n "$(TEST_DSN)" || (echo "TEST_DSN is required" && exit 1)
	@test -n "$(TEST_REDIS_ADDR)" || (echo "TEST_REDIS_ADDR is required" && exit 1)
	@V05_STREAM_RPC_VERIFY=1 TEST_DSN="$(TEST_DSN)" SERVICE_RPC_MYSQL_TEST_DSN="$(TEST_DSN)" TEST_REDIS_ADDR="$(TEST_REDIS_ADDR)" TEST_REDIS_PASSWORD="$(TEST_REDIS_PASSWORD)" TEST_REDIS_DB="$(TEST_REDIS_DB)" GOCACHE="$(GO_CACHE_DIR)" go test -race -count=1 -timeout 240s ./internal/seckill/streamqueue/... ./cmd/seckill-orchestrator/...
	@GOCACHE="$(GO_CACHE_DIR)" go vet ./internal/seckill/streamqueue/... ./cmd/seckill-orchestrator/...
	@if rg -n 'internal/(order|seckill)/mysqlrepo|internal/platform/mq|franz-go|kafka' cmd/seckill-orchestrator internal/seckill/streamqueue --glob '*.go' --glob '!**/*_test.go'; then \
		echo "v0.5 orchestrator boundary violation: repository or Kafka dependency found"; exit 1; \
	else \
		echo "v0.5 orchestrator RPC boundary scan passed"; \
	fi
	@git diff --check

verify-notification-v05:
	@test -n "$(TEST_DSN)" || (echo "TEST_DSN is required" && exit 1)
	@test -n "$(TEST_KAFKA_BROKERS)" || (echo "TEST_KAFKA_BROKERS is required" && exit 1)
	@V05_NOTIFICATION_MIGRATION_VERIFY=1 TEST_DSN="$(TEST_DSN)" GOCACHE="$(GO_CACHE_DIR)" go test -race -count=1 -run '^TestV05NotificationMigrationRoundTrip$$' ./cmd/migrate
	@V05_NOTIFICATION_VERIFY=1 TEST_DSN="$(TEST_DSN)" TEST_KAFKA_BROKERS="$(TEST_KAFKA_BROKERS)" GOCACHE="$(GO_CACHE_DIR)" go test -race -count=1 -timeout 300s ./internal/notification/... ./cmd/notification-rpc/... ./cmd/notification-consumer/...
	@GOCACHE="$(GO_CACHE_DIR)" go test -race -count=1 ./internal/handler/... ./cmd/gateway-api/...
	@GOCACHE="$(GO_CACHE_DIR)" go vet ./internal/notification/... ./cmd/notification-rpc/... ./cmd/notification-consumer/... ./internal/handler/... ./cmd/gateway-api/...
	@if rg -ni 'internal/order/mysqlrepo|\b(FROM|JOIN|INSERT INTO|UPDATE|DELETE FROM)\s+orders\b|order_items' internal/notification cmd/notification-rpc cmd/notification-consumer --glob '*.go' --glob '!**/*_test.go'; then \
		echo "v0.5 notification boundary violation: order database dependency found"; exit 1; \
	else \
		echo "v0.5 notification ownership boundary scan passed"; \
	fi
	@git diff --check

verify-projector-v05:
	@test -n "$(TEST_DSN)" || (echo "TEST_DSN is required" && exit 1)
	@test -n "$(TEST_REDIS_ADDR)" || (echo "TEST_REDIS_ADDR is required" && exit 1)
	@test -n "$(TEST_KAFKA_BROKERS)" || (echo "TEST_KAFKA_BROKERS is required" && exit 1)
	@V05_PROJECTOR_VERIFY=1 TEST_DSN="$(TEST_DSN)" TEST_REDIS_ADDR="$(TEST_REDIS_ADDR)" TEST_REDIS_PASSWORD="$(TEST_REDIS_PASSWORD)" TEST_KAFKA_BROKERS="$(TEST_KAFKA_BROKERS)" GOCACHE="$(GO_CACHE_DIR)" go test -race -count=1 -timeout 360s ./internal/seckill/resultprojector/... ./cmd/seckill-result-projector/...
	@GOCACHE="$(GO_CACHE_DIR)" go vet ./internal/seckill/resultprojector/... ./cmd/seckill-result-projector/...
	@if rg -n 'internal/(order|notification)/mysqlrepo|internal/platform/database' internal/seckill/resultprojector cmd/seckill-result-projector --glob '*.go' --glob '!**/*_test.go'; then \
		echo "v0.5 projector boundary violation: downstream database dependency found"; exit 1; \
	else \
		echo "v0.5 projector Redis-only boundary scan passed"; \
	fi
	@git diff --check

verify-rpc-faults-v05:
	@test -n "$(TEST_ETCD_HOSTS)" || (echo "TEST_ETCD_HOSTS is required to acknowledge the disposable etcd target" && exit 1)
	@GOCACHE="$(GO_CACHE_DIR)" go test -race -count=1 -timeout 180s ./tests/rpcfault/... ./internal/platform/rpc/...
	@GOCACHE="$(GO_CACHE_DIR)" go vet ./tests/rpcfault/... ./internal/platform/rpc/... ./cmd/seckill-orchestrator/...
	@if rg -n 'mysqlrepo|OpenMySQL|internal/platform/database' internal/platform/rpc cmd/gateway-api --glob '*.go' --glob '!**/*_test.go'; then \
		echo "v0.5 RPC fallback boundary violation"; exit 1; \
	else \
		echo "v0.5 RPC no-database-fallback scan passed"; \
	fi
	@git diff --check

v05-topology:
	@bash scripts/v05-apps.sh list

v05-apps-start:
	@bash scripts/v05-apps.sh start

v05-apps-stop:
	@bash scripts/v05-apps.sh stop

v05-diagnose:
	@GOCACHE="$(GO_CACHE_DIR)" go run ./cmd/v05check -f etc/v05check.yaml

verify-lifecycle-v05:
	@GOCACHE="$(GO_CACHE_DIR)" go test -race -count=1 -timeout 240s ./cmd/...
	@GOCACHE="$(GO_CACHE_DIR)" go vet ./cmd/v05check/...
	@V05_TOPOLOGY_MODE=daily bash scripts/v05-apps.sh list | rg -q 'application_instances=11.*total_instances=15'
	@V05_TOPOLOGY_MODE=governance bash scripts/v05-apps.sh list | rg -q 'application_instances=13.*total_instances=17'
	@if rg -ni 'FLUSHALL|FLUSHDB|DeleteTopics|kafka-topics.*--delete|automatic.*(repair|compensat)' cmd/v05check scripts/v05-apps.sh; then \
		echo "v0.5 lifecycle destructive/auto-repair boundary violation"; exit 1; \
	else \
		echo "v0.5 lifecycle read-only boundary scan passed"; \
	fi
	@git diff --check

# v0.5 总门禁按 migration 版本顺序推进同一个隔离库：v8 ownership → v9 outbox → v10 notification。
# 缺少任一真实依赖参数立即失败；各子门禁不会 silent skip。
verify-v05:
	@test -n "$(TEST_DSN)" || (echo "TEST_DSN is required" && exit 1)
	@test -n "$(TEST_REDIS_ADDR)" || (echo "TEST_REDIS_ADDR is required" && exit 1)
	@test -n "$(TEST_KAFKA_BROKERS)" || (echo "TEST_KAFKA_BROKERS is required" && exit 1)
	@test -n "$(TEST_ORDER_CREATED_TOPIC)" || (echo "TEST_ORDER_CREATED_TOPIC is required and must be disposable" && exit 1)
	@test -n "$(TEST_ETCD_HOSTS)" || (echo "TEST_ETCD_HOSTS is required" && exit 1)
	@$(MAKE) verify-proto
	@$(MAKE) verify-migrations-v05 TEST_DSN="$(TEST_DSN)"
	@$(MAKE) verify-http-contract-v05
	@$(MAKE) verify-kafka-v05 TEST_KAFKA_BROKERS="$(TEST_KAFKA_BROKERS)"
	@$(MAKE) verify-order-outbox-v05 TEST_DSN="$(TEST_DSN)" TEST_KAFKA_BROKERS="$(TEST_KAFKA_BROKERS)" TEST_ORDER_CREATED_TOPIC="$(TEST_ORDER_CREATED_TOPIC)"
	@$(MAKE) verify-stream-rpc-v05 TEST_DSN="$(TEST_DSN)" TEST_REDIS_ADDR="$(TEST_REDIS_ADDR)" TEST_REDIS_PASSWORD="$(TEST_REDIS_PASSWORD)" TEST_REDIS_DB="$(TEST_REDIS_DB)"
	@$(MAKE) verify-notification-v05 TEST_DSN="$(TEST_DSN)" TEST_KAFKA_BROKERS="$(TEST_KAFKA_BROKERS)"
	@$(MAKE) verify-projector-v05 TEST_DSN="$(TEST_DSN)" TEST_REDIS_ADDR="$(TEST_REDIS_ADDR)" TEST_REDIS_PASSWORD="$(TEST_REDIS_PASSWORD)" TEST_KAFKA_BROKERS="$(TEST_KAFKA_BROKERS)"
	@$(MAKE) verify-rpc-faults-v05 TEST_ETCD_HOSTS="$(TEST_ETCD_HOSTS)"
	@$(MAKE) verify-lifecycle-v05
	@SERVICE_RPC_MYSQL_TEST_DSN="$(TEST_DSN)" TEST_DSN="$(TEST_DSN)" TEST_REDIS_ADDR="$(TEST_REDIS_ADDR)" TEST_REDIS_PASSWORD="$(TEST_REDIS_PASSWORD)" TEST_REDIS_DB="$(TEST_REDIS_DB)" GOCACHE="$(GO_CACHE_DIR)" go test -race -count=1 -timeout 600s ./internal/...
	@GOCACHE="$(GO_CACHE_DIR)" go vet ./...
	@$(MAKE) verify-v05-boundary
	@git diff --check

verify-v05-boundary:
	@if rg -n 'mysqlrepo|platform/database|OpenMySQL|go-redis|OpenRedis|internal/platform/mq|franz-go' main.go cmd/gateway-api internal/handler/gateway_rpc.go internal/handler/notification_rpc.go --glob '*.go'; then \
		echo "v0.5 boundary violation: gateway owns data/MQ dependency"; exit 1; \
	else echo "v0.5 gateway boundary passed"; fi
	@if rg -n 'mysqlrepo|platform/database|internal/platform/mq|franz-go|kafka' cmd/seckill-orchestrator internal/seckill/streamqueue --glob '*.go' --glob '!**/*_test.go'; then \
		echo "v0.5 boundary violation: orchestrator bypasses RPC or adds Stream-Kafka bridge"; exit 1; \
	else echo "v0.5 orchestrator boundary passed"; fi
	@if rg -n 'internal/platform/database|mysqlrepo' cmd/seckill-stream-worker --glob '*.go'; then \
		echo "v0.5 boundary violation: legacy direct-SQL Stream worker remains runnable"; exit 1; \
	else echo "v0.5 legacy worker retirement passed"; fi
	@if rg -ni '\b(FROM|JOIN|INSERT INTO|UPDATE|DELETE FROM)\s+(users|products|product_skus|seckill_)' internal/order/mysqlrepo --glob '*.go' --glob '!**/*_test.go'; then \
		echo "v0.5 boundary violation: order repository cross-service SQL"; exit 1; \
	else echo "v0.5 order SQL ownership passed"; fi
	@if rg -ni 'internal/order/mysqlrepo|\b(FROM|JOIN|INSERT INTO|UPDATE|DELETE FROM)\s+orders\b|order_items' internal/notification cmd/notification-rpc cmd/notification-consumer --glob '*.go' --glob '!**/*_test.go'; then \
		echo "v0.5 boundary violation: notification reads order storage"; exit 1; \
	else echo "v0.5 notification ownership passed"; fi
	@if rg -n 'seckill_order_jobs' main.go cmd internal migrations --glob '*.go' --glob '*.sql'; then \
		echo "v0.5 boundary violation: legacy seckill job found"; exit 1; \
	else echo "v0.5 queue topology boundary passed"; fi
	@if rg -n '^\s*(type|func|var|const)\s+.*(Compensat|Reconcil|Saga|TCC|Payment(State|Status))' main.go cmd internal --glob '*.go'; then \
		echo "v0.5 boundary violation: future compensation/payment API implemented early"; exit 1; \
	else echo "v0.5 future-scope boundary passed"; fi
labs-test:
	go test ./labs/...

labs-race:
	go test -race ./labs/...

labs-bench:
	go test -run='^$$' -bench=. -benchmem ./labs/mutex-vs-atomic

migrate-up:
	go run ./cmd/migrate -f $(CONFIG) up

migrate-down:
	go run ./cmd/migrate -f $(CONFIG) down

migrate-version:
	go run ./cmd/migrate -f $(CONFIG) version
