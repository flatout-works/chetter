#!/usr/bin/env bash
set -euo pipefail

SRC="$HOME/chetter-src"

cd "$SRC"
OLD=$(git rev-parse HEAD)
git pull --ff-only 2>/dev/null || (git fetch --depth=1 origin main && git reset --hard origin/main)
NEW=$(git rev-parse HEAD)

if [ "$OLD" = "$NEW" ]; then
  echo "No changes since last pull, checking if variant builds were requested..."
  if [ "${BUILD_VARIANT_GOLANG:-false}" != "true" ] && \
     [ "${BUILD_VARIANT_PYTHON:-false}" != "true" ] && \
     [ "${BUILD_VARIANT_NODE:-false}" != "true" ] && \
     [ "${BUILD_VARIANT_RUST:-false}" != "true" ] && \
     [ "${BUILD_VARIANT_MINIMAL:-false}" != "true" ] && \
     [ "${BUILD_BASE:-false}" != "true" ]; then
    echo "No variant or base builds requested either — nothing to do."
    exit 0
  fi
  echo "Source unchanged but variant/base builds requested — building those only."
  SKIP_MAIN=true
else
  echo "Source updated: $OLD -> $NEW"
  SKIP_MAIN=false
fi

BASE_IMAGE_ARG="--build-arg BASE_IMAGE=chetter-runner-base:latest"

if [ "${SKIP_MAIN}" != "true" ]; then

  if [ "${BUILD_BASE:-false}" = "true" ] || ! docker image inspect chetter-runner-base:latest >/dev/null 2>&1; then
    echo "=== Building runner base image ==="
    docker build \
      -f runner/Dockerfile.chetter-base \
      -t "chetter-runner-base:latest" .
  fi

  echo "=== Building MCP image ==="
  docker build \
    -t "chetter-mcp:latest" .

  echo "=== Building runner image ==="
  docker build $BASE_IMAGE_ARG \
    -f runner/Dockerfile.chetter \
    -t "chetter-runner:latest" .
fi

build_variant() {
  local name="$1"
  local dockerfile="runner/images/$name/Dockerfile"
  echo "=== Building $name variant ==="
  docker build $BASE_IMAGE_ARG \
    -f "$dockerfile" \
    -t "chetter-runner-$name:latest" .
}

[ "${BUILD_VARIANT_GOLANG:-false}" = "true" ]  && build_variant golang
[ "${BUILD_VARIANT_PYTHON:-false}" = "true" ]  && build_variant python
[ "${BUILD_VARIANT_NODE:-false}" = "true" ]    && build_variant node
[ "${BUILD_VARIANT_RUST:-false}" = "true" ]    && build_variant rust
[ "${BUILD_VARIANT_MINIMAL:-false}" = "true" ] && build_variant minimal

echo "=== Done ==="
