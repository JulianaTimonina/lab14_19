// Package coordinator manages shard distribution across collector instances
// using etcd for coordination and leader election.
package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
)

const (
	// etcd key prefixes
	collectorsPrefix = "/energy/collectors/"
	shardsPrefix     = "/energy/shards/"
	assignPrefix     = "/energy/assignments/"
	sessionTTL       = 10 // seconds
)

// Shard represents a subset of meter IDs assigned to a collector.
type Shard struct {
	ShardID   int      `json:"shard_id"`
	MeterIDs  []string `json:"meter_ids"`
	Collector string   `json:"collector"`
}

// Coordinator manages etcd-based coordination.
type Coordinator struct {
	client     *clientv3.Client
	session    *concurrency.Session
	election   *concurrency.Election
	collectorID string
	mu         sync.Mutex
	myShards   []Shard
	leaseID    clientv3.LeaseID
}

// New creates a new Coordinator connected to the given etcd endpoints.
func New(endpoints []string, collectorID string) (*Coordinator, error) {
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to etcd: %w", err)
	}

	// Create a session with TTL
	session, err := concurrency.NewSession(cli, concurrency.WithTTL(sessionTTL))
	if err != nil {
		cli.Close()
		return nil, fmt.Errorf("failed to create etcd session: %w", err)
	}

	// Create an election for leader election
	election := concurrency.NewElection(session, "/energy/leader")

	return &Coordinator{
		client:      cli,
		session:     session,
		election:    election,
		collectorID: collectorID,
	}, nil
}

// Register registers this collector instance in etcd with a heartbeat.
func (c *Coordinator) Register(ctx context.Context) error {
	key := collectorsPrefix + c.collectorID
	lease, err := c.client.Grant(ctx, sessionTTL)
	if err != nil {
		return fmt.Errorf("failed to grant lease: %w", err)
	}
	c.leaseID = lease.ID

	_, err = c.client.Put(ctx, key, "alive", clientv3.WithLease(lease.ID))
	if err != nil {
		return fmt.Errorf("failed to register collector: %w", err)
	}

	// Keep the lease alive
	go func() {
		ch, kaErr := c.client.KeepAlive(context.Background(), lease.ID)
		if kaErr != nil {
			log.Printf("[%s] keepalive error: %v", c.collectorID, kaErr)
			return
		}
		for range ch {
			// keep alive
		}
	}()

	log.Printf("[%s] registered in etcd", c.collectorID)
	return nil
}

// RunForElection attempts to become the leader to perform shard rebalancing.
func (c *Coordinator) RunForElection(ctx context.Context) error {
	return c.election.Campaign(ctx, c.collectorID)
}

// IsLeader returns true if this instance is the current leader.
func (c *Coordinator) IsLeader(ctx context.Context) (bool, error) {
	resp, err := c.election.Leader(ctx)
	if err != nil {
		return false, nil // no leader yet
	}
	return string(resp.Kvs[0].Value) == c.collectorID, nil
}

// Resign resigns from leadership.
func (c *Coordinator) Resign(ctx context.Context) error {
	return c.election.Resign(ctx)
}

// GetActiveCollectors returns the list of currently active collector IDs.
func (c *Coordinator) GetActiveCollectors(ctx context.Context) ([]string, error) {
	resp, err := c.client.Get(ctx, collectorsPrefix, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}
	collectors := make([]string, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		id := strings.TrimPrefix(string(kv.Key), collectorsPrefix)
		collectors = append(collectors, id)
	}
	sort.Strings(collectors)
	return collectors, nil
}

// InitShards creates the initial shard definitions in etcd.
func (c *Coordinator) InitShards(ctx context.Context, allMeterIDs []string, numShards int) error {
	// Check if shards already exist
	resp, err := c.client.Get(ctx, shardsPrefix, clientv3.WithPrefix())
	if err != nil {
		return err
	}
	if len(resp.Kvs) > 0 {
		log.Printf("[%s] shards already exist, skipping initialization", c.collectorID)
		return nil
	}

	// Distribute meter IDs across shards
	shardSize := (len(allMeterIDs) + numShards - 1) / numShards
	for i := 0; i < numShards; i++ {
		start := i * shardSize
		end := start + shardSize
		if end > len(allMeterIDs) {
			end = len(allMeterIDs)
		}
		if start >= len(allMeterIDs) {
			break
		}

		shard := Shard{
			ShardID:  i + 1,
			MeterIDs: allMeterIDs[start:end],
		}
		data, _ := json.Marshal(shard)
		key := fmt.Sprintf("%s%d", shardsPrefix, shard.ShardID)
		_, err := c.client.Put(ctx, key, string(data))
		if err != nil {
			return fmt.Errorf("failed to create shard %d: %w", shard.ShardID, err)
		}
		log.Printf("[%s] created shard %d with %d meters", c.collectorID, shard.ShardID, len(shard.MeterIDs))
	}
	return nil
}

// RebalanceShards redistributes shards among active collectors.
// Only the leader should call this.
func (c *Coordinator) RebalanceShards(ctx context.Context) error {
	collectors, err := c.GetActiveCollectors(ctx)
	if err != nil {
		return err
	}
	if len(collectors) == 0 {
		return fmt.Errorf("no active collectors")
	}

	// Get all shards
	shardResp, err := c.client.Get(ctx, shardsPrefix, clientv3.WithPrefix())
	if err != nil {
		return err
	}
	if len(shardResp.Kvs) == 0 {
		log.Printf("[%s] no shards to rebalance", c.collectorID)
		return nil
	}

	// Parse shards
	type shardEntry struct {
		key   string
		shard Shard
	}
	var shards []shardEntry
	for _, kv := range shardResp.Kvs {
		var s Shard
		if err := json.Unmarshal(kv.Value, &s); err != nil {
			continue
		}
		shards = append(shards, shardEntry{key: string(kv.Key), shard: s})
	}

	// Distribute shards round-robin among collectors
	assignments := make(map[string][]Shard)
	for i, se := range shards {
		collector := collectors[i%len(collectors)]
		se.shard.Collector = collector
		assignments[collector] = append(assignments[collector], se.shard)

		// Update shard in etcd with collector assignment
		data, _ := json.Marshal(se.shard)
		_, err := c.client.Put(ctx, se.key, string(data))
		if err != nil {
			return fmt.Errorf("failed to update shard %d: %w", se.shard.ShardID, err)
		}

		// Write assignment key
		assignKey := fmt.Sprintf("%s%s/%d", assignPrefix, collector, se.shard.ShardID)
		_, err = c.client.Put(ctx, assignKey, string(data))
		if err != nil {
			return fmt.Errorf("failed to write assignment: %w", err)
		}
	}

	log.Printf("[%s] rebalanced %d shards across %d collectors", c.collectorID, len(shards), len(collectors))
	for _, col := range collectors {
		log.Printf("[%s] collector %s has %d shards", c.collectorID, col, len(assignments[col]))
	}
	return nil
}

// GetMyShards retrieves the shards assigned to this collector.
func (c *Coordinator) GetMyShards(ctx context.Context) ([]Shard, error) {
	assignKey := fmt.Sprintf("%s%s/", assignPrefix, c.collectorID)
	resp, err := c.client.Get(ctx, assignKey, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}

	shards := make([]Shard, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		var s Shard
		if err := json.Unmarshal(kv.Value, &s); err != nil {
			continue
		}
		shards = append(shards, s)
	}
	return shards, nil
}

// WatchAssignments watches for assignment changes and returns them on a channel.
func (c *Coordinator) WatchAssignments(ctx context.Context) <-chan []Shard {
	ch := make(chan []Shard)
	go func() {
		assignKey := fmt.Sprintf("%s%s/", assignPrefix, c.collectorID)
		rch := c.client.Watch(ctx, assignKey, clientv3.WithPrefix())
		for {
			select {
			case <-ctx.Done():
				return
			case wresp := <-rch:
				if wresp.Err() != nil {
					continue
				}
				shards, err := c.GetMyShards(ctx)
				if err == nil {
					ch <- shards
				}
			}
		}
	}()
	return ch
}

// WatchCollectors watches for collector changes and triggers rebalance.
func (c *Coordinator) WatchCollectors(ctx context.Context, rebalanceFn func()) {
	rch := c.client.Watch(ctx, collectorsPrefix, clientv3.WithPrefix())
	for {
		select {
		case <-ctx.Done():
			return
		case <-rch:
			// Small delay to let changes settle
			time.Sleep(500 * time.Millisecond)
			rebalanceFn()
		}
	}
}

// Close cleans up the etcd session and connection.
func (c *Coordinator) Close() error {
	ctx := context.Background()
	// Remove registration
	if c.leaseID != 0 {
		c.client.Revoke(ctx, c.leaseID)
	}
	c.session.Close()
	return c.client.Close()
}

// GetShardIDs returns the list of all shard IDs from etcd.
func (c *Coordinator) GetShardIDs(ctx context.Context) ([]int, error) {
	resp, err := c.client.Get(ctx, shardsPrefix, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		key := string(kv.Key)
		idStr := strings.TrimPrefix(key, shardsPrefix)
		var id int
		if _, err := fmt.Sscanf(idStr, "%d", &id); err == nil {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	return ids, nil
}

// GetAllMeterIDsFromShards reads all meter IDs from all shards in etcd.
func (c *Coordinator) GetAllMeterIDsFromShards(ctx context.Context) ([]string, error) {
	resp, err := c.client.Get(ctx, shardsPrefix, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}
	var all []string
	seen := make(map[string]bool)
	for _, kv := range resp.Kvs {
		var s Shard
		if err := json.Unmarshal(kv.Value, &s); err != nil {
			continue
		}
		for _, id := range s.MeterIDs {
			if !seen[id] {
				all = append(all, id)
				seen[id] = true
			}
		}
	}
	sort.Strings(all)
	return all, nil
}

// EnsureNoStaleAssignments removes assignments for collectors that are no longer active.
func (c *Coordinator) EnsureNoStaleAssignments(ctx context.Context) error {
	active, err := c.GetActiveCollectors(ctx)
	if err != nil {
		return err
	}
	activeSet := make(map[string]bool, len(active))
	for _, a := range active {
		activeSet[a] = true
	}

	resp, err := c.client.Get(ctx, assignPrefix, clientv3.WithPrefix())
	if err != nil {
		return err
	}
	for _, kv := range resp.Kvs {
		key := string(kv.Key)
		rel := strings.TrimPrefix(key, assignPrefix)
		parts := strings.SplitN(rel, "/", 2)
		if len(parts) >= 1 && !activeSet[parts[0]] {
			log.Printf("[%s] removing stale assignment: %s", c.collectorID, key)
			_, err := c.client.Delete(ctx, key)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// GetCollectorID returns this collector's ID.
func (c *Coordinator) GetCollectorID() string {
	return c.collectorID
}

// PathJoin is a helper to join etcd key paths.
func PathJoin(elem ...string) string {
	return path.Join(elem...)
}