.PHONY: build test vet lint check runner-test runner-vet runner-lint runner-check docker-build-mcp docker-build-runner-base docker-build-runner

MCP_IMAGE ?= ghcr.io/flatout-works/devfleet-mcp:local
RUNNER_BASE_IMAGE ?= ghcr.io/flatout-works/devfleet-runner-base:local
RUNNER_IMAGE ?= ghcr.io/flatout-works/devfleet-runner:local
build:
	mkdir -p bin
	go build -o bin/devfleet .

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
	docker build -f runner/Dockerfile.devfleet-base -t $(RUNNER_BASE_IMAGE) .

docker-build-runner:
	docker build -f runner/Dockerfile.devfleet -t $(RUNNER_IMAGE) .
