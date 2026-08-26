.PHONY: test test-race vet verify-v01 verify-v02 verify-v02-boundary labs-test labs-race labs-bench migrate-up migrate-down migrate-version

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
