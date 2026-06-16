#!/bin/sh
set -eu

: "${CHETTER_SERVER_URL:=http://chetter-mcp:8080}"
: "${RUNNER_WORKSPACE_ROOT:=/var/lib/chetter-runner/workspaces}"
: "${RUNNER_MAX_CONCURRENT:=2}"

# Parse a Docker image reference and query its registry for the manifest digest.
# Supports docker.io, ghcr.io, and other registries implementing the Docker
# Registry HTTP API V2. Returns the digest (e.g. sha256:...) on stdout.
resolve_registry_digest() {
  local image_ref="$1"
  [ -n "$image_ref" ] || return 1

  local registry="registry-1.docker.io"
  local repo=""
  local tag="latest"

  # If the ref already contains a digest, return it directly.
  case "$image_ref" in
    *@sha256:*)
      echo "${image_ref#*@}"
      return 0
      ;;
  esac

  local rest="$image_ref"

  # Extract tag. A valid tag cannot contain '/', so a ':' followed by a '/'
  # is part of a registry port (e.g. localhost:5000/repo) and not a tag.
  if [ "${rest#*:}" != "$rest" ]; then
    local maybe_tag="${rest##*:}"
    if [ "${maybe_tag#*/}" = "$maybe_tag" ]; then
      tag="$maybe_tag"
      rest="${rest%:*}"
    fi
  fi

  # Split registry and repository.
  if [ "${rest#*/}" != "$rest" ]; then
    local first_part="${rest%%/*}"
    case "$first_part" in
      *.* | *:* | localhost)
        registry="$first_part"
        repo="${rest#*/}"
        ;;
      *)
        repo="$rest"
        ;;
    esac
  else
    repo="$rest"
  fi

  # Normalize common registry hosts for HTTPS.
  case "$registry" in
    docker.io) registry="registry-1.docker.io" ;;
  esac

  # Docker Hub official images need the library/ namespace.
  if [ "$registry" = "registry-1.docker.io" ] && [ "${repo#*/}" = "$repo" ]; then
    repo="library/$repo"
  fi

  [ -n "$repo" ] || return 1

  local manifest_url="https://$registry/v2/$repo/manifests/$tag"
  local accept_header="Accept: application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json"
  local headers realm service scope token digest

  headers=$(curl -sS -I -H "$accept_header" "$manifest_url" 2>/dev/null || true)

  # If authentication is required, fetch a bearer token.
  if echo "$headers" | grep -qi "^www-authenticate:"; then
    realm=$(echo "$headers" | grep -i "^www-authenticate:" | sed -n 's/.*realm="\([^"]*\)".*/\1/p' | head -1 | tr -d '\r')
    service=$(echo "$headers" | grep -i "^www-authenticate:" | sed -n 's/.*service="\([^"]*\)".*/\1/p' | head -1 | tr -d '\r')
    scope=$(echo "$headers" | grep -i "^www-authenticate:" | sed -n 's/.*scope="\([^"]*\)".*/\1/p' | head -1 | tr -d '\r')

    if [ -n "$realm" ] && [ -n "$scope" ]; then
      local token_url="$realm?service=$(printf '%s' "$service" | sed 's/ /%20/g')&scope=$(printf '%s' "$scope" | sed 's/ /%20/g')"
      local token_curl="curl -sS"
      # Private GHCR repositories need a PAT; GITHUB_TOKEN is always available
      # to the runner for git operations and package reads.
      if [ "$registry" = "ghcr.io" ] && [ -n "${GITHUB_TOKEN:-}" ]; then
        token_curl="$token_curl -u oauth2:$GITHUB_TOKEN"
      fi
      token=$($token_curl "$token_url" 2>/dev/null | sed -n 's/.*"token"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
      if [ -n "$token" ]; then
        headers=$(curl -sS -I -H "$accept_header" -H "Authorization: Bearer $token" "$manifest_url" 2>/dev/null || true)
      fi
    fi
  fi

  digest=$(echo "$headers" | grep -i "^docker-content-digest:" | awk '{print $2}' | tr -d '\r' | head -1)

  if [ -n "$digest" ]; then
    echo "$digest"
    return 0
  fi
  return 1
}

# Resolve runner image digest for OpenCode task signature footers.
# Order of resolution:
# 1. Already provided via CHETTER_RUNNER_IMAGE_DIGEST env var.
# 2. Image ref already contains a digest (image@sha256:...).
# 3. Docker inspect via container ID from cgroup/hostname (requires docker CLI).
# 4. Query the container registry for the tag's manifest digest.
# 5. Fall back to "unknown".
if [ -z "${CHETTER_RUNNER_IMAGE_DIGEST:-}" ] && [ -n "${CHETTER_RUNNER_IMAGE:-}" ]; then
  case "$CHETTER_RUNNER_IMAGE" in
    *@sha256:*)
      CHETTER_RUNNER_IMAGE_DIGEST="${CHETTER_RUNNER_IMAGE#*@}"
      ;;
  esac
fi

if [ -z "${CHETTER_RUNNER_IMAGE_DIGEST:-}" ]; then
  # Try Docker inspect if docker CLI is available.
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

if [ -z "${CHETTER_RUNNER_IMAGE_DIGEST:-}" ] && [ -n "${CHETTER_RUNNER_IMAGE:-}" ]; then
  # Query the registry directly. This works when the runner container does not
  # have the Docker CLI or socket mounted.
  CHETTER_RUNNER_IMAGE_DIGEST=$(resolve_registry_digest "$CHETTER_RUNNER_IMAGE" || true)
fi

: "${CHETTER_RUNNER_IMAGE_DIGEST:=unknown}"
: "${CHETTER_RUNNER_IMAGE:=unknown}"
export CHETTER_RUNNER_IMAGE CHETTER_RUNNER_IMAGE_DIGEST

mkdir -p "$RUNNER_WORKSPACE_ROOT" /var/lib/chetter-runner/cache/go/pkg/mod /var/lib/chetter-runner/cache/go/build /var/lib/chetter-runner/cache/npm

cat > /tmp/runner.yaml <<EOF
server:
  url: ${CHETTER_SERVER_URL}
  auth_token: "${CHETTER_RUNNER_AUTH_TOKEN:-${CHETTER_MCP_AUTH_TOKEN:-}}"

execution:
  harness: "${CHETTER_HARNESS:-opencode}"

runner:
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
