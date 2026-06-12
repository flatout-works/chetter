#!/bin/sh
set -eu

: "${NATS_URL:=nats://chetter-nats:4222}"
: "${TASK_SUBJECT:=chetter.runner.tasks}"
: "${RESULT_SUBJECT:=chetter.tasks}"
: "${RUNNER_WORKSPACE_ROOT:=/var/lib/chetter-runner/workspaces}"
: "${RUNNER_MAX_CONCURRENT:=2}"
: "${JETSTREAM_TASK_STREAM:=CHETTER_TASKS}"
: "${JETSTREAM_EVENT_STREAM:=CHETTER_EVENTS}"
: "${JETSTREAM_TASK_DURABLE:=chetter-runner}"
: "${JETSTREAM_TASK_QUEUE:=chetter-runners}"
: "${JETSTREAM_ACK_WAIT_SECONDS:=10}"
: "${JETSTREAM_MAX_DELIVER:=3}"
: "${JETSTREAM_MAX_ACK_PENDING:=4}"
: "${JETSTREAM_STORAGE:=file}"

# Resolve runner image digest for OpenCode task signature footers.
# If not already set, try Docker inspect via container ID from cgroup or hostname.
if [ -z "${CHETTER_RUNNER_IMAGE_DIGEST:-}" ]; then
  CID=""
  if [ -r /proc/self/cgroup ]; then
    CID=$(sed -n 's/.*\/docker[-/]\?\([[:xdigit:]]\{64\}\).*/\1/p' /proc/self/cgroup 2>/dev/null | head -1 || true)
  fi
  if [ -z "$CID" ] && [ -r /proc/1/cgroup ]; then
    CID=$(sed -n 's/.*\/docker[-/]\?\([[:xdigit:]]\{64\}\).*/\1/p' /proc/1/cgroup 2>/dev/null | head -1 || true)
  fi
  if [ -z "$CID" ] && [ -n "${HOSTNAME:-}" ]; then
    CID="$HOSTNAME"
  fi
  if [ -n "$CID" ] && command -v docker >/dev/null 2>&1; then
    DIGEST=$(docker inspect --format='{{index .RepoDigests 0}}' "$CID" 2>/dev/null | cut -d@ -f2 || true)
    if [ -n "${DIGEST:-}" ]; then
      CHETTER_RUNNER_IMAGE_DIGEST="sha256:${DIGEST#sha256:}"
    fi
  fi
fi
: "${CHETTER_RUNNER_IMAGE_DIGEST:=unknown}"
: "${CHETTER_RUNNER_IMAGE:=unknown}"
export CHETTER_RUNNER_IMAGE CHETTER_RUNNER_IMAGE_DIGEST

mkdir -p "$RUNNER_WORKSPACE_ROOT" /var/lib/chetter-runner/cache/go/pkg/mod /var/lib/chetter-runner/cache/go/build /var/lib/chetter-runner/cache/npm

cat > /tmp/runner.yaml <<EOF
nats:
  url: ${NATS_URL}

jetstream:
  enabled: true
  task_stream: ${JETSTREAM_TASK_STREAM}
  event_stream: ${JETSTREAM_EVENT_STREAM}
  task_durable: ${JETSTREAM_TASK_DURABLE}
  task_queue: ${JETSTREAM_TASK_QUEUE}
  ack_wait_seconds: ${JETSTREAM_ACK_WAIT_SECONDS}
  max_deliver: ${JETSTREAM_MAX_DELIVER}
  max_ack_pending: ${JETSTREAM_MAX_ACK_PENDING}
  storage: ${JETSTREAM_STORAGE}

runner:
  listen_subject: ${TASK_SUBJECT}
  result_subject: ${RESULT_SUBJECT}
  workspace_root: ${RUNNER_WORKSPACE_ROOT}
  max_concurrent: ${RUNNER_MAX_CONCURRENT}

proxy:
  listen_addr: :18080

dns:
  listen_addr: :5300
  upstream: 8.8.8.8:53

workspace: {}

git:
  ssh_key_path: ""
  pat: "${GITHUB_TOKEN:-}"

deploy:
  provider: local
  registry: "${DOCKER_REGISTRY:-}"
  chetter_url: chetter.flatout.works

chetter_mcp:
  url: "${CHETTER_MCP_URL:-}"
  auth_token: "${CHETTER_MCP_AUTH_TOKEN:-}"
EOF

exec runner -config /tmp/runner.yaml
