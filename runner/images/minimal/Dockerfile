# syntax=docker/dockerfile:1.7

# Chetter Runner — Minimal Variant
# =========================================================================
# Lightweight image with only opencode, claude-code, git, and curl.
# No language toolchain — suitable for generic tasks, documentation,
# or as a starting point for custom variants.
#
# Does NOT inherit chetter-runner-base. Builds from debian:bookworm-slim
# to keep the image small.
# =========================================================================

FROM golang:1.26-bookworm AS runner-builder

ARG CACHEBUST
WORKDIR /src
COPY go.mod go.sum* ./
COPY gen/ ./gen/
COPY runner/go.mod runner/go.sum* ./runner/
WORKDIR /src/runner
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download
WORKDIR /src
COPY runner/ ./runner/
WORKDIR /src/runner
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /out/runner ./cmd/runner

FROM golang:1.26-bookworm AS mcp-bridge-builder

WORKDIR /build
COPY runner/harness/mcp-bridge/main.go ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /out/mcp-bridge ./main.go

FROM debian:bookworm-slim

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

RUN curl -fsSL https://opencode.ai/install | bash \
    && ln -sf "$HOME/.opencode/bin/opencode" /usr/local/bin/opencode

RUN npm install -g @anthropic-ai/claude-code \
    && ln -sf "$(npm root -g)/node_modules/.bin/claude" /usr/local/bin/claude

RUN npm install -g --ignore-scripts @earendil-works/pi-coding-agent \
    && ln -sf "$(npm root -g)/node_modules/.bin/pi" /usr/local/bin/pi

RUN mkdir -p /opt/pi-extensions \
    && cd /opt/pi-extensions \
    && npm init -y \
    && npm install --ignore-scripts pi-mcp-adapter

ENV PI_MCP_ADAPTER_PATH=/opt/pi-extensions/node_modules/pi-mcp-adapter

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

COPY --from=runner-builder /out/runner /usr/local/bin/runner
COPY --from=mcp-bridge-builder /out/mcp-bridge /usr/local/bin/mcp-bridge
COPY runner/chetter-entrypoint.sh /usr/local/bin/chetter-entrypoint
# Agent and skill definitions are injected at runtime.

RUN chmod +x /usr/local/bin/runner /usr/local/bin/mcp-bridge /usr/local/bin/chetter-entrypoint

ENV RUNNER_LOCAL=true
ENV RUNNER_WORKSPACE_ROOT=/var/lib/chetter-runner/workspaces

WORKDIR /var/lib/chetter-runner/workspaces
ENTRYPOINT ["chetter-entrypoint"]
