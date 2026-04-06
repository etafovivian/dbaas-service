BINARY     := dbaas-service
AGENT      := dbaas-agent
CMD_DIR    := ./cmd/dbaas-service
AGENT_DIR  := ./cmd/dbaas-agent
BUILD_DIR  := ./bin
IMAGE_NAME := ghcr.io/etafovivian/dbaas-service
AGENT_IMAGE := ghcr.io/etafovivian/dbaas-agent
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

.PHONY: build agent test lint migrate docker docker-agent clean

build:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=$(VERSION)" -o $(BUILD_DIR)/$(BINARY) $(CMD_DIR)

agent:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=$(VERSION)" -o $(BUILD_DIR)/$(AGENT) $(AGENT_DIR)

test:
	go test -race -timeout 120s ./...

lint:
	golangci-lint run ./...

migrate:
	@echo "Running migrations against $$DBAAS_SERVICE_DATABASE_URL"
	go run $(CMD_DIR)/main.go --migrate-only

docker:
	docker build \
		-f Dockerfile \
		--build-arg VERSION=$(VERSION) \
		-t $(IMAGE_NAME):$(VERSION) \
		-t $(IMAGE_NAME):latest \
		..

docker-agent:
	docker build \
		-f Dockerfile.agent \
		--build-arg VERSION=$(VERSION) \
		-t $(AGENT_IMAGE):$(VERSION) \
		-t $(AGENT_IMAGE):latest \
		..

clean:
	rm -rf $(BUILD_DIR)

.DEFAULT_GOAL := build
