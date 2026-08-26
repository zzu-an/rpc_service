# go run ./cmd/loadtest \
#   -strategy pessimistic \
#   -scenario unique \
#   -sku-id 1 \
#   -stock 500 \
#   -requests 1000 \
#   -concurrency 1 \
#   -run-id sku-123-atomic-001 \
#   -output docs/benchmark/sku-123-pessimistic-001.json

# go run ./cmd/loadtest \
#   -strategy optimistic \
#   -scenario unique \
#   -sku-id 1 \
#   -stock 500 \
#   -requests 1000 \
#   -concurrency 1 \
#   -run-id sku-123-atomic-001 \
#   -output docs/benchmark/sku-123-optimistic-001.json

# go run ./cmd/loadtest \
#   -strategy atomic \
#   -scenario unique \
#   -sku-id 1 \
#   -stock 500 \
#   -requests 1000 \
#   -concurrency 1 \
#   -run-id sku-123-atomic-001 \
#   -output docs/benchmark/sku-123-atomic-001.json

go run ./cmd/loadtest \
  -strategy atomic \
  -scenario replay \
  -requests 1000 \
  -concurrency 100 \
  -stock 1000 \
  -run-id replay-001 \
  -output docs/benchmark/v02-atomic-replay.json

go run ./cmd/loadtest \
  -strategy optimistic \
  -scenario replay \
  -requests 1000 \
  -concurrency 100 \
  -stock 1000 \
  -run-id replay-001 \
  -output docs/benchmark/v02-optimistic-replay.json

go run ./cmd/loadtest \
  -strategy pessimistic \
  -scenario replay \
  -requests 1000 \
  -concurrency 100 \
  -stock 1000 \
  -run-id replay-001 \
  -output docs/benchmark/v02-pessimistic-replay.json