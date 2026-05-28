// Package arrowserver implements an Apache Arrow Flight RPC server
// that serves electricity meter readings in Arrow columnar format.
package arrowserver

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/flight"
	"github.com/apache/arrow-go/v18/arrow/ipc"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/yliana-efimova/energy-collector/internal/source"
)

// ServerConfig holds the Arrow Flight server configuration.
type ServerConfig struct {
	Port      int           // gRPC port for Flight RPC
	NumMeters int           // Number of simulated meters
	Interval  time.Duration // Data generation interval
}

// Server is an Apache Arrow Flight RPC server that serves meter readings.
type Server struct {
	cfg    ServerConfig
	source *source.Source
	flight.BaseFlightServer
	server flight.Server
}

// New creates a new Arrow Flight server.
func New(cfg ServerConfig) *Server {
	return &Server{
		cfg:    cfg,
		source: source.New(cfg.NumMeters),
	}
}

// getSchema returns the Arrow schema for meter readings.
func (s *Server) getSchema() *arrow.Schema {
	return arrow.NewSchema(
		[]arrow.Field{
			{Name: "meter_id", Type: arrow.BinaryTypes.String},
			{Name: "location", Type: arrow.BinaryTypes.String},
			{Name: "timestamp", Type: arrow.FixedWidthTypes.Timestamp_us},
			{Name: "power_kw", Type: arrow.PrimitiveTypes.Float64},
			{Name: "voltage_v", Type: arrow.PrimitiveTypes.Float64},
			{Name: "current_a", Type: arrow.PrimitiveTypes.Float64},
		},
		nil,
	)
}

// GetFlightInfo describes the available data streams.
func (s *Server) GetFlightInfo(ctx context.Context, desc *flight.FlightDescriptor) (*flight.FlightInfo, error) {
	schema := s.getSchema()
	addr := s.server.Addr().String()

	endpoint := &flight.FlightEndpoint{
		Ticket: &flight.Ticket{Ticket: []byte("all_meters")},
		Location: []*flight.Location{
			{Uri: fmt.Sprintf("grpc://%s", addr)},
		},
	}

	return &flight.FlightInfo{
		Schema:           flight.SerializeSchema(schema, memory.DefaultAllocator),
		FlightDescriptor: desc,
		Endpoint:         []*flight.FlightEndpoint{endpoint},
		TotalRecords:     -1,
		TotalBytes:       -1,
	}, nil
}

// GetSchema returns the schema for the given flight descriptor.
func (s *Server) GetSchema(ctx context.Context, in *flight.FlightDescriptor) (*flight.SchemaResult, error) {
	schema := s.getSchema()
	return &flight.SchemaResult{Schema: flight.SerializeSchema(schema, memory.DefaultAllocator)}, nil
}

// DoGet serves the Arrow RecordBatch data stream to the client.
func (s *Server) DoGet(request *flight.Ticket, stream flight.FlightService_DoGetServer) error {
	// Read all meters
	meters := s.source.GetMeters()
	meterIDs := make([]string, len(meters))
	for i, m := range meters {
		meterIDs[i] = m.ID
	}

	readings := s.source.ReadMeters(meterIDs)

	// Build Arrow Record from readings
	rec, err := s.readingsToRecord(readings)
	if err != nil {
		return fmt.Errorf("failed to create record: %w", err)
	}
	defer rec.Release()

	// Use RecordWriter to send the record
	w := flight.NewRecordWriter(stream, ipc.WithSchema(rec.Schema()))
	if err := w.Write(rec); err != nil {
		return fmt.Errorf("failed to write record: %w", err)
	}

	log.Printf("[arrow-server] sent %d readings (%d rows) via Arrow Flight",
		len(readings), rec.NumRows())

	return nil
}

// readingsToRecord converts a slice of Readings to an Arrow Record.
func (s *Server) readingsToRecord(readings []source.Reading) (arrow.Record, error) {
	pool := memory.NewGoAllocator()
	schema := s.getSchema()
	n := len(readings)

	// Build columns
	meterIDs := make([]string, n)
	locations := make([]string, n)
	timestamps := make([]arrow.Timestamp, n)
	powerKW := make([]float64, n)
	voltageV := make([]float64, n)
	currentA := make([]float64, n)

	for i, r := range readings {
		meterIDs[i] = r.MeterID
		locations[i] = r.Location
		timestamps[i] = arrow.Timestamp(r.Timestamp.UnixMicro())
		powerKW[i] = r.PowerKW
		voltageV[i] = r.VoltageV
		currentA[i] = r.CurrentA
	}

	// Build arrays
	columns := make([]arrow.Array, 6)

	// meter_id (string)
	builder := array.NewStringBuilder(pool)
	builder.AppendValues(meterIDs, nil)
	columns[0] = builder.NewArray()
	builder.Release()

	// location (string)
	builder2 := array.NewStringBuilder(pool)
	builder2.AppendValues(locations, nil)
	columns[1] = builder2.NewArray()
	builder2.Release()

	// timestamp (timestamp_us)
	builder3 := array.NewTimestampBuilder(pool, arrow.FixedWidthTypes.Timestamp_us.(*arrow.TimestampType))
	builder3.AppendValues(timestamps, nil)
	columns[2] = builder3.NewArray()
	builder3.Release()

	// power_kw (float64)
	builder4 := array.NewFloat64Builder(pool)
	builder4.AppendValues(powerKW, nil)
	columns[3] = builder4.NewArray()
	builder4.Release()

	// voltage_v (float64)
	builder5 := array.NewFloat64Builder(pool)
	builder5.AppendValues(voltageV, nil)
	columns[4] = builder5.NewArray()
	builder5.Release()

	// current_a (float64)
	builder6 := array.NewFloat64Builder(pool)
	builder6.AppendValues(currentA, nil)
	columns[5] = builder6.NewArray()
	builder6.Release()

	// Create record
	rec := array.NewRecord(schema, columns, int64(n))

	// Release column arrays
	for _, col := range columns {
		col.Release()
	}

	return rec, nil
}

// Start launches the Arrow Flight gRPC server.
func (s *Server) Start(ctx context.Context) error {
	addr := fmt.Sprintf(":%d", s.cfg.Port)

	// Create Flight server
	flightServer := flight.NewFlightServer()
	flightServer.Init(addr)
	s.server = flightServer

	// Register our custom Flight service
	flightServer.RegisterFlightService(s)

	log.Printf("[arrow-server] listening on %s with %d simulated meters",
		addr, s.cfg.NumMeters)

	// Handle graceful shutdown
	go func() {
		<-ctx.Done()
		log.Println("[arrow-server] shutting down...")
		flightServer.Shutdown()
	}()

	return flightServer.Serve()
}