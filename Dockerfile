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

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/chetter /chetter
EXPOSE 8080 8090
ENTRYPOINT ["/chetter"]
