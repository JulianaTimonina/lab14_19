package aggregator

import (
	"math"
	"testing"
	"time"

	"github.com/yliana-efimova/energy-collector/internal/source"
)

// makeReading creates a source.Reading with the given parameters.
func makeReading(meterID, location string, powerKW, voltageV, currentA float64) source.Reading {
	return source.Reading{
		MeterID:   meterID,
		Location:  location,
		Timestamp: time.Now(),
		PowerKW:   powerKW,
		VoltageV:  voltageV,
		CurrentA:  currentA,
	}
}

// TestNewAggregator_TimeBased verifies that a time-based aggregator is created successfully.
func TestNewAggregator_TimeBased(t *testing.T) {
	cfg := Config{
		Type:       WindowTime,
		WindowSize: 30 * time.Second,
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}
	if a == nil {
		t.Fatal("New() returned nil")
	}
	if a.GetConfig().Type != WindowTime {
		t.Errorf("expected WindowTime, got %v", a.GetConfig().Type)
	}
	if a.GetConfig().WindowSize != 30*time.Second {
		t.Errorf("expected WindowSize=30s, got %v", a.GetConfig().WindowSize)
	}
}

// TestNewAggregator_CountBased verifies that a count-based aggregator is created successfully.
func TestNewAggregator_CountBased(t *testing.T) {
	cfg := Config{
		Type:       WindowCount,
		MaxRecords: 10,
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}
	if a == nil {
		t.Fatal("New() returned nil")
	}
	if a.GetConfig().Type != WindowCount {
		t.Errorf("expected WindowCount, got %v", a.GetConfig().Type)
	}
	if a.GetConfig().MaxRecords != 10 {
		t.Errorf("expected MaxRecords=10, got %d", a.GetConfig().MaxRecords)
	}
}

// TestNewAggregator_InvalidTimeConfig verifies that a time-based aggregator with zero duration fails.
func TestNewAggregator_InvalidTimeConfig(t *testing.T) {
	cfg := Config{
		Type:       WindowTime,
		WindowSize: 0,
	}
	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for zero WindowSize, got nil")
	}
}

// TestNewAggregator_InvalidCountConfig verifies that a count-based aggregator with zero max records fails.
func TestNewAggregator_InvalidCountConfig(t *testing.T) {
	cfg := Config{
		Type:       WindowCount,
		MaxRecords: 0,
	}
	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for zero MaxRecords, got nil")
	}
}

// TestAdd_SingleReading verifies that adding a single reading returns nil (window not yet complete).
func TestAdd_SingleReading(t *testing.T) {
	cfg := Config{
		Type:       WindowCount,
		MaxRecords: 3,
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}

	r := makeReading("MTR-001", "Building A - Floor 1", 10.5, 220.0, 47.7)
	result := a.Add(r)
	if result != nil {
		t.Fatalf("expected nil result for single reading, got %d aggregated records", len(result))
	}
}

// TestAdd_CountWindowTrigger verifies that the count-based window triggers correctly.
func TestAdd_CountWindowTrigger(t *testing.T) {
	cfg := Config{
		Type:       WindowCount,
		MaxRecords: 3,
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}

	// Add 3 readings for the same meter — should trigger the window
	r1 := makeReading("MTR-001", "Building A - Floor 1", 10.0, 220.0, 45.5)
	r2 := makeReading("MTR-001", "Building A - Floor 1", 12.0, 221.0, 54.3)
	r3 := makeReading("MTR-001", "Building A - Floor 1", 11.0, 219.0, 50.2)

	a.Add(r1)
	a.Add(r2)
	result := a.Add(r3)

	if result == nil {
		t.Fatal("expected non-nil result after 3 readings, got nil")
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 aggregated record, got %d", len(result))
	}

	agg := result[0]
	if agg.MeterID != "MTR-001" {
		t.Errorf("expected MeterID=MTR-001, got %s", agg.MeterID)
	}
	if agg.Count != 3 {
		t.Errorf("expected Count=3, got %d", agg.Count)
	}
	if agg.SumPowerKW != 33.0 {
		t.Errorf("expected SumPowerKW=33.0, got %f", agg.SumPowerKW)
	}
	if agg.AvgPowerKW != 11.0 {
		t.Errorf("expected AvgPowerKW=11.0, got %f", agg.AvgPowerKW)
	}
	if agg.MinPowerKW != 10.0 {
		t.Errorf("expected MinPowerKW=10.0, got %f", agg.MinPowerKW)
	}
	if agg.MaxPowerKW != 12.0 {
		t.Errorf("expected MaxPowerKW=12.0, got %f", agg.MaxPowerKW)
	}
}

// TestAdd_CountWindowTrigger_MultipleMeters verifies count-based window with multiple meters.
// Each meter accumulates its own count; the window triggers when ANY meter reaches MaxRecords.
func TestAdd_CountWindowTrigger_MultipleMeters(t *testing.T) {
	cfg := Config{
		Type:       WindowCount,
		MaxRecords: 2,
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}

	// Add two readings for MTR-001 — should trigger the window
	r1 := makeReading("MTR-001", "Building A - Floor 1", 10.0, 220.0, 45.5)
	r2 := makeReading("MTR-001", "Building A - Floor 1", 12.0, 221.0, 54.3)

	a.Add(r1)
	result := a.Add(r2)

	if result == nil {
		t.Fatal("expected non-nil result after 2 readings for same meter, got nil")
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 aggregated record, got %d", len(result))
	}

	agg := result[0]
	if agg.MeterID != "MTR-001" {
		t.Errorf("expected MTR-001, got %s", agg.MeterID)
	}
	if agg.Count != 2 {
		t.Errorf("expected Count=2, got %d", agg.Count)
	}
}

// TestAdd_CountWindowTrigger_MultipleMeters_Simultaneous verifies that when multiple meters
// are in the same window, Flush returns all of them.
func TestAdd_CountWindowTrigger_MultipleMeters_Simultaneous(t *testing.T) {
	cfg := Config{
		Type:       WindowCount,
		MaxRecords: 3,
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}

	// Add readings for two different meters within the same window
	r1 := makeReading("MTR-001", "Building A - Floor 1", 10.0, 220.0, 45.5)
	r2 := makeReading("MTR-002", "Building B - Server Room", 50.0, 230.0, 217.4)

	a.Add(r1)
	a.Add(r2)

	// Flush should return both meters
	result := a.Flush()
	if result == nil {
		t.Fatal("expected non-nil result from Flush, got nil")
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 aggregated records, got %d", len(result))
	}

	meterIDs := make(map[string]bool)
	for _, agg := range result {
		meterIDs[agg.MeterID] = true
		if agg.Count != 1 {
			t.Errorf("meter %s: expected Count=1, got %d", agg.MeterID, agg.Count)
		}
	}
	if !meterIDs["MTR-001"] {
		t.Error("expected MTR-001 in results")
	}
	if !meterIDs["MTR-002"] {
		t.Error("expected MTR-002 in results")
	}
}

// TestAdd_TimeWindowTrigger verifies that the time-based window triggers after the specified duration.
func TestAdd_TimeWindowTrigger(t *testing.T) {
	cfg := Config{
		Type:       WindowTime,
		WindowSize: 50 * time.Millisecond,
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}

	// Add a reading
	r1 := makeReading("MTR-001", "Building A - Floor 1", 10.0, 220.0, 45.5)
	result := a.Add(r1)
	if result != nil {
		t.Fatal("expected nil before window expires")
	}

	// Wait for the window to expire
	time.Sleep(60 * time.Millisecond)

	// Add another reading — should trigger flush of the previous window
	r2 := makeReading("MTR-001", "Building A - Floor 1", 12.0, 221.0, 54.3)
	result = a.Add(r2)

	if result == nil {
		t.Fatal("expected non-nil result after window expiry, got nil")
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 aggregated record, got %d", len(result))
	}
	if result[0].Count != 1 {
		t.Errorf("expected Count=1 (only first reading in window), got %d", result[0].Count)
	}
}

// TestFlush_Empty verifies that flushing an empty aggregator returns nil.
func TestFlush_Empty(t *testing.T) {
	cfg := Config{
		Type:       WindowCount,
		MaxRecords: 10,
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}

	result := a.Flush()
	if result != nil {
		t.Fatal("expected nil when flushing empty aggregator")
	}
}

// TestFlush_WithData verifies that flushing returns all pending data.
func TestFlush_WithData(t *testing.T) {
	cfg := Config{
		Type:       WindowCount,
		MaxRecords: 10,
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}

	r1 := makeReading("MTR-001", "Building A - Floor 1", 10.0, 220.0, 45.5)
	r2 := makeReading("MTR-001", "Building A - Floor 1", 12.0, 221.0, 54.3)

	a.Add(r1)
	a.Add(r2)

	result := a.Flush()
	if result == nil {
		t.Fatal("expected non-nil result when flushing with data")
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 aggregated record, got %d", len(result))
	}
	if result[0].Count != 2 {
		t.Errorf("expected Count=2, got %d", result[0].Count)
	}
}

// TestAggregatedValues verifies the correctness of sum/avg/min/max calculations.
func TestAggregatedValues(t *testing.T) {
	cfg := Config{
		Type:       WindowCount,
		MaxRecords: 5,
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}

	// Add 4 readings — window not yet complete
	powers := []float64{10.0, 20.0, 30.0, 40.0}
	for i, p := range powers {
		r := makeReading("MTR-001", "Building A - Floor 1", p, 220.0+float64(i), 45.5+float64(i))
		a.Add(r)
	}

	// 5th reading should trigger the window (MaxRecords=5)
	r5 := makeReading("MTR-001", "Building A - Floor 1", 50.0, 224.0, 49.5)
	result := a.Add(r5)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 aggregated record, got %d", len(result))
	}

	agg := result[0]
	expectedSum := 150.0
	expectedAvg := 30.0
	expectedMin := 10.0
	expectedMax := 50.0

	if math.Abs(agg.SumPowerKW-expectedSum) > 0.001 {
		t.Errorf("SumPowerKW: expected %f, got %f", expectedSum, agg.SumPowerKW)
	}
	if math.Abs(agg.AvgPowerKW-expectedAvg) > 0.001 {
		t.Errorf("AvgPowerKW: expected %f, got %f", expectedAvg, agg.AvgPowerKW)
	}
	if math.Abs(agg.MinPowerKW-expectedMin) > 0.001 {
		t.Errorf("MinPowerKW: expected %f, got %f", expectedMin, agg.MinPowerKW)
	}
	if math.Abs(agg.MaxPowerKW-expectedMax) > 0.001 {
		t.Errorf("MaxPowerKW: expected %f, got %f", expectedMax, agg.MaxPowerKW)
	}
}

// TestAggregatedValues_Averages verifies average voltage and current calculations.
func TestAggregatedValues_Averages(t *testing.T) {
	cfg := Config{
		Type:       WindowCount,
		MaxRecords: 3,
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}

	r1 := makeReading("MTR-001", "Building A - Floor 1", 10.0, 220.0, 45.5)
	r2 := makeReading("MTR-001", "Building A - Floor 1", 12.0, 230.0, 52.2)
	r3 := makeReading("MTR-001", "Building A - Floor 1", 11.0, 210.0, 52.4)

	a.Add(r1)
	a.Add(r2)
	result := a.Add(r3)

	if result == nil || len(result) != 1 {
		t.Fatalf("expected 1 aggregated record, got %v", result)
	}

	agg := result[0]
	expectedAvgVoltage := (220.0 + 230.0 + 210.0) / 3.0
	expectedAvgCurrent := (45.5 + 52.2 + 52.4) / 3.0

	if math.Abs(agg.AvgVoltageV-expectedAvgVoltage) > 0.001 {
		t.Errorf("AvgVoltageV: expected %f, got %f", expectedAvgVoltage, agg.AvgVoltageV)
	}
	if math.Abs(agg.AvgCurrentA-expectedAvgCurrent) > 0.001 {
		t.Errorf("AvgCurrentA: expected %f, got %f", expectedAvgCurrent, agg.AvgCurrentA)
	}
}

// TestWindowType_String verifies the String() method of WindowType.
func TestWindowType_String(t *testing.T) {
	if WindowTime.String() != "time" {
		t.Errorf("expected 'time', got '%s'", WindowTime.String())
	}
	if WindowCount.String() != "count" {
		t.Errorf("expected 'count', got '%s'", WindowCount.String())
	}
	var unknown WindowType = 99
	if unknown.String() != "unknown" {
		t.Errorf("expected 'unknown', got '%s'", unknown.String())
	}
}

// TestConcurrentAdd verifies that the aggregator is safe for concurrent use.
func TestConcurrentAdd(t *testing.T) {
	cfg := Config{
		Type:       WindowCount,
		MaxRecords: 100,
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}

	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(n int) {
			for j := 0; j < 10; j++ {
				meterID := "MTR-" + string(rune('A'+n))
				r := makeReading(meterID, "Building", float64(j), 220.0, 50.0)
				a.Add(r)
			}
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	result := a.Flush()
	if result == nil {
		t.Fatal("expected non-nil result after concurrent adds")
	}
	if len(result) != 10 {
		t.Errorf("expected 10 aggregated records (one per meter), got %d", len(result))
	}
}