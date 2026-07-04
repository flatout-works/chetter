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

if [ "${SKIP_MAIN}" != "true" ]; then

  if [ "${BUILD_AGENT_BASE:-false}" = "true" ] || ! docker image inspect chetter-agent-base:latest >/dev/null 2>&1; then
    echo "=== Building agent-base image ==="
    docker build \
      -f runner/images/base/Dockerfile \
      -t "chetter-agent-base:latest" .
  fi

  echo "=== Building MCP image ==="
  GIT_HASH=$(git rev-parse --short HEAD)
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
