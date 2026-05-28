// Command collector is the entry point for the energy data collector.
// It starts a collector instance that connects to etcd for coordination
// and collects simulated electricity meter data.
//
// Usage:
//   collector -id=node1 -endpoints=localhost:2379 -meters=50 -shards=5 -interval=10s
//
// With tumbling window aggregation:
//   collector -id=node1 -endpoints=localhost:2379 -meters=50 -shards=5 -interval=10s \
//     -agg-window=30s
//   collector -id=node1 -endpoints=localhost:2379 -meters=50 -shards=5 -interval=10s \
//     -agg-count=100
//
// With Kafka output:
//   collector -id=node1 -endpoints=localhost:2379 -meters=50 -shards=5 -interval=10s \
//     -kafka -kafka-brokers=localhost:9092 -kafka-topic=energy-readings
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/yliana-efimova/energy-collector/internal/aggregator"
	"github.com/yliana-efimova/energy-collector/internal/collector"
)

func main() {
	var (
		collectorID = flag.String("id", "", "Unique collector instance ID (required)")
		endpoints   = flag.String("endpoints", "localhost:2379", "Comma-separated etcd endpoints")
		numMeters   = flag.Int("meters", 50, "Number of simulated electricity meters")
		numShards   = flag.Int("shards", 5, "Number of shards for distribution")
		interval    = flag.Duration("interval", 10*time.Second, "Collection interval")

		// Tumbling window aggregation flags
		aggWindow = flag.Duration("agg-window", 0, "Tumbling window duration (e.g. 30s). Enables time-based aggregation.")
		aggCount  = flag.Int("agg-count", 0, "Tumbling window record count (e.g. 100). Enables count-based aggregation.")

		// Rust validation flag
		enableValidation = flag.Bool("validate", false, "Enable Rust-based data validation for readings")

		// Kafka output flags
		kafkaMode   = flag.Bool("kafka", false, "Enable Kafka output mode")
		kafkaBrokers = flag.String("kafka-brokers", "localhost:9092", "Comma-separated Kafka broker addresses")
		kafkaTopic   = flag.String("kafka-topic", "energy-readings", "Kafka topic name")
	)
	flag.Parse()

	if *collectorID == "" {
		hostname, _ := os.Hostname()
		*collectorID = fmt.Sprintf("collector-%s-%d", hostname, time.Now().UnixNano()%10000)
	}

	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.Printf("Starting energy collector: id=%s, endpoints=%s, meters=%d, shards=%d, interval=%s",
		*collectorID, *endpoints, *numMeters, *numShards, *interval)

	// Parse endpoints
	ep := parseEndpoints(*endpoints)

	// Build optional aggregator config
	var aggCfg *aggregator.Config
	if *aggWindow > 0 {
		aggCfg = &aggregator.Config{
			Type:       aggregator.WindowTime,
			WindowSize: *aggWindow,
		}
		log.Printf("Time-based tumbling window enabled: window=%s", *aggWindow)
	} else if *aggCount > 0 {
		aggCfg = &aggregator.Config{
			Type:        aggregator.WindowCount,
			MaxRecords:  *aggCount,
		}
		log.Printf("Count-based tumbling window enabled: max_records=%d", *aggCount)
	}

	// Determine output mode
	outputMode := collector.OutputLog
	var kafkaBrokersList []string
	if *kafkaMode {
		outputMode = collector.OutputKafka
		kafkaBrokersList = strings.Split(*kafkaBrokers, ",")
		for i := range kafkaBrokersList {
			kafkaBrokersList[i] = strings.TrimSpace(kafkaBrokersList[i])
		}
		log.Printf("Kafka output mode enabled: brokers=%v, topic=%s", kafkaBrokersList, *kafkaTopic)
	}

	// Create collector config
	cfg := collector.Config{
		CollectorID:      *collectorID,
		EtcdEndpoints:    ep,
		NumMeters:        *numMeters,
		NumShards:        *numShards,
		CollectInterval:  *interval,
		AggregatorConfig: aggCfg,
		EnableValidation: *enableValidation,
		OutputMode:       outputMode,
		KafkaBrokers:     kafkaBrokersList,
		KafkaTopic:       *kafkaTopic,
	}

	if *enableValidation {
		log.Printf("Rust-based data validation enabled")
	}

	// Create and start collector
	c := collector.New(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := c.Start(ctx); err != nil {
		log.Fatalf("Failed to start collector: %v", err)
	}

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("Received signal %v, shutting down...", sig)

	// Graceful shutdown
	c.Stop()
	log.Println("Collector stopped")
}

// parseEndpoints splits a comma-separated endpoint string into a slice.
func parseEndpoints(s string) []string {
	if s == "" {
		return []string{"localhost:2379"}
	}
	eps := make([]string, 0)
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			if i > start {
				eps = append(eps, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		eps = append(eps, s[start:])
	}
	return eps
}