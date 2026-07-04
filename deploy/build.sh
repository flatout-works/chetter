#!/usr/bin/env bash
set -euo pipefail
# Build chetter images from the current checkout.
# Manual fallback: run this on wowbagger after a git sync to build images.
# Pushing to GHCR is deferred until we make proper GitHub releases.

cd "$(dirname "$0")/.."

echo "=== Building MCP image ==="
docker build -t chetter-mcp:latest .

echo "=== Building agent-base image ==="
docker build -f runner/images/base/Dockerfile -t chetter-agent-base:latest .

echo "=== Building runner daemon image ==="
docker build -f runner/Dockerfile.chetter -t chetter-runner:latest .

echo "=== Done ==="
