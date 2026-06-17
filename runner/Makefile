IMAGE ?= chetter/runner
CONTAINER ?= chetter-runner
CONFIG ?= $(CURDIR)/runner.docker.yaml
PROXY_PORT ?= 18080
STATICCHECK ?= $(shell go env GOPATH)/bin/staticcheck
STATICCHECK_VERSION ?= latest
ROOT := $(abspath $(CURDIR)/..)

.PHONY: local build rebuild verify-image preflight-netns rm run restart wait logs follow ps test vet lint check coverage

$(STATICCHECK):
	go install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)

local:
	go build -o runner ./cmd/runner
	go build -o mcp-bridge ./harness/mcp-bridge

build:
	docker build -f $(CURDIR)/Dockerfile.runner -t $(IMAGE) $(ROOT)

rebuild:
	docker build --no-cache -f $(CURDIR)/Dockerfile.runner -t $(IMAGE) $(ROOT)

verify-image:
	docker run --rm \
		-v /var/run/docker.sock:/var/run/docker.sock \
		--mount type=bind,source=/run/netns,target=/run/netns,bind-propagation=rshared \
		-v /tmp:/tmp \
		--entrypoint sh $(IMAGE) \
		-lc 'which docker || echo "no docker cli"; test "$${TMPDIR}" = "/tmp/chetter-runner"'

preflight-netns:
	@test -d /run/netns || { echo '/run/netns does not exist; run: sudo mkdir -p /run/netns'; exit 1; }
	@findmnt -no PROPAGATION /run/netns | grep -q shared || { echo '/run/netns is not shared; run: sudo mount --bind /run/netns /run/netns && sudo mount --make-rshared /run/netns'; exit 1; }

rm:
	docker rm -f $(CONTAINER) 2>/dev/null || true

run: preflight-netns rm
	docker run -d --name $(CONTAINER) \
		--privileged \
		-v /var/run/docker.sock:/var/run/docker.sock \
		--mount type=bind,source=/run/netns,target=/run/netns,bind-propagation=rshared \
		-v /tmp:/tmp \
		-v /var/lib/runner:/var/lib/runner \
		-v "$(CONFIG):/etc/runner/runner.yaml:ro" \
		-p $(PROXY_PORT):18080 \
		$(IMAGE)

restart: rebuild verify-image run wait

wait:
	sleep 2
	docker ps --filter name=$(CONTAINER)
	docker logs --tail=80 $(CONTAINER)

logs:
	docker logs --tail=200 $(CONTAINER)

follow:
	docker logs -f $(CONTAINER)

ps:
	docker ps --filter name=$(CONTAINER)

test:
	go test ./...

vet:
	go vet ./...

lint: $(STATICCHECK)
	$(STATICCHECK) ./...

check: test vet lint

coverage:
	@go test ./... -coverprofile=/tmp/chetter-runner-cover.out -count=1 -timeout 60s > /dev/null 2>&1; \
	go tool cover -func=/tmp/chetter-runner-cover.out | grep '^total:' | awk '{print "total        " $$3}'; \
	echo ""; \
	go test ./... -cover -count=1 -timeout 60s 2>&1 | grep 'coverage:' | grep -v 'coverage: 0\.0%' | grep -v '/gen/' | sort -t':' -k2 -rn
	@rm -f /tmp/chetter-runner-cover.out
