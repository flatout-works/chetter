FROM golang:1.26-bookworm AS build

WORKDIR /src
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download
ARG CACHEBUST
RUN echo "$CACHEBUST" > /dev/null
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -o /out/chetter ./

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/chetter /chetter
EXPOSE 8080
ENTRYPOINT ["/chetter"]
