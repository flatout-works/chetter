#!/usr/bin/env bash
set -euo pipefail

SRC="$HOME/chetter-src"
REGISTRY="ghcr.io/flatout-works"
TAG="main"

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

CACHEBUST="${NEW:0:7}"
BASE_IMAGE_ARG="--build-arg BASE_IMAGE=$REGISTRY/chetter-runner-base:$TAG"

if [ "${SKIP_MAIN}" != "true" ]; then

  if [ "${BUILD_BASE:-false}" = "true" ]; then
    echo "=== Building runner base image ==="
    docker build --build-arg "CACHEBUST=$CACHEBUST" \
      -f runner/Dockerfile.chetter-base \
      -t "$REGISTRY/chetter-runner-base:$TAG" \
      -t "chetter-runner-base:latest" .
  fi

  echo "=== Building MCP image ==="
  docker build --build-arg "CACHEBUST=$CACHEBUST" \
    -t "$REGISTRY/chetter-mcp:$TAG" \
    -t "chetter-mcp:latest" .

  echo "=== Building runner image ==="
  docker build $BASE_IMAGE_ARG \
    --build-arg "CACHEBUST=$CACHEBUST" \
    -f runner/Dockerfile.chetter \
    -t "$REGISTRY/chetter-runner:$TAG" \
    -t "chetter-runner:latest" .
fi

build_variant() {
  local name="$1"
  local dockerfile="runner/images/$name/Dockerfile"
  local img="$REGISTRY/chetter-runner:$TAG"
  echo "=== Building $name variant ==="
  docker build $BASE_IMAGE_ARG \
    --build-arg "CACHEBUST=$CACHEBUST" \
    -f "$dockerfile" \
    -t "$REGISTRY/chetter-runner:$name" \
    -t "chetter-runner-$name:latest" .
}

[ "${BUILD_VARIANT_GOLANG:-false}" = "true" ]  && build_variant golang
[ "${BUILD_VARIANT_PYTHON:-false}" = "true" ]  && build_variant python
[ "${BUILD_VARIANT_NODE:-false}" = "true" ]    && build_variant node
[ "${BUILD_VARIANT_RUST:-false}" = "true" ]    && build_variant rust
[ "${BUILD_VARIANT_MINIMAL:-false}" = "true" ] && build_variant minimal

echo "=== Done ==="
