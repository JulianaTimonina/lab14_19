// Package collector implements the data collection logic.
// Each collector instance reads data from its assigned shards and reports results.
package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/yliana-efimova/energy-collector/internal/aggregator"
	"github.com/yliana-efimova/energy-collector/internal/coordinator"
	"github.com/yliana-efimova/energy-collector/internal/kafkautil"
	"github.com/yliana-efimova/energy-collector/internal/source"
)

// OutputMode defines how collected readings are output.
type OutputMode int

const (
	// OutputLog outputs readings to the log (default).
	OutputLog OutputMode = iota
	// OutputKafka outputs readings to a Kafka topic.
	OutputKafka
)

// Config holds the collector configuration.
type Config struct {
	CollectorID     string
	EtcdEndpoints   []string
	NumMeters       int
	NumShards       int
	CollectInterval time.Duration

	// Aggregation config (optional). If nil, raw readings are emitted.
	AggregatorConfig *aggregator.Config

	// EnableValidation enables Rust-based data validation for readings.
	// When enabled, each reading is validated against the Rust validator library.
	// Invalid readings are logged as warnings but not discarded.
	EnableValidation bool

	// OutputMode selects how readings are output (log or Kafka).
	OutputMode OutputMode

	// Kafka config (used when OutputMode == OutputKafka).
	KafkaBrokers []string
	KafkaTopic   string
}

// Collector represents a data collector instance.
type Collector struct {
	cfg         Config
	coord       *coordinator.Coordinator
	source      *source.Source
	agg         *aggregator.Aggregator
	kafkaProd   *kafkautil.Producer
	mu          sync.Mutex
	collected   int
}

// New creates a new Collector instance.
func New(cfg Config) *Collector {
	c := &Collector{
		cfg:    cfg,
		source: source.New(cfg.NumMeters),
	}

	// Initialize aggregator if configured
	if cfg.AggregatorConfig != nil {
		agg, err := aggregator.New(*cfg.AggregatorConfig)
		if err != nil {
			log.Printf("[%s] failed to create aggregator, falling back to raw output: %v", cfg.CollectorID, err)
		} else {
			c.agg = agg
			log.Printf("[%s] tumbling window aggregator enabled: type=%s, window_size=%v, max_records=%d",
				cfg.CollectorID, agg.GetConfig().Type, agg.GetConfig().WindowSize, agg.GetConfig().MaxRecords)
		}
	}

	// Initialize Kafka producer if output mode is Kafka
	if cfg.OutputMode == OutputKafka {
		if len(cfg.KafkaBrokers) == 0 {
			log.Printf("[%s] WARNING: Kafka output mode selected but no brokers specified, falling back to log output", cfg.CollectorID)
		} else {
			topic := cfg.KafkaTopic
			if topic == "" {
				topic = "energy-readings"
			}
			c.kafkaProd = kafkautil.NewProducer(kafkautil.ProducerConfig{
				Brokers: cfg.KafkaBrokers,
				Topic:   topic,
			})
			log.Printf("[%s] Kafka producer enabled: brokers=%v, topic=%s", cfg.CollectorID, cfg.KafkaBrokers, topic)
		}
	}

	return c
}

// Start initializes the collector, connects to etcd, and begins collecting data.
func (c *Collector) Start(ctx context.Context) error {
	// Connect to etcd
	coord, err := coordinator.New(c.cfg.EtcdEndpoints, c.cfg.CollectorID)
	if err != nil {
		return fmt.Errorf("failed to create coordinator: %w", err)
	}
	c.coord = coord

	// Register in etcd
	if err := c.coord.Register(ctx); err != nil {
		return fmt.Errorf("failed to register: %w", err)
	}

	// Run for leader election
	go func() {
		if err := c.coord.RunForElection(ctx); err != nil {
			log.Printf("[%s] election error: %v", c.cfg.CollectorID, err)
		}
	}()

	// Leader loop: initialize shards and rebalance
	go c.leaderLoop(ctx)

	// Watch for collector changes to trigger rebalance
	go c.coord.WatchCollectors(ctx, func() {
		if isLeader, _ := c.coord.IsLeader(ctx); isLeader {
			log.Printf("[%s] collector change detected, rebalancing...", c.cfg.CollectorID)
			c.ensureStaleCleanup(ctx)
			if err := c.coord.RebalanceShards(ctx); err != nil {
				log.Printf("[%s] rebalance error: %v", c.cfg.CollectorID, err)
			}
		}
	})

	// Watch for assignment changes
	go func() {
		ch := c.coord.WatchAssignments(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case shards := <-ch:
				log.Printf("[%s] received %d shards in assignment update", c.cfg.CollectorID, len(shards))
				c.mu.Lock()
				c.collected = 0
				c.mu.Unlock()
			}
		}
	}()

	// Main collection loop
	go c.collectionLoop(ctx)

	log.Printf("[%s] collector started, waiting for assignments...", c.cfg.CollectorID)
	return nil
}

// leaderLoop periodically checks if this instance is the leader and initializes/rebalances shards.
func (c *Collector) leaderLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			isLeader, err := c.coord.IsLeader(ctx)
			if err != nil || !isLeader {
				continue
			}

			// Get all meter IDs from the source
			meters := c.source.GetMeters()
			meterIDs := make([]string, len(meters))
			for i, m := range meters {
				meterIDs[i] = m.ID
			}

			// Initialize shards if needed
			if err := c.coord.InitShards(ctx, meterIDs, c.cfg.NumShards); err != nil {
				log.Printf("[%s] init shards error: %v", c.cfg.CollectorID, err)
				continue
			}

			// Clean stale assignments
			c.ensureStaleCleanup(ctx)

			// Rebalance shards
			if err := c.coord.RebalanceShards(ctx); err != nil {
				log.Printf("[%s] rebalance error: %v", c.cfg.CollectorID, err)
				continue
			}
		}
	}
}

// ensureStaleCleanup removes assignments for dead collectors.
func (c *Collector) ensureStaleCleanup(ctx context.Context) {
	if err := c.coord.EnsureNoStaleAssignments(ctx); err != nil {
		log.Printf("[%s] stale cleanup error: %v", c.cfg.CollectorID, err)
	}
}

// collectionLoop periodically reads data from assigned shards.
func (c *Collector) collectionLoop(ctx context.Context) {
	ticker := time.NewTicker(c.cfg.CollectInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.collectOnce(ctx)
		}
	}
}

// collectOnce performs a single collection cycle.
func (c *Collector) collectOnce(ctx context.Context) {
	shards, err := c.coord.GetMyShards(ctx)
	if err != nil {
		log.Printf("[%s] failed to get shards: %v", c.cfg.CollectorID, err)
		return
	}

	if len(shards) == 0 {
		return
	}

	// Collect meter IDs from assigned shards
	meterIDs := make([]string, 0)
	for _, shard := range shards {
		meterIDs = append(meterIDs, shard.MeterIDs...)
	}

	// Read data from the source
	readings := c.source.ReadMeters(meterIDs)

	// Validate readings using Rust validator library (if enabled)
	if c.cfg.EnableValidation {
		for _, r := range readings {
			if errs := r.Validate(); len(errs) > 0 {
				log.Printf("[%s] VALIDATION WARNING: meter=%s errors=%v",
					c.cfg.CollectorID, r.MeterID, errs)
			}
		}
	}

	c.mu.Lock()
	c.collected += len(readings)
	c.mu.Unlock()

	if c.agg != nil {
		// Aggregation mode: feed readings into the tumbling window
		for _, r := range readings {
			aggregated := c.agg.Add(r)
			if aggregated != nil {
				// Window is complete — output aggregated results
				for _, agg := range aggregated {
					data, _ := json.Marshal(agg)
					log.Printf("[%s] AGGREGATED %s", c.cfg.CollectorID, string(data))
				}
				log.Printf("[%s] window completed: %d aggregated records (raw: %d, total raw: %d)",
					c.cfg.CollectorID, len(aggregated), len(readings), c.collected)
			}
		}
	} else if c.kafkaProd != nil {
		// Kafka output mode: send readings to Kafka topic
		if err := c.kafkaProd.SendReadings(ctx, readings); err != nil {
			log.Printf("[%s] failed to send readings to Kafka: %v", c.cfg.CollectorID, err)
		} else {
			log.Printf("[%s] sent %d readings to Kafka topic %s (total: %d)",
				c.cfg.CollectorID, len(readings), c.cfg.KafkaTopic, c.collected)
		}
	} else {
		// Raw mode: output each reading as JSON to log
		for _, r := range readings {
			data, _ := json.Marshal(r)
			log.Printf("[%s] DATA %s", c.cfg.CollectorID, string(data))
		}
		log.Printf("[%s] collected %d readings from %d shards (total: %d)",
			c.cfg.CollectorID, len(readings), len(shards), c.collected)
	}
}

// GetStats returns collection statistics.
func (c *Collector) GetStats() map[string]interface{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return map[string]interface{}{
		"collector_id": c.cfg.CollectorID,
		"collected":    c.collected,
	}
}

// Stop gracefully shuts down the collector.
func (c *Collector) Stop() error {
	// Flush any remaining aggregated data before shutdown
	if c.agg != nil {
		aggregated := c.agg.Flush()
		if len(aggregated) > 0 {
			for _, agg := range aggregated {
				data, _ := json.Marshal(agg)
				log.Printf("[%s] AGGREGATED (final flush) %s", c.cfg.CollectorID, string(data))
			}
			log.Printf("[%s] final flush: %d aggregated records", c.cfg.CollectorID, len(aggregated))
		}
	}

	// Close Kafka producer if used
	if c.kafkaProd != nil {
		if err := c.kafkaProd.Close(); err != nil {
			log.Printf("[%s] error closing Kafka producer: %v", c.cfg.CollectorID, err)
		}
		log.Printf("[%s] Kafka producer closed", c.cfg.CollectorID)
	}

	if c.coord != nil {
		// Resign if leader
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		c.coord.Resign(ctx)
		return c.coord.Close()
	}
	return nil
}