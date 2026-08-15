# syntax=docker/dockerfile:1.7

# Chetter Agent Base — Harness Runtime Layer
# =========================================================================
# Base image for per-task agent containers. Contains agent harness CLIs and
# common runtime tools. Language/toolchain-specific packages are added by
# runner/images/<stack>/Dockerfile.
# =========================================================================

# --------------------------------------------------------------------------
# Stage 1: Download standalone binaries
# --------------------------------------------------------------------------
FROM debian:bookworm-slim AS binary-downloader

RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    apt-get update && apt-get install -y --no-install-recommends ca-certificates curl

ARG GH_VERSION=2.70.0
RUN GH_ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/') \
    && curl -fsSL -o /tmp/gh.tar.gz \
       "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_${GH_ARCH}.tar.gz" \
    && tar -C /usr/local/bin --strip-components=2 -xzf /tmp/gh.tar.gz "gh_${GH_VERSION}_linux_${GH_ARCH}/bin/gh" \
    && rm /tmp/gh.tar.gz && chmod +x /usr/local/bin/gh

ARG DOCKER_VERSION=27.5.1
ARG TARGETARCH
RUN case "$TARGETARCH" in \
      amd64) docker_arch=x86_64 ;; \
      arm64) docker_arch=aarch64 ;; \
      *) echo "unsupported Docker CLI architecture: $TARGETARCH" >&2; exit 1 ;; \
    esac \
    && curl -fsSL "https://download.docker.com/linux/static/stable/${docker_arch}/docker-${DOCKER_VERSION}.tgz" \
    | tar -C /usr/local/bin --strip-components=1 -xzf - docker/docker \
    && chmod +x /usr/local/bin/docker

# --------------------------------------------------------------------------
# Stage 2: Build harness serve proxies
# --------------------------------------------------------------------------
FROM ghcr.io/hmbown/codewhale:v0.8.67 AS codewhale-release

FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS proxy-builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src
COPY go.mod go.sum* ./
COPY gen/ ./gen/
COPY runner/go.mod runner/go.sum* ./runner/
WORKDIR /src/runner
RUN go mod download 2>/dev/null || true
WORKDIR /src
COPY runner/ ./runner/
WORKDIR /src/runner
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags="-s -w" -o /out/claude-serve-proxy ./cmd/claude-serve-proxy
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags="-s -w" -o /out/codex-serve-proxy ./cmd/codex-serve-proxy

# --------------------------------------------------------------------------
# Stage 3: Final agent base image
# --------------------------------------------------------------------------
FROM debian:bookworm-slim

ARG TARGETARCH

# Pi requires Node >= 22.19.0; Claude Code supports >= 18 and works with 22.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    apt-get update && apt-get install -y --no-install-recommends ca-certificates curl \
    && curl -fsSL https://deb.nodesource.com/setup_22.x | bash -

RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    if [ "$TARGETARCH" = arm64 ]; then \
      codewhale_runtime_packages='libcap2 libdbus-1-3 libgcc-s1 libgcrypt20 libgpg-error0 liblz4-1 liblzma5 libsystemd0 libzstd1'; \
    else \
      codewhale_runtime_packages=''; \
    fi \
    && apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    git \
    openssh-client \
    socat \
    make \
    jq \
    ripgrep \
    nodejs \
    $codewhale_runtime_packages \
    && rm -rf /var/lib/apt/lists/*

COPY --from=binary-downloader /usr/local/bin/gh /usr/local/bin/gh-real
COPY --chmod=0755 runner/scripts/gh /usr/local/bin/gh
COPY --from=binary-downloader /usr/local/bin/docker /usr/local/bin/docker
COPY --from=codewhale-release /usr/local/bin/codewhale /opt/chetter-codewhale/codewhale
COPY --from=codewhale-release /usr/local/bin/codewhale-tui /opt/chetter-codewhale/codewhale-tui

# OpenCode
RUN curl -fsSL https://opencode.ai/install | bash \
    && ln -sf "$HOME/.opencode/bin/opencode" /usr/local/bin/opencode

# Claude Code (via npm)
RUN npm install -g @anthropic-ai/claude-code \
    && claude --version

# CodeWhale's npm arm64 binary requires glibc >= 2.39. The matching upstream
# release image supplies a Bookworm-compatible arm64 binary; amd64 retains the
# existing npm installation path and its verified MCP config contract.
RUN if [ "$TARGETARCH" = arm64 ]; then \
      install -m 0755 /opt/chetter-codewhale/codewhale /usr/local/bin/codewhale \
      && install -m 0755 /opt/chetter-codewhale/codewhale-tui /usr/local/bin/codewhale-tui; \
    else \
      npm install -g codewhale@0.8.67; \
    fi \
    && rm -rf /opt/chetter-codewhale \
    && codewhale --version

# Codex CLI. Pin its App Server protocol because the Chetter bridge relies on it.
ARG CODEX_VERSION=0.144.1
RUN npm install -g @openai/codex@${CODEX_VERSION} \
    && codex --version

# claude-serve-proxy — Go HTTP wrapper for Claude CLI headless mode
COPY --from=proxy-builder /out/claude-serve-proxy /usr/local/bin/claude-serve-proxy
COPY --from=proxy-builder /out/codex-serve-proxy /usr/local/bin/codex-serve-proxy

# Pi coding agent (via npm) + MCP adapter extension.
RUN npm install -g --ignore-scripts @earendil-works/pi-coding-agent \
    && ln -sf "$(npm root -g)/node_modules/.bin/pi" /usr/local/bin/pi

RUN mkdir -p /opt/pi-extensions \
    && cd /opt/pi-extensions \
    && npm init -y \
    && npm install --ignore-scripts pi-mcp-adapter@2.11.0

ENV PI_MCP_ADAPTER_PATH=/opt/pi-extensions/node_modules/pi-mcp-adapter

# npm packages for OpenCode plugins
RUN --mount=type=cache,target=/root/.npm \
    mkdir -p /opt/opencode/.config/opencode \
    && cd /opt/opencode/.config/opencode \
    && npm init -y \
    && npm install @mem9/opencode@0.1.3 @opencode-ai/plugin@1.14.19 zod@4.1.8

ENV HOME=/opt/opencode
ENV XDG_CONFIG_HOME=/opt/opencode/.config
ENV GOMODCACHE=/var/lib/chetter-runner/cache/go/pkg/mod
ENV GOCACHE=/var/lib/chetter-runner/cache/go/build
ENV npm_config_cache=/var/lib/chetter-runner/cache/npm

WORKDIR /workspace
