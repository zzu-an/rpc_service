.PHONY: test test-race vet verify-v01 labs-test labs-race labs-bench migrate-up migrate-down migrate-version

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
