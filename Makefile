BINARY    := pocket-settlement-monitor
MODULE    := github.com/pokt-network/pocket-settlement-monitor
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT    ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_DATE?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS   := -X $(MODULE)/internal/version.Version=$(VERSION) \
             -X $(MODULE)/internal/version.Commit=$(COMMIT) \
             -X $(MODULE)/internal/version.BuildDate=$(BUILD_DATE)

GO        := go
GOFLAGS   := -trimpath
GOTEST    := $(GO) test

CONFIG    ?= config.example.yaml

.PHONY: all build build-release run test test-coverage fmt fmt-check vet lint tidy clean docker mock-webhook test-beta test-mainnet test-localnet tilt tilt-down help

all: fmt lint test build

build: ## Development build
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY) .

run: build ## Build and run monitor (CONFIG=config.beta.yaml)
	./bin/$(BINARY) monitor --config $(CONFIG)

build-release: ## Production build (CGO_ENABLED=0, stripped)
	CGO_ENABLED=0 $(GO) build $(GOFLAGS) -ldflags "-s -w $(LDFLAGS)" -o bin/$(BINARY) .

test: ## Run all tests with -race
	$(GOTEST) -race -count=1 ./...

test-coverage: ## Run tests with coverage report
	$(GOTEST) -race -coverprofile=coverage.out -covermode=atomic ./...
	$(GO) tool cover -func=coverage.out

fmt: ## Format all Go files
	gofmt -s -w .
	goimports -w -local $(MODULE) .

fmt-check: ## Check formatting without modifying files
	@test -z "$$(gofmt -l .)" || (echo "Files not formatted:"; gofmt -l .; exit 1)

vet: ## Run go vet
	$(GO) vet ./...

lint: ## Run golangci-lint
	golangci-lint run ./...

tidy: ## Run go mod tidy
	$(GO) mod tidy

clean: ## Remove build artifacts
	rm -rf bin/ dist/ coverage.out

docker: ## Build Docker image
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $(BINARY):$(VERSION) .

mock-webhook: ## Start mock Discord webhook server for testing
	@trap '' INT; $(GO) run scripts/mock-webhook.go; true

test-beta: build ## Run integration tests against beta testnet
	./scripts/test-beta.sh

test-mainnet: build ## Run integration tests against mainnet
	./scripts/test-mainnet.sh

test-localnet: build ## Run integration tests against localnet
	./scripts/test-localnet.sh

tilt: ## Start Tilt development environment
	@if [ ! -f tilt_config.yaml ]; then \
		echo "tilt_config.yaml not found. Creating from example..."; \
		cp tilt_config.example.yaml tilt_config.yaml; \
		echo "Edit tilt_config.yaml with your settings, then re-run 'make tilt'."; \
	else \
		tilt up; \
	fi

tilt-down: ## Stop Tilt development environment and remove volumes
	tilt down --delete-volumes

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'
