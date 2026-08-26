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

ARG GH_VERSION=2.98.0
ARG GH_SHA256_AMD64=3b8ac6b30336802fc1a858d7c084e11cdf24ac1a761ca90b68022d7d729208de
ARG GH_SHA256_ARM64=cf689084f3a3618f7eae4a2420d335d74626d65f5e594b9828d125d69f800d86
RUN GH_ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/') \
    && GH_SHA256=$(eval echo "\$GH_SHA256_$(echo $GH_ARCH | tr '[:lower:]' '[:upper:]')") \
    && curl -fsSL -o /tmp/gh.tar.gz \
       "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_${GH_ARCH}.tar.gz" \
    && echo "${GH_SHA256}  /tmp/gh.tar.gz" | sha256sum -c - \
    && tar -C /usr/local/bin --strip-components=2 -xzf /tmp/gh.tar.gz "gh_${GH_VERSION}_linux_${GH_ARCH}/bin/gh" \
    && rm /tmp/gh.tar.gz && chmod +x /usr/local/bin/gh

ARG DOCKER_VERSION=29.7.2
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
    xz-utils \
    && rm -rf /var/lib/apt/lists/*

# Pi requires Node >= 22.19.0; Claude Code supports >= 18 and works with 22.
# Download Node.js directly from nodejs.org (with SHA256 verification) instead
# of piping the NodeSource setup script through bash. Requires curl + xz-utils,
# installed above.
ARG NODE_VERSION=22.22.0
ARG NODE_SHA256_X64=9aa8e9d2298ab68c600bd6fb86a6c13bce11a4eca1ba9b39d79fa021755d7c37
ARG NODE_SHA256_ARM64=1bf1eb9ee63ffc4e5d324c0b9b62cf4a289f44332dfef9607cea1a0d9596ba6f
RUN NODE_ARCH=$(uname -m | sed 's/x86_64/x64/;s/aarch64/arm64/') \
    && NODE_SHA256=$(eval echo "\$NODE_SHA256_$(echo $NODE_ARCH | tr '[:lower:]' '[:upper:]')") \
    && curl -fsSL -o /tmp/node.tar.xz \
       "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${NODE_ARCH}.tar.xz" \
    && echo "${NODE_SHA256}  /tmp/node.tar.xz" | sha256sum -c - \
    && tar -C /usr/local --strip-components=1 -xJf /tmp/node.tar.xz \
    && rm /tmp/node.tar.xz \
    && node --version && npm --version

COPY --from=binary-downloader /usr/local/bin/gh /usr/local/bin/gh-real
COPY --chmod=0755 runner/scripts/gh /usr/local/bin/gh
COPY --from=binary-downloader /usr/local/bin/docker /usr/local/bin/docker

# OpenCode (pinned version with SHA256 verification, instead of piping the
# unpinned installer through bash). Asset naming matches the official install
# script: opencode-linux-<arch>.tar.gz with a single top-level binary.
ARG OPENCODE_VERSION=1.0.180
ARG OPENCODE_SHA256_X64=d0fb7e00ecdcb0679d796396809b8725aa6f58955c79faa5fc56d6a0723dbebd
ARG OPENCODE_SHA256_ARM64=93497d5a20ab49560e0309b662f8d82b148476ab8a062d87dc3885d10f08078b
RUN OC_ARCH=$(uname -m | sed 's/x86_64/x64/;s/aarch64/arm64/') \
    && OC_SHA256=$(eval echo "\$OPENCODE_SHA256_$(echo $OC_ARCH | tr '[:lower:]' '[:upper:]')") \
    && curl -fsSL -o /tmp/opencode.tar.gz \
       "https://github.com/anomalyco/opencode/releases/download/v${OPENCODE_VERSION}/opencode-linux-${OC_ARCH}.tar.gz" \
    && echo "${OC_SHA256}  /tmp/opencode.tar.gz" | sha256sum -c - \
    && tar -C /tmp -xzf /tmp/opencode.tar.gz \
    && mv /tmp/opencode /usr/local/bin/opencode \
    && chmod +x /usr/local/bin/opencode \
    && rm -f /tmp/opencode.tar.gz

# Claude Code (via npm)
ARG CLAUDE_CODE_VERSION=2.1.245
RUN npm install -g @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION} \
    && claude --version

# CodeWhale (via npm)
ARG CODEWHALE_VERSION=0.9.11
RUN npm install -g codewhale@${CODEWHALE_VERSION} \
    && codewhale --version

# Codex CLI. Pin its App Server protocol because the Chetter bridge relies on it.
ARG CODEX_VERSION=0.149.1
RUN npm install -g @openai/codex@${CODEX_VERSION} \
    && codex --version

# claude-serve-proxy — Go HTTP wrapper for Claude CLI headless mode
COPY --from=proxy-builder /out/claude-serve-proxy /usr/local/bin/claude-serve-proxy
COPY --from=proxy-builder /out/codex-serve-proxy /usr/local/bin/codex-serve-proxy

# Pi coding agent (via npm) + MCP adapter extension.
ARG PI_AGENT_VERSION=0.84.3
RUN npm install -g --ignore-scripts @earendil-works/pi-coding-agent@${PI_AGENT_VERSION} \
    && ln -sf "$(npm root -g)/node_modules/.bin/pi" /usr/local/bin/pi

RUN mkdir -p /opt/pi-extensions \
    && cd /opt/pi-extensions \
    && npm init -y \
    && npm install --ignore-scripts pi-mcp-adapter@2.27.0

ENV PI_MCP_ADAPTER_PATH=/opt/pi-extensions/node_modules/pi-mcp-adapter

# npm packages for OpenCode plugins
RUN --mount=type=cache,target=/root/.npm \
    mkdir -p /opt/opencode/.config/opencode \
    && cd /opt/opencode/.config/opencode \
    && npm init -y \
    && npm install @mem9/opencode@0.1.6 @opencode-ai/plugin@1.18.23 zod@4.4.3

ENV HOME=/opt/opencode
ENV XDG_CONFIG_HOME=/opt/opencode/.config
ENV GOMODCACHE=/var/lib/chetter-runner/cache/go/pkg/mod
ENV GOCACHE=/var/lib/chetter-runner/cache/go/build
ENV npm_config_cache=/var/lib/chetter-runner/cache/npm

WORKDIR /workspace
