// Command arrowserver starts an Apache Arrow Flight RPC server
// that serves electricity meter readings in Arrow columnar format.
//
// Usage:
//   arrowserver -port=50051 -meters=50 -interval=10s
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yliana-efimova/energy-collector/internal/arrowserver"
)

func main() {
	var (
		port     = flag.Int("port", 50051, "gRPC port for Arrow Flight RPC")
		numMeters = flag.Int("meters", 50, "Number of simulated electricity meters")
		interval  = flag.Duration("interval", 10*time.Second, "Data generation interval")
	)
	flag.Parse()

	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.Printf("Starting Arrow Flight server: port=%d, meters=%d, interval=%s",
		*port, *numMeters, *interval)

	cfg := arrowserver.ServerConfig{
		Port:      *port,
		NumMeters: *numMeters,
		Interval:  *interval,
	}

	server := arrowserver.New(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start server in a goroutine
	go func() {
		if err := server.Start(ctx); err != nil {
			log.Fatalf("Arrow Flight server error: %v", err)
		}
	}()

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("Received signal %v, shutting down...", sig)
	cancel()

	// Give server time to shut down gracefully
	time.Sleep(500 * time.Millisecond)
	log.Println("Arrow Flight server stopped")
}