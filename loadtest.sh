SERVICE_RPC_LOADTEST_ADMIN_EMAIL='task004-e2e-20260822@example.com' \
SERVICE_RPC_LOADTEST_ADMIN_PASSWORD='correct-password-123' \
go run ./cmd/loadtest \
  -concurrency 10 \
  -requests 1000 \
  -timeout 10s
