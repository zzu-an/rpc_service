.PHONY: test test-race vet verify-v01 verify-v02 verify-v02-boundary verify-v03 verify-v03-boundary labs-test labs-race labs-bench migrate-up migrate-down migrate-version

CONFIG ?= etc/store-api.yaml
TEST_DSN ?=

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
