IMAGE ?= devfleet/runner
CONTAINER ?= devfleet-runner
CONFIG ?= $(CURDIR)/runner.docker.yaml
NATS_URL ?= nats://localhost:4222
HOST_NATS ?= host.docker.internal
PROXY_PORT ?= 18080
STATICCHECK ?= $(shell go env GOPATH)/bin/staticcheck
STATICCHECK_VERSION ?= latest

.PHONY: local build rebuild verify-image preflight-netns rm run restart wait logs follow ps smoke policy cycle policy-cycle test vet lint check coverage

$(STATICCHECK):
	go install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)

local:
	go build -o runner ./cmd/runner
	go build -o mcp-bridge ./harness/mcp-bridge

build:
	docker build -f Dockerfile.runner -t $(IMAGE) .

rebuild:
	docker build --no-cache -f Dockerfile.runner -t $(IMAGE) .

verify-image:
	docker run --rm \
		-v /run/containerd:/run/containerd \
		--mount type=bind,source=/run/netns,target=/run/netns,bind-propagation=rshared \
		-v /var/lib/containerd:/var/lib/containerd \
		-v /tmp:/tmp \
		--entrypoint sh $(IMAGE) \
		-lc 'which ctr && ctr version && test "$${TMPDIR}" = "/tmp/devfleet-runner"'

preflight-netns:
	@test -d /run/netns || { echo '/run/netns does not exist; run: sudo mkdir -p /run/netns'; exit 1; }
	@findmnt -no PROPAGATION /run/netns | grep -q shared || { echo '/run/netns is not shared; run: sudo mount --bind /run/netns /run/netns && sudo mount --make-rshared /run/netns'; exit 1; }

rm:
	docker rm -f $(CONTAINER) 2>/dev/null || true

run: preflight-netns rm
	docker run -d --name $(CONTAINER) \
		--privileged \
		--add-host=$(HOST_NATS):host-gateway \
		-v /run/containerd:/run/containerd \
		--mount type=bind,source=/run/netns,target=/run/netns,bind-propagation=rshared \
		-v /var/lib/containerd:/var/lib/containerd \
		-v /dev/kvm:/dev/kvm \
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

smoke:
	NATS_URL=$(NATS_URL) go run test/smoke_task.go

policy:
	NATS_URL=$(NATS_URL) go run test/policy_task.go

cycle: restart smoke

policy-cycle: restart policy

test:
	go test ./...

vet:
	go vet ./...

lint: $(STATICCHECK)
	$(STATICCHECK) ./...

check: test vet lint

coverage:
	@go test ./... -coverprofile=/tmp/devfleet-runner-cover.out -count=1 -timeout 60s > /dev/null 2>&1; \
	go tool cover -func=/tmp/devfleet-runner-cover.out | grep '^total:' | awk '{print "total        " $$3}'; \
	echo ""; \
	go test ./... -cover -count=1 -timeout 60s 2>&1 | grep 'coverage:' | grep -v 'coverage: 0\.0%' | grep -v '/gen/' | sort -t':' -k2 -rn
	@rm -f /tmp/devfleet-runner-cover.out
