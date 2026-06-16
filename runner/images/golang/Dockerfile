# syntax=docker/dockerfile:1.7

# Chetter Runner — Golang Variant
# =========================================================================
# Full Go development environment with opencode, claude-code, buf, sqlc,
# goose, govulncheck, osv-scanner, gh, hcloud, and MySQL client.
# This is the default variant used for Go-based projects.
# =========================================================================

ARG BASE_IMAGE=ghcr.io/flatout-works/chetter-runner-base:main

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

FROM ${BASE_IMAGE}

COPY --from=runner-builder /out/runner /usr/local/bin/runner
COPY --from=mcp-bridge-builder /out/mcp-bridge /usr/local/bin/mcp-bridge
COPY runner/chetter-entrypoint.sh /usr/local/bin/chetter-entrypoint

COPY tools/skills/ /opt/opencode/.agents/skills/
COPY .opencode/agent/ /opt/opencode/.config/opencode/agent/

RUN chmod +x /usr/local/bin/runner /usr/local/bin/mcp-bridge /usr/local/bin/chetter-entrypoint \
    && chmod -R 755 /opt/opencode/.agents/skills /opt/opencode/.config/opencode/agent

ENV RUNNER_LOCAL=true
ENV RUNNER_WORKSPACE_ROOT=/var/lib/chetter-runner/workspaces

WORKDIR /var/lib/chetter-runner/workspaces
ENTRYPOINT ["chetter-entrypoint"]
