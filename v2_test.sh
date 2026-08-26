go run ./cmd/loadtest \
  -strategy pessimistic \
  -scenario unique \
  -sku-id 1 \
  -stock 500 \
  -requests 1000 \
  -concurrency 100 \
  -run-id sku-123-atomic-001 \
  -output docs/benchmark/sku-123-pessimistic-001.json

go run ./cmd/loadtest \
  -strategy optimistic \
  -scenario unique \
  -sku-id 1 \
  -stock 500 \
  -requests 1000 \
  -concurrency 100 \
  -run-id sku-123-atomic-001 \
  -output docs/benchmark/sku-123-optimistic-001.json

go run ./cmd/loadtest \
  -strategy atomic \
  -scenario unique \
  -sku-id 1 \
  -stock 500 \
  -requests 1000 \
  -concurrency 100 \
  -run-id sku-123-atomic-001 \
  -output docs/benchmark/sku-123-atomic-001.json
