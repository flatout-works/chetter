FROM node:24-bookworm AS web-build

WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web ./
RUN npm run build

FROM golang:1.26-bookworm AS build

WORKDIR /src
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download
ARG CACHEBUST
RUN echo "$CACHEBUST" > /dev/null
COPY . .
COPY --from=web-build /src/web/build ./internal/webui/dist
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -o /out/chetter ./

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    git \
    openssh-client \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 65532 nonroot \
    && useradd --uid 65532 --gid nonroot --shell /usr/sbin/nologin --no-create-home nonroot
COPY --from=build /out/chetter /chetter
EXPOSE 8080 8090
USER 65532:65532
ENTRYPOINT ["/chetter"]
