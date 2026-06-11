#!/bin/sh
set -eu

: "${NATS_URL:=nats://nats:4222}"
: "${TASK_SUBJECT:=devfleet.runner.tasks}"
: "${RESULT_SUBJECT:=devfleet.tasks}"
: "${RUNNER_WORKSPACE_ROOT:=/var/lib/devfleet-runner/workspaces}"
: "${RUNNER_MAX_CONCURRENT:=2}"
: "${JETSTREAM_TASK_STREAM:=DEVFLEET_TASKS}"
: "${JETSTREAM_EVENT_STREAM:=DEVFLEET_EVENTS}"
: "${JETSTREAM_TASK_DURABLE:=devfleet-runner}"
: "${JETSTREAM_TASK_QUEUE:=devfleet-runners}"
: "${JETSTREAM_ACK_WAIT_SECONDS:=10}"
: "${JETSTREAM_MAX_DELIVER:=3}"
: "${JETSTREAM_MAX_ACK_PENDING:=4}"
: "${JETSTREAM_STORAGE:=file}"

mkdir -p "$RUNNER_WORKSPACE_ROOT" /var/lib/devfleet-runner/cache/go/pkg/mod /var/lib/devfleet-runner/cache/go/build /var/lib/devfleet-runner/cache/npm

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
  devfleet_url: devfleet.works

devfleet_mcp:
  url: "${DEVFLEET_MCP_URL:-}"
  auth_token: "${DEVFLEET_MCP_AUTH_TOKEN:-}"
EOF

exec runner -config /tmp/runner.yaml
