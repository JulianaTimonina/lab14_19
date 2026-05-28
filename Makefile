.PHONY: build clean run run-node1 run-node2 run-node3 test lint help

BINARY=collector
BUILD_DIR=build

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

build: ## Build the collector binary
	go build -o $(BUILD_DIR)/$(BINARY) ./cmd/collector

build-all: ## Build for multiple platforms
	GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY)-linux-amd64 ./cmd/collector
	GOOS=darwin GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY)-darwin-amd64 ./cmd/collector
	GOOS=windows GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY)-windows-amd64.exe ./cmd/collector

clean: ## Clean build artifacts
	rm -rf $(BUILD_DIR)

run: ## Run a single collector instance (requires etcd)
	go run ./cmd/collector -id=node1 -endpoints=localhost:2379 -meters=50 -shards=5 -interval=10s

run-node1: ## Run collector node 1
	go run ./cmd/collector -id=node1 -endpoints=localhost:2379 -meters=50 -shards=5 -interval=10s

run-node2: ## Run collector node 2
	go run ./cmd/collector -id=node2 -endpoints=localhost:2379 -meters=50 -shards=5 -interval=10s

run-node3: ## Run collector node 3
	go run ./cmd/collector -id=node3 -endpoints=localhost:2379 -meters=50 -shards=5 -interval=10s

run-all: ## Run 3 collector nodes simultaneously (requires etcd)
	$(MAKE) run-node1 &
	$(MAKE) run-node2 &
	$(MAKE) run-node3 &
	wait

test: ## Run tests
	go test ./... -v

lint: ## Run linter
	golangci-lint run ./...

deps: ## Download dependencies
	go mod tidy
	go mod download

docker-etcd: ## Start etcd in Docker
	docker run -d --name etcd \
		-p 2379:2379 -p 2380:2380 \
		-e ALLOW_NONE_AUTHENTICATION=yes \
		bitnami/etcd:latest

docker-stop: ## Stop etcd container
	docker stop etcd && docker rm etcd