.PHONY: generate tools build test vet lint check runner-test runner-vet runner-lint runner-check migrate migrate-status migrate-down migrate-create docker-build-mcp docker-build-runner-base docker-build-runner

MCP_IMAGE ?= ghcr.io/flatout-works/chetter-mcp:local
RUNNER_BASE_IMAGE ?= ghcr.io/flatout-works/chetter-runner-base:local
RUNNER_IMAGE ?= ghcr.io/flatout-works/chetter-runner:local
DB_DSN ?= root@tcp(127.0.0.1:4000)/chetter?parseTime=true
BIN_DIR := $(CURDIR)/bin
BUF := $(BIN_DIR)/buf
SQLC := $(BIN_DIR)/sqlc
BUF_VERSION := v1.69.0
SQLC_VERSION := v1.31.1

generate: tools
	$(BUF) dep update
	$(BUF) generate
	$(SQLC) generate

tools: $(BUF) $(SQLC)

$(BUF):
	GOBIN=$(BIN_DIR) go install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)

$(SQLC):
	GOBIN=$(BIN_DIR) go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)

build:
	mkdir -p bin
	go build -o bin/chetter .
	go build -o bin/chetterctl ./cmd/chetterctl

migrate:
	go run github.com/pressly/goose/v3/cmd/goose@latest -dir db/migrations mysql "$(DB_DSN)" up

migrate-status:
	go run github.com/pressly/goose/v3/cmd/goose@latest -dir db/migrations mysql "$(DB_DSN)" status

migrate-down:
	go run github.com/pressly/goose/v3/cmd/goose@latest -dir db/migrations mysql "$(DB_DSN)" down

migrate-create:
	@read -p "Migration name: " name; \
	go run github.com/pressly/goose/v3/cmd/goose@latest -dir db/migrations -s create $$name sql

test:
	go test ./...

vet:
	go vet ./...

lint:
	go run honnef.co/go/tools/cmd/staticcheck@latest ./...

runner-test:
	$(MAKE) -C runner test

runner-vet:
	$(MAKE) -C runner vet

runner-lint:
	$(MAKE) -C runner lint

runner-check:
	$(MAKE) -C runner check

check: test vet lint runner-check

docker-build-mcp:
	docker build -t $(MCP_IMAGE) .

docker-build-runner-base:
	docker build -f runner/Dockerfile.chetter-base -t $(RUNNER_BASE_IMAGE) .

docker-build-runner:
	docker build -f runner/Dockerfile.chetter -t $(RUNNER_IMAGE) .
