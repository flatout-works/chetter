#!/usr/bin/env bash
set -euo pipefail

# Build Chetter images on wowbagger, push them to GHCR, sync Arcane GitOps,
# and redeploy the Arcane project. This is intended to be called by a local
# wowbagger webhook, not by GitHub-hosted runners.

cd "$(dirname "$0")/.."

: "${REGISTRY:=ghcr.io/flatout-works}"
: "${TAG:=main}"
: "${GIT_REMOTE:=origin}"
: "${GIT_REF:=main}"
: "${ARCANE_CHETTER_PROJECT:=chetter}"

if [ -z "${GHCR_TOKEN:-}" ]; then
  echo "GHCR_TOKEN is required to push images to GHCR" >&2
  exit 1
fi

git fetch "$GIT_REMOTE" "$GIT_REF"
git checkout "$GIT_REF"
git pull --ff-only "$GIT_REMOTE" "$GIT_REF"

REGISTRY="$REGISTRY" TAG="$TAG" ./deploy/build-and-push.sh

if [ -n "${ARCANE_CHETTER_GITOPS_ID:-}" ]; then
  arcane-cli gitops sync "$ARCANE_CHETTER_GITOPS_ID" --yes
fi

arcane-cli projects redeploy "$ARCANE_CHETTER_PROJECT" --yes
