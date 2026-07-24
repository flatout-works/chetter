.PHONY: install generate tools build web-build web-check test test-postgres vet lint check runner-test runner-vet runner-lint runner-check migrate migrate-status migrate-down migrate-create docker-build-mcp docker-build-agent-base docker-build-runner

MCP_IMAGE ?= ghcr.io/flatout-works/chetter-mcp:local
AGENT_BASE_IMAGE ?= ghcr.io/flatout-works/chetter-agent-base:local
RUNNER_IMAGE ?= ghcr.io/flatout-works/chetter-runner:local
DB_DSN ?= root@tcp(127.0.0.1:4000)/chetter?parseTime=true
CHETTER_DB_DIALECT ?= tidb
GOOSE_DIALECT := $(if $(filter postgres postgresql,$(CHETTER_DB_DIALECT)),postgres,$(if $(filter tidb,$(CHETTER_DB_DIALECT)),tidb,mysql))
GOOSE_MIGRATIONS := $(if $(filter postgres postgresql,$(CHETTER_DB_DIALECT)),db/postgres/migrations,db/migrations)
GOOSE_DSN := $(subst tls=tidb,tls=true,$(DB_DSN))
BIN_DIR := $(CURDIR)/bin
WEB_EMBED_DIR := internal/webui/dist
BUF := $(BIN_DIR)/buf
SQLC := $(BIN_DIR)/sqlc
STATICCHECK := $(BIN_DIR)/staticcheck
BUF_VERSION := v1.69.0
SQLC_VERSION := v1.31.1
STATICCHECK_VERSION := 2025.1.1

install:
	go mod download

generate: tools
	$(BUF) dep update
	$(BUF) generate
	$(SQLC) generate
	go generate ./internal/data

tools: $(BUF) $(SQLC)

$(BUF):
	GOBIN=$(BIN_DIR) go install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)

$(SQLC):
	GOBIN=$(BIN_DIR) go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)

build: web-build
	mkdir -p bin
	go build -ldflags="-X 'main._gitHash=$(shell git rev-parse --short HEAD)'" -o bin/chetter .
	go build -o bin/chetterctl ./cmd/chetterctl

web-build:
	npm --prefix web ci
	npm --prefix web run build
	mkdir -p $(WEB_EMBED_DIR)
	rm -rf $(WEB_EMBED_DIR)/*
	cp -R web/build/. $(WEB_EMBED_DIR)/

web-check:
	if [ ! -d web/node_modules ] || [ web/package-lock.json -nt web/node_modules/.package-lock.json ]; then \
		npm --prefix web ci; \
	fi
	npm --prefix web run check

migrate:
	go run github.com/pressly/goose/v3/cmd/goose@latest -dir $(GOOSE_MIGRATIONS) $(GOOSE_DIALECT) "$(GOOSE_DSN)" up

migrate-status:
	go run github.com/pressly/goose/v3/cmd/goose@latest -dir $(GOOSE_MIGRATIONS) $(GOOSE_DIALECT) "$(GOOSE_DSN)" status

migrate-down:
	go run github.com/pressly/goose/v3/cmd/goose@latest -dir $(GOOSE_MIGRATIONS) $(GOOSE_DIALECT) "$(GOOSE_DSN)" down

migrate-create:
	@read -p "Migration name: " name; \
	go run github.com/pressly/goose/v3/cmd/goose@latest -dir $(GOOSE_MIGRATIONS) -s create $$name sql

test:
	go test ./...

test-postgres:
	CHETTER_TEST_DB_DIALECT=postgres go test ./...

vet:
	go vet ./...

$(STATICCHECK):
	GOBIN=$(BIN_DIR) go install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)

lint: $(STATICCHECK)
	$(STATICCHECK) ./...

runner-test:
	$(MAKE) -C runner test

runner-vet:
	$(MAKE) -C runner vet

runner-lint:
	$(MAKE) -C runner lint

runner-check:
	$(MAKE) -C runner check

check:
	$(MAKE) -j3 check-root check-web check-runner

check-root: test vet lint

check-web:
	$(MAKE) web-check

check-runner:
	$(MAKE) runner-check

docker-build-mcp:
	docker build -t $(MCP_IMAGE) .

docker-build-agent-base:
	docker build -f runner/images/base/Dockerfile -t $(AGENT_BASE_IMAGE) .

docker-build-runner:
	docker build -f runner/Dockerfile.chetter -t $(RUNNER_IMAGE) .
