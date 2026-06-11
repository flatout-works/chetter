#!/bin/bash
# Rebuild, restart the Kata runner, and submit the OpenCode Kata test task.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RUNNER_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
LOG_FILE="${DEVFLEET_KATA_LOG:-/tmp/devfleet-kata-runner.log}"
KEEP_RUNNER=0
NO_BUILD=0
FLUSH_IPTABLES=0

usage() {
  cat <<'EOF'
Usage: test/retry-kata.sh [options]

Options:
  --keep-runner      Leave the runner running after the test exits
  --no-build         Skip go build
  --flush-iptables   Flush nat/FORWARD/INPUT chains before retrying (dev-only)
  -h, --help         Show this help

Environment:
  SYNTHETIC_API_KEY  Required by test/opencode_kata_task.go
  NATS_URL           Optional, defaults to nats://localhost:4222
  DEVFLEET_KATA_LOG   Optional log path, defaults to /tmp/devfleet-kata-runner.log
  DEVFLEET_KATA_PREFLIGHT=1 enables verbose curl/strace Kata debugging
EOF
}

for arg in "$@"; do
  case "$arg" in
    --keep-runner) KEEP_RUNNER=1 ;;
    --no-build) NO_BUILD=1 ;;
    --flush-iptables) FLUSH_IPTABLES=1 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown option: $arg" >&2; usage; exit 2 ;;
  esac
done

if [ -z "${SYNTHETIC_API_KEY:-}" ]; then
  echo "SYNTHETIC_API_KEY is required" >&2
  exit 1
fi

cleanup_runner() {
  if [ "$KEEP_RUNNER" = "0" ]; then
    sudo pkill -f "runner -config runner.yaml" 2>/dev/null || true
  fi
}

cleanup_state() {
  echo "[retry] stopping existing runner"
  sudo pkill -f "runner -config runner.yaml" 2>/dev/null || true
  pkill -f "go run test/opencode_kata_task.go" 2>/dev/null || true
  pkill -f "opencode_kata_task" 2>/dev/null || true

  echo "[retry] killing stale kata-poc tasks"
  sudo ctr -n devfleet-runner task ls 2>/dev/null | awk 'NR > 1 && $1 ~ /^kata-poc-/ { print $1 }' | while read -r task_id; do
    [ -n "$task_id" ] || continue
    sudo ctr -n devfleet-runner task kill --signal SIGKILL "$task_id" 2>/dev/null || true
    sudo ctr -n devfleet-runner task delete "$task_id" 2>/dev/null || true
    sudo ctr -n devfleet-runner container delete "$task_id" 2>/dev/null || true
  done

  echo "[retry] deleting stale test bridges/netns"
  ip -o link show 2>/dev/null | awk -F': ' '$2 ~ /^br-[0-9a-f]{8}$/ { print $2 }' | while read -r bridge; do
    sudo ip link delete "$bridge" 2>/dev/null || true
  done
  ip netns list 2>/dev/null | awk '$1 ~ /^fo-[0-9a-f]{8}$/ { print $1 }' | while read -r netns; do
    sudo ip netns delete "$netns" 2>/dev/null || true
  done

  if [ "$FLUSH_IPTABLES" = "1" ]; then
    echo "[retry] flushing iptables chains (dev-only)"
    sudo iptables -t nat -F
    sudo iptables -F FORWARD
    sudo iptables -F INPUT
  fi
}

start_runner() {
  : > "$LOG_FILE"
  echo "[retry] starting runner, log: $LOG_FILE"
  (cd "$RUNNER_DIR" && sudo ./runner -config runner.yaml > "$LOG_FILE" 2>&1) &

  for _ in $(seq 1 30); do
    if grep -q "listening on devfleet.runner.tasks" "$LOG_FILE" 2>/dev/null; then
      echo "[retry] runner ready"
      return 0
    fi
    sleep 1
  done

  echo "[retry] runner failed to become ready" >&2
  cat "$LOG_FILE" >&2 || true
  return 1
}

run_test() {
  echo "[retry] running Kata OpenCode task"
  (cd "$RUNNER_DIR" && timeout 360 go run test/opencode_kata_task.go)
}

show_log_hint() {
  echo ""
  echo "[retry] runner log: $LOG_FILE"
  echo "[retry] useful command: less +F $LOG_FILE"
}

trap 'show_log_hint; cleanup_runner' EXIT

cd "$RUNNER_DIR"
if [ "$NO_BUILD" = "0" ]; then
  echo "[retry] building runner"
  go build -o runner ./cmd/runner
fi

cleanup_state
start_runner
run_test
