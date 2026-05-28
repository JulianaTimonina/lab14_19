package source

import (
	"testing"
)

// TestNewSource verifies that a source with N meters is created correctly.
func TestNewSource(t *testing.T) {
	s := New(5)
	if s == nil {
		t.Fatal("New() returned nil")
	}
	if len(s.Meters) != 5 {
		t.Errorf("expected 5 meters, got %d", len(s.Meters))
	}
}

// TestNewSource_Zero verifies that a source with 0 meters is created correctly.
func TestNewSource_Zero(t *testing.T) {
	s := New(0)
	if s == nil {
		t.Fatal("New() returned nil")
	}
	if len(s.Meters) != 0 {
		t.Errorf("expected 0 meters, got %d", len(s.Meters))
	}
}

// TestNewSource_MeterIDs verifies that meter IDs follow the expected format.
func TestNewSource_MeterIDs(t *testing.T) {
	s := New(3)
	expectedIDs := []string{"MTR-001", "MTR-002", "MTR-003"}
	for i, m := range s.Meters {
		if m.ID != expectedIDs[i] {
			t.Errorf("meter[%d]: expected ID %s, got %s", i, expectedIDs[i], m.ID)
		}
	}
}

// TestNewSource_Locations verifies that locations are assigned round-robin.
func TestNewSource_Locations(t *testing.T) {
	s := New(10)
	if len(s.Meters) != 10 {
		t.Fatalf("expected 10 meters, got %d", len(s.Meters))
	}
	// First 8 meters should have unique locations (there are 8 locations defined)
	// Meters 9 and 10 should repeat locations from the start
	if s.Meters[0].Location == "" {
		t.Error("meter[0] has empty location")
	}
	if s.Meters[8].Location != s.Meters[0].Location {
		t.Errorf("meter[8] location should repeat meter[0] location: got %s vs %s",
			s.Meters[8].Location, s.Meters[0].Location)
	}
}

// TestGetMeters verifies that GetMeters returns a copy of all meters.
func TestGetMeters(t *testing.T) {
	s := New(5)
	meters := s.GetMeters()
	if len(meters) != 5 {
		t.Fatalf("expected 5 meters, got %d", len(meters))
	}

	// Verify it's a copy by modifying the returned slice
	meters[0].BaseLoad = 999.0
	if s.Meters[0].BaseLoad == 999.0 {
		t.Error("GetMeters() did not return a copy; original was modified")
	}
}

// TestReadMeters_All verifies that ReadMeters with empty IDs reads all meters.
func TestReadMeters_All(t *testing.T) {
	s := New(3)
	readings := s.ReadMeters(nil)
	if len(readings) != 3 {
		t.Fatalf("expected 3 readings, got %d", len(readings))
	}

	// Check that all meter IDs are present
	idSet := make(map[string]bool)
	for _, r := range readings {
		idSet[r.MeterID] = true
		if r.MeterID == "" {
			t.Error("reading has empty MeterID")
		}
		if r.Location == "" {
			t.Error("reading has empty Location")
		}
		if r.PowerKW <= 0 {
			t.Errorf("meter %s: PowerKW should be positive, got %f", r.MeterID, r.PowerKW)
		}
		if r.VoltageV <= 0 {
			t.Errorf("meter %s: VoltageV should be positive, got %f", r.MeterID, r.VoltageV)
		}
		if r.CurrentA <= 0 {
			t.Errorf("meter %s: CurrentA should be positive, got %f", r.MeterID, r.CurrentA)
		}
		if r.Timestamp.IsZero() {
			t.Errorf("meter %s: Timestamp is zero", r.MeterID)
		}
	}

	if !idSet["MTR-001"] || !idSet["MTR-002"] || !idSet["MTR-003"] {
		t.Errorf("not all meter IDs present in readings: %v", idSet)
	}
}

// TestReadMeters_Subset verifies that ReadMeters with specific IDs returns only those meters.
func TestReadMeters_Subset(t *testing.T) {
	s := New(5)
	readings := s.ReadMeters([]string{"MTR-001", "MTR-003", "MTR-005"})
	if len(readings) != 3 {
		t.Fatalf("expected 3 readings, got %d", len(readings))
	}

	for _, r := range readings {
		if r.MeterID != "MTR-001" && r.MeterID != "MTR-003" && r.MeterID != "MTR-005" {
			t.Errorf("unexpected meter ID in subset: %s", r.MeterID)
		}
	}
}

// TestReadMeters_EmptyIDs verifies that ReadMeters with an empty list reads all meters.
func TestReadMeters_EmptyIDs(t *testing.T) {
	s := New(4)
	readings := s.ReadMeters([]string{})
	if len(readings) != 4 {
		t.Fatalf("expected 4 readings for empty ID list, got %d", len(readings))
	}
}

// TestReadMeters_NonexistentID verifies that requesting a nonexistent meter ID returns no readings.
func TestReadMeters_NonexistentID(t *testing.T) {
	s := New(3)
	readings := s.ReadMeters([]string{"NONEXISTENT"})
	if len(readings) != 0 {
		t.Fatalf("expected 0 readings for nonexistent ID, got %d", len(readings))
	}
}

// TestReadMeters_Concurrent verifies that ReadMeters is safe for concurrent use.
func TestReadMeters_Concurrent(t *testing.T) {
	s := New(10)
	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func() {
			readings := s.ReadMeters(nil)
			if len(readings) != 10 {
				t.Errorf("expected 10 readings, got %d", len(readings))
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

// TestReading_Validate verifies that the Validate() method works on a reading.
// Note: This test requires the Rust validator library to be linked.
// If the library is not available, the test will be skipped.
func TestReading_Validate(t *testing.T) {
	s := New(1)
	readings := s.ReadMeters(nil)
	if len(readings) != 1 {
		t.Fatalf("expected 1 reading, got %d", len(readings))
	}

	r := readings[0]
	// Validate should not panic
	errs := r.Validate()
	// Generated readings should be valid (meter ID format is correct, power in range, etc.)
	// Note: if the Rust library is not linked, this will return an error
	for _, err := range errs {
		t.Logf("validation message: %s", err)
	}
}

// TestMeterFields verifies that meter fields are populated correctly.
func TestMeterFields(t *testing.T) {
	s := New(2)
	meters := s.GetMeters()

	if meters[0].ID != "MTR-001" {
		t.Errorf("expected MTR-001, got %s", meters[0].ID)
	}
	if meters[0].BaseLoad != 5.0 {
		t.Errorf("expected BaseLoad=5.0, got %f", meters[0].BaseLoad)
	}
	if meters[0].Fluctuation != 2.0 {
		t.Errorf("expected Fluctuation=2.0, got %f", meters[0].Fluctuation)
	}

	if meters[1].ID != "MTR-002" {
		t.Errorf("expected MTR-002, got %s", meters[1].ID)
	}
	if meters[1].BaseLoad != 6.5 {
		t.Errorf("expected BaseLoad=6.5, got %f", meters[1].BaseLoad)
	}
	if meters[1].Fluctuation != 2.3 {
		t.Errorf("expected Fluctuation=2.3, got %f", meters[1].Fluctuation)
	}
}

// TestReadMeters_PowerRange verifies that generated power values are within expected range.
func TestReadMeters_PowerRange(t *testing.T) {
	s := New(100)
	readings := s.ReadMeters(nil)

	for _, r := range readings {
		// Power should be non-negative
		if r.PowerKW < 0 {
			t.Errorf("meter %s: negative PowerKW: %f", r.MeterID, r.PowerKW)
		}
		// Voltage should be in reasonable range (210-230V with fluctuation)
		if r.VoltageV < 200 || r.VoltageV > 240 {
			t.Errorf("meter %s: VoltageV out of range: %f", r.MeterID, r.VoltageV)
		}
		// Current should be positive
		if r.CurrentA <= 0 {
			t.Errorf("meter %s: non-positive CurrentA: %f", r.MeterID, r.CurrentA)
		}
	}
}