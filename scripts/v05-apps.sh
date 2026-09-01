#!/usr/bin/env bash
set -euo pipefail

mode="${V05_TOPOLOGY_MODE:-daily}"
state_dir="${TMPDIR:-/tmp}/service-rpc-v05-apps"
bin_dir="$state_dir/bin"
log_dir="$state_dir/logs"

daily_specs=(
  "gateway-api|./cmd/gateway-api|etc/gateway-api.yaml"
  "user-rpc|./cmd/user-rpc|etc/user-rpc.yaml"
  "product-rpc|./cmd/product-rpc|etc/product-rpc.yaml"
  "seckill-rpc|./cmd/seckill-rpc|etc/seckill-rpc.yaml"
  "inventory-rpc|./cmd/inventory-rpc|etc/inventory-rpc.yaml"
  "order-rpc|./cmd/order-rpc|etc/order-rpc.yaml"
  "notification-rpc|./cmd/notification-rpc|etc/notification-rpc.yaml"
  "seckill-orchestrator|./cmd/seckill-orchestrator|etc/seckill-orchestrator.yaml"
  "order-outbox-relay|./cmd/order-outbox-relay|etc/order-outbox-relay.yaml"
  "seckill-result-projector|./cmd/seckill-result-projector|etc/seckill-result-projector.yaml"
  "notification-consumer|./cmd/notification-consumer|etc/notification-consumer.yaml"
)
governance_specs=(
  "order-rpc-secondary|./cmd/order-rpc|etc/order-rpc-secondary.yaml"
  "notification-consumer-secondary|./cmd/notification-consumer|etc/notification-consumer-secondary.yaml"
)

specs=("${daily_specs[@]}")
if [[ "$mode" == "governance" ]]; then
  specs+=("${governance_specs[@]}")
elif [[ "$mode" != "daily" ]]; then
  echo "V05_TOPOLOGY_MODE must be daily or governance" >&2
  exit 2
fi

list_specs() {
  echo "mode=$mode application_instances=${#specs[@]} infrastructure_instances=4 total_instances=$((${#specs[@]} + 4))"
  for spec in "${specs[@]}"; do
    IFS='|' read -r name package config <<<"$spec"
    echo "$name package=$package config=$config"
  done
}

start_apps() {
  mkdir -p "$bin_dir" "$log_dir"
  for spec in "${specs[@]}"; do
    IFS='|' read -r name package config <<<"$spec"
    pid_file="$state_dir/$name.pid"
    if [[ -f "$pid_file" ]] && kill -0 "$(<"$pid_file")" 2>/dev/null; then
      echo "$name is already running" >&2
      exit 1
    fi
    GOCACHE="${TMPDIR:-/tmp}/service-rpc-go-build" go build -o "$bin_dir/$name" "$package"
    "$bin_dir/$name" -f "$config" >"$log_dir/$name.log" 2>&1 &
    echo "$!" >"$pid_file"
  done
  sleep 1
  for spec in "${specs[@]}"; do
    IFS='|' read -r name _ _ <<<"$spec"
    if ! kill -0 "$(<"$state_dir/$name.pid")" 2>/dev/null; then
      echo "$name failed during startup; inspect $log_dir/$name.log" >&2
      exit 1
    fi
  done
  echo "started ${#specs[@]} application processes; infrastructure must be managed separately"
}

stop_apps() {
  for spec in "${specs[@]}"; do
    IFS='|' read -r name _ _ <<<"$spec"
    pid_file="$state_dir/$name.pid"
    [[ -f "$pid_file" ]] || continue
    pid="$(<"$pid_file")"
    kill -TERM "$pid" 2>/dev/null || true
  done
  # 先给 RPC 停止接流量、consumer 停止 fetch 并等待在途；超时后强停也不会提交未完成 offset/PEL。
  for attempt in {1..100}; do
    alive=0
    for spec in "${specs[@]}"; do
      IFS='|' read -r name _ _ <<<"$spec"
      pid_file="$state_dir/$name.pid"
      [[ -f "$pid_file" ]] || continue
      kill -0 "$(<"$pid_file")" 2>/dev/null && alive=$((alive + 1))
    done
    [[ "$alive" -eq 0 ]] && break
    sleep 0.1
  done
  for spec in "${specs[@]}"; do
    IFS='|' read -r name _ _ <<<"$spec"
    pid_file="$state_dir/$name.pid"
    [[ -f "$pid_file" ]] || continue
    pid="$(<"$pid_file")"
    if kill -0 "$pid" 2>/dev/null; then
      echo "$name exceeded graceful timeout; leaving its uncommitted work for recovery" >&2
      kill -KILL "$pid" 2>/dev/null || true
    fi
    rm -f "$pid_file"
  done
  echo "stopped v0.5 $mode application topology"
}

case "${1:-list}" in
  list) list_specs ;;
  start) start_apps ;;
  stop) stop_apps ;;
  *) echo "usage: $0 {list|start|stop}" >&2; exit 2 ;;
esac
