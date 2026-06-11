#!/usr/bin/env sh
set -eu

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"

: "${REGISTRY:=ghcr.io/flatout-works}"
: "${TAG:=main}"

mcp_image="${MCP_IMAGE:-$REGISTRY/devfleet-mcp:$TAG}"
runner_base_image="${RUNNER_BASE_IMAGE:-$REGISTRY/devfleet-runner-base:$TAG}"
runner_image="${RUNNER_IMAGE:-$REGISTRY/devfleet-runner:$TAG}"

if [ -n "${GHCR_TOKEN:-}" ]; then
  : "${GHCR_USERNAME:=gokr}"
  printf '%s' "$GHCR_TOKEN" | docker login ghcr.io -u "$GHCR_USERNAME" --password-stdin
fi

cd "$repo_root"

docker build -t "$mcp_image" .
docker build -f runner/Dockerfile.devfleet-base -t "$runner_base_image" .
docker build -f runner/Dockerfile.devfleet -t "$runner_image" .

docker push "$mcp_image"
docker push "$runner_base_image"
docker push "$runner_image"
