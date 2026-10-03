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
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /out/niffler-serve-proxy ./cmd/niffler-serve-proxy

# --------------------------------------------------------------------------
# Niffler: build the pinned complete harness, not just its HTTP proxy.
# --------------------------------------------------------------------------
FROM golang:1.26-bookworm AS niffler-builder
ARG NIF_REV=3f7684bd0dab0b29f59318ad7fe281ea51578bd9
ARG NIM_VERSION=2.2.12
ARG NIM_SHA256=7df1611449a6842af69322aa2c1206942982650a5f6bc0d37bc8ec109932f638
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential ca-certificates curl git make xz-utils pkg-config \
    libssl-dev liblz4-dev libpcre3-dev libclang-dev clang libicu-dev \
    && rm -rf /var/lib/apt/lists/*
RUN test "$(uname -m)" = x86_64 \
    && curl -fsSL -o /opt/nim.tar.xz "https://nim-lang.org/download/nim-${NIM_VERSION}-linux_x64.tar.xz" \
    && echo "${NIM_SHA256}  /opt/nim.tar.xz" | sha256sum -c - \
    && mkdir /opt/nim \
    && tar -C /opt/nim --strip-components=1 -xJf /opt/nim.tar.xz \
    && rm /opt/nim.tar.xz
ENV PATH="/opt/nim/bin:${PATH}"
WORKDIR /opt/niffler
RUN git init . \
    && git remote add origin https://github.com/gokr/niffler.git \
    && git fetch --depth=1 origin "${NIF_REV}" \
    && git checkout --detach FETCH_HEAD \
    && test "$(git rev-parse HEAD)" = "${NIF_REV}"
RUN --mount=type=cache,target=/root/.nimble \
    --mount=type=cache,target=/root/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    make install-nim-deps \
    && make build \
    && mkdir -p /dist/niffler/var/bin \
    && git archive HEAD | tar -x -C /dist/niffler \
    && cp -a var/bin/. /dist/niffler/var/bin/ \
    && printf '%s\n' "${NIF_REV}" > /dist/niffler/REVISION \
    && rm -f /dist/niffler/nimble.paths \
    && cp -a /root/.nimble/pkgs2 /dist/nimble-pkgs2 \
    && test ! -e /dist/niffler/.env \
    && test ! -e /dist/niffler/var/store.db

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
    build-essential pkg-config libssl-dev liblz4-dev libpcre3-dev \
    libclang-dev clang libicu-dev util-linux procps \
    && rm -rf /var/lib/apt/lists/*

# Full Niffler distribution and self-extension toolchains are shared by every
# variant. The proxy creates a private runtime/store; never boot this template.
ARG NIF_REV=3f7684bd0dab0b29f59318ad7fe281ea51578bd9
LABEL dev.niffler.revision="${NIF_REV}"
COPY --from=niffler-builder /usr/local/go /usr/local/go
COPY --from=niffler-builder /opt/nim /opt/nim
COPY --from=niffler-builder /dist/nimble-pkgs2 /opt/niffler/nimble-pkgs2
COPY --from=niffler-builder /dist/niffler /opt/niffler
ENV PATH="/opt/nim/bin:/usr/local/go/bin:${PATH}"
RUN chmod -R a-w /opt/niffler \
    && test -x /opt/niffler/var/bin/niffler \
    && test -x /opt/niffler/var/bin/session \
    && test -x /opt/niffler/var/bin/store-sqlite \
    && test -x /opt/niffler/var/bin/nats-server

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
COPY --from=proxy-builder /out/niffler-serve-proxy /usr/local/bin/niffler-serve-proxy

# Pi coding agent (via npm) + MCP adapter extension.
ARG PI_AGENT_VERSION=0.84.3
RUN npm install -g --ignore-scripts @earendil-works/pi-coding-agent@${PI_AGENT_VERSION} \
    && ln -sf "$(npm root -g)/@earendil-works/pi-coding-agent/dist/bundle/cli.js" /usr/local/bin/pi \
    && pi --version

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
    # @mem9/opencode is pinned to 0.1.3: 0.1.6 changed its export shape and
    # crashes opencode 1.0.180's plugin loader (TypeError: fn3 is not a
    # function at src/plugin/index.ts), which turns every task's /config
    # readiness probe into a 500. Re-bump only after mem9 ships a release
    # verified against the pinned opencode version.
    && npm install @mem9/opencode@0.1.3 @opencode-ai/plugin@1.18.23 zod@4.4.3

ENV HOME=/opt/opencode
ENV XDG_CONFIG_HOME=/opt/opencode/.config
ENV GOMODCACHE=/var/lib/chetter-runner/cache/go/pkg/mod
ENV GOCACHE=/var/lib/chetter-runner/cache/go/build
ENV npm_config_cache=/var/lib/chetter-runner/cache/npm

WORKDIR /workspace
