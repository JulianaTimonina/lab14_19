.PHONY: build clean run run-node1 run-node2 run-node3 run-all run-agg-time run-agg-count test lint help

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

run: ## Run a single collector instance (raw mode, requires etcd)
	go run ./cmd/collector -id=node1 -endpoints=localhost:2379 -meters=50 -shards=5 -interval=10s

run-node1: ## Run collector node 1 (raw mode)
	go run ./cmd/collector -id=node1 -endpoints=localhost:2379 -meters=50 -shards=5 -interval=10s

run-node2: ## Run collector node 2 (raw mode)
	go run ./cmd/collector -id=node2 -endpoints=localhost:2379 -meters=50 -shards=5 -interval=10s

run-node3: ## Run collector node 3 (raw mode)
	go run ./cmd/collector -id=node3 -endpoints=localhost:2379 -meters=50 -shards=5 -interval=10s

run-all: ## Run 3 collector nodes simultaneously (raw mode, requires etcd)
	$(MAKE) run-node1 &
	$(MAKE) run-node2 &
	$(MAKE) run-node3 &
	wait

# --- Tumbling window aggregation targets ---

run-agg-time: ## Run with time-based tumbling window (30s)
	go run ./cmd/collector -id=node1 -endpoints=localhost:2379 -meters=50 -shards=5 -interval=10s -agg-window=30s

run-agg-count: ## Run with count-based tumbling window (100 records)
	go run ./cmd/collector -id=node1 -endpoints=localhost:2379 -meters=50 -shards=5 -interval=10s -agg-count=100

run-agg-time-node1: ## Run node 1 with time-based tumbling window (30s)
	go run ./cmd/collector -id=node1 -endpoints=localhost:2379 -meters=50 -shards=5 -interval=10s -agg-window=30s

run-agg-time-node2: ## Run node 2 with time-based tumbling window (30s)
	go run ./cmd/collector -id=node2 -endpoints=localhost:2379 -meters=50 -shards=5 -interval=10s -agg-window=30s

run-agg-time-node3: ## Run node 3 with time-based tumbling window (30s)
	go run ./cmd/collector -id=node3 -endpoints=localhost:2379 -meters=50 -shards=5 -interval=10s -agg-window=30s

run-agg-time-all: ## Run 3 nodes with time-based tumbling window (30s)
	$(MAKE) run-agg-time-node1 &
	$(MAKE) run-agg-time-node2 &
	$(MAKE) run-agg-time-node3 &
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