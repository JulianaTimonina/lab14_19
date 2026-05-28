.PHONY: build clean run run-node1 run-node2 run-node3 run-all run-agg-time run-agg-count run-agg-time-node1 run-agg-time-node2 run-agg-time-node3 run-agg-time-all test lint deps docker-etcd docker-stop \
	build-arrow run-arrow run-arrow-client run-arrow-bench \
	rust-build rust-clean rust-test build-with-rust build-all-with-rust \
	docker-build docker-kafka docker-kafka-stop \
	k8s-apply k8s-delete k8s-hpa k8s-status \
	run-kafka-collector run-kafka-analyzer run-python-collector run-python-bench \
	pip-install-all

BINARY=collector
BUILD_DIR=build

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

# --- Rust validation library ---

rust-build: ## Build the Rust validation library (release)
	cd rust_validator && cargo build --release

rust-clean: ## Clean Rust build artifacts
	cd rust_validator && cargo clean

rust-test: ## Run Rust validation library tests
	cd rust_validator && cargo test

# --- Go build targets ---

build: ## Build the collector binary (without Rust validation)
	go build -o $(BUILD_DIR)/$(BINARY) ./cmd/collector

build-with-rust: rust-build ## Build the collector binary with Rust validation support
	go build -o $(BUILD_DIR)/$(BINARY)-with-rust -ldflags="-r rust_validator/target/release" ./cmd/collector

build-all: ## Build for multiple platforms (without Rust validation)
	GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY)-linux-amd64 ./cmd/collector
	GOOS=darwin GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY)-darwin-amd64 ./cmd/collector
	GOOS=windows GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY)-windows-amd64.exe ./cmd/collector

build-all-with-rust: rust-build ## Build for multiple platforms with Rust validation support
	GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY)-linux-amd64-with-rust -ldflags="-r rust_validator/target/release" ./cmd/collector
	GOOS=darwin GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY)-darwin-amd64-with-rust -ldflags="-r rust_validator/target/release" ./cmd/collector
	GOOS=windows GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY)-windows-amd64-with-rust.exe -ldflags="-r rust_validator/target/release" ./cmd/collector

clean: ## Clean build artifacts
	rm -rf $(BUILD_DIR)

run: ## Run a single collector instance (raw mode, requires etcd)
	go run ./cmd/collector -id=node1 -endpoints=localhost:2379 -meters=50 -shards=5 -interval=10s

run-with-rust: rust-build ## Run a single collector instance with Rust validation
	go run -ldflags="-r rust_validator/target/release" ./cmd/collector -id=node1 -endpoints=localhost:2379 -meters=50 -shards=5 -interval=10s -validate

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

# --- Apache Arrow Flight RPC targets ---

build-arrow: ## Build the Arrow Flight server binary
	go build -o $(BUILD_DIR)/arrow-server ./cmd/arrowserver

run-arrow: ## Start the Arrow Flight server (port 50051, 50 meters)
	go run ./cmd/arrowserver -port=50051 -meters=50 -interval=10s

run-arrow-client: ## Run Python Arrow Flight client
	python python/arrow_client.py --server localhost:50051

run-arrow-bench: ## Run Python Arrow Flight client with benchmark
	python python/arrow_client.py --server localhost:50051 --benchmark --iterations=5

# --- Python dependencies ---

pip-install: ## Install Python dependencies for Arrow client
	pip install -r python/requirements.txt

pip-install-all: ## Install all Python dependencies (Arrow, Kafka, benchmark)
	pip install -r python/requirements.txt

# --- Testing & Linting ---

test: ## Run tests
	go test ./... -v

lint: ## Run linter
	golangci-lint run ./...

deps: ## Download dependencies
	go mod tidy
	go mod download

# --- Docker ---

docker-etcd: ## Start etcd in Docker
	docker run -d --name etcd \
		-p 2379:2379 -p 2380:2380 \
		-e ALLOW_NONE_AUTHENTICATION=yes \
		bitnami/etcd:latest

docker-stop: ## Stop etcd container
	docker stop etcd && docker rm etcd

docker-build: ## Build Docker image for collector and arrow-server
	docker build -t energy-collector:latest .

docker-kafka: ## Start Kafka and Zookeeper in Docker
	docker run -d --name zookeeper \
		-p 2181:2181 \
		-e ALLOW_ANONYMOUS_LOGIN=yes \
		bitnami/zookeeper:latest
	docker run -d --name kafka \
		-p 9092:9092 \
		-e KAFKA_CFG_ZOOKEEPER_CONNECT=zookeeper:2181 \
		-e ALLOW_PLAINTEXT_LISTENER=yes \
		-e KAFKA_CFG_LISTENER_SECURITY_PROTOCOL_MAP=INTERNAL:PLAINTEXT,EXTERNAL:PLAINTEXT \
		-e KAFKA_CFG_LISTENERS=INTERNAL://0.0.0.0:9092,EXTERNAL://0.0.0.0:9093 \
		-e KAFKA_CFG_ADVERTISED_LISTENERS=INTERNAL://localhost:9092,EXTERNAL://localhost:9093 \
		-e KAFKA_CFG_INTER_BROKER_LISTENER_NAME=INTERNAL \
		-e KAFKA_CFG_AUTO_CREATE_TOPICS_ENABLE=true \
		bitnami/kafka:latest

docker-kafka-stop: ## Stop Kafka and Zookeeper containers
	docker stop kafka && docker rm kafka
	docker stop zookeeper && docker rm zookeeper

# --- Kubernetes ---

k8s-apply: ## Apply all Kubernetes manifests
	kubectl apply -f k8s/namespace.yaml
	kubectl apply -f k8s/etcd-deployment.yaml
	kubectl apply -f k8s/kafka-deployment.yaml
	kubectl apply -f k8s/collector-deployment.yaml
	kubectl apply -f k8s/arrow-server-deployment.yaml
	kubectl apply -f k8s/hpa.yaml

k8s-delete: ## Delete all Kubernetes resources
	kubectl delete -f k8s/hpa.yaml --ignore-not-found
	kubectl delete -f k8s/arrow-server-deployment.yaml --ignore-not-found
	kubectl delete -f k8s/collector-deployment.yaml --ignore-not-found
	kubectl delete -f k8s/kafka-deployment.yaml --ignore-not-found
	kubectl delete -f k8s/etcd-deployment.yaml --ignore-not-found
	kubectl delete namespace energy-system --ignore-not-found

k8s-hpa: ## Check HPA status
	kubectl -n energy-system get hpa -w

k8s-status: ## Show status of all pods in energy-system
	kubectl -n energy-system get all

# --- Kafka collector (Go) ---

run-kafka-collector: ## Run Go collector with Kafka output (requires etcd + Kafka)
	go run ./cmd/collector -id=kafka-node1 -endpoints=localhost:2379 -meters=50 -shards=5 -interval=10s -kafka -kafka-brokers=localhost:9092 -kafka-topic=energy-readings

# --- Kafka analyzer (Python) ---

run-kafka-analyzer: ## Run Python Kafka analyzer with sliding window
	python python/kafka_analyzer.py --broker=localhost:9092 --topic=energy-readings --window=300

# --- Python collector ---

run-python-collector: ## Run Python async collector (benchmark mode)
	python python/async_collector.py --meters=50 --interval=5 --benchmark --benchmark-cycles=10

run-python-bench: ## Run Go vs Python benchmark comparison
	python python/benchmark_compare.py --meters=50 --cycles=10 --output=./benchmark_results