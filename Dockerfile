FROM node:24-bookworm AS web-build

WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web ./
RUN npm run build

FROM golang:1.26-bookworm AS build

WORKDIR /src
ARG GIT_COMMIT_SHA=unknown
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download
COPY main.go go.mod go.sum* ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY gen/ ./gen/
COPY pkg/ ./pkg/
COPY db/ ./db/
COPY --from=web-build /src/web/build ./internal/webui/dist
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -ldflags="-X 'main._gitHash=${GIT_COMMIT_SHA}'" -o /out/chetter ./ && \
    CGO_ENABLED=0 go build -o /out/chetter-migrate ./cmd/chetter-migrate

FROM debian:bookworm-slim
ENV DD_SOURCE=go
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    git \
    openssh-client \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 65532 nonroot \
    && useradd --uid 65532 --gid nonroot --shell /usr/sbin/nologin --no-create-home nonroot
COPY --from=build /out/chetter /chetter
COPY --from=build /out/chetter-migrate /usr/local/bin/chetter-migrate
COPY db/migrations /migrations
COPY db/postgres/migrations /migrations-postgres
COPY chetter-entrypoint.sh /usr/local/bin/chetter-entrypoint
RUN chmod 0755 /usr/local/bin/chetter-entrypoint
EXPOSE 8080 8090
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/chetter-entrypoint"]
