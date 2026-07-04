#!/usr/bin/env bash
set -euo pipefail

SRC="$HOME/chetter-src"

cd "$SRC"
OLD=$(git rev-parse HEAD)
git pull --ff-only 2>/dev/null || (git fetch --depth=1 origin main && git reset --hard origin/main)
NEW=$(git rev-parse HEAD)

if [ "$OLD" = "$NEW" ]; then
  echo "No changes since last pull, checking if agent-base build was requested..."
  if [ "${BUILD_AGENT_BASE:-false}" != "true" ]; then
    echo "No agent-base build requested either — nothing to do."
    exit 0
  fi
  echo "Source unchanged but agent-base build requested — building that only."
  SKIP_MAIN=true
else
  echo "Source updated: $OLD -> $NEW"
  SKIP_MAIN=false
fi

# Authenticate to GHCR for pushing agent-base.
# GITHUB_TOKEN is passed from the CI workflow via SSH env.
if [ -n "${GITHUB_TOKEN:-}" ]; then
  echo "$GITHUB_TOKEN" | docker login ghcr.io -u gokr --password-stdin
fi

if [ "${SKIP_MAIN}" != "true" ]; then

  GIT_HASH=$(git rev-parse --short HEAD)

  if [ "${BUILD_AGENT_BASE:-false}" = "true" ] || ! docker image inspect chetter-agent-base:latest >/dev/null 2>&1; then
    echo "=== Building agent-base image ==="
    docker build \
      -f runner/images/base/Dockerfile \
      -t "chetter-agent-base:latest" \
      -t "ghcr.io/flatout-works/chetter-agent-base:$GIT_HASH" \
      -t "ghcr.io/flatout-works/chetter-agent-base:main" .
    echo "=== Pushing agent-base to GHCR ==="
    docker push "ghcr.io/flatout-works/chetter-agent-base:$GIT_HASH"
    docker push "ghcr.io/flatout-works/chetter-agent-base:main"
  fi

  echo "=== Building MCP image ==="
  docker build \
    --build-arg GIT_HASH="$GIT_HASH" \
    -t "chetter-mcp:latest" .

  echo "=== Building runner daemon image ==="
  docker build \
    --build-arg GIT_HASH="$GIT_HASH" \
    -f runner/Dockerfile.chetter \
    -t "chetter-runner:latest" .
fi

echo "=== Done ==="
