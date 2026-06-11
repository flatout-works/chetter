#!/bin/bash
# Start runner fully detached and run a smoke test

set +m  # disable job control

KILL_RUNNER=0
for arg in "$@"; do
  if [ "$arg" = "--kill" ]; then
    KILL_RUNNER=1
  fi
done

if [ "$KILL_RUNNER" = "1" ]; then
  pkill -f "runner -config test.runner.yaml" 2>/dev/null || true
  echo "Killed existing runner(s)"
  exit 0
fi

# Kill existing
pkill -f "runner -config test.runner.yaml" 2>/dev/null || true
sleep 1

# Clean workspace
rm -rf /tmp/devfleet-test-runner

# Start runner fully detached with setsid
setsid bash -c '
  cd "$(dirname "$0")/.."
  export RUNNER_LOCAL=true
  exec ./runner -config test.runner.yaml > /tmp/runner-smoke.log 2>&1
' &
RUNNER_PGID=$!
echo "Runner started in session $RUNNER_PGID"

# Wait for ready
for i in $(seq 1 30); do
  if grep -q "listening on devfleet.test.tasks" /tmp/runner-smoke.log 2>/dev/null; then
    echo "Runner ready after $i seconds"
    break
  fi
  sleep 1
done

if ! grep -q "listening on devfleet.test.tasks" /tmp/runner-smoke.log 2>/dev/null; then
  echo "Runner failed to start. Log:"
  cat /tmp/runner-smoke.log 2>/dev/null || true
  exit 1
fi

# Send task
echo "Sending task via NATS..."
cd "$(dirname "$0")/.."
export NATS_URL=nats://localhost:4222
timeout 90 go run test/opencode_smoke.go

# Show runner log
echo ""
echo "=== Runner log ==="
cat /tmp/runner-smoke.log 2>/dev/null || true
