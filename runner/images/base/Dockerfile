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
RUN curl -fsSL "https://download.docker.com/linux/static/stable/x86_64/docker-${DOCKER_VERSION}.tgz" \
    | tar -C /usr/local/bin --strip-components=1 -xzf - docker/docker \
    && chmod +x /usr/local/bin/docker

# --------------------------------------------------------------------------
# Stage 2: Build harness serve proxies
# --------------------------------------------------------------------------
FROM golang:1.26-bookworm AS proxy-builder

WORKDIR /src
COPY go.mod go.sum* ./
COPY gen/ ./gen/
COPY runner/go.mod runner/go.sum* ./runner/
WORKDIR /src/runner
RUN go mod download 2>/dev/null || true
WORKDIR /src
COPY runner/ ./runner/
WORKDIR /src/runner
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /out/claude-serve-proxy ./cmd/claude-serve-proxy
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /out/codex-serve-proxy ./cmd/codex-serve-proxy

# --------------------------------------------------------------------------
# Stage 3: Final agent base image
# --------------------------------------------------------------------------
FROM debian:bookworm-slim

# Pi requires Node >= 22.19.0; Claude Code supports >= 18 and works with 22.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    apt-get update && apt-get install -y --no-install-recommends ca-certificates curl \
    && curl -fsSL https://deb.nodesource.com/setup_22.x | bash -

RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    git \
    openssh-client \
    socat \
    make \
    jq \
    ripgrep \
    nodejs \
    && rm -rf /var/lib/apt/lists/*

COPY --from=binary-downloader /usr/local/bin/gh /usr/local/bin/gh-real
COPY --chmod=0755 runner/scripts/gh /usr/local/bin/gh
COPY --from=binary-downloader /usr/local/bin/docker /usr/local/bin/docker

# OpenCode
RUN curl -fsSL https://opencode.ai/install | bash \
    && ln -sf "$HOME/.opencode/bin/opencode" /usr/local/bin/opencode

# Claude Code (via npm)
RUN npm install -g @anthropic-ai/claude-code \
    && claude --version

# CodeWhale (via npm)
RUN npm install -g codewhale@0.8.67 \
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
