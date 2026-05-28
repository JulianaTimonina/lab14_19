// Package source provides an emulation of electricity meters.
// Each meter has a unique ID and reports simulated power consumption.
package source

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/yliana-efimova/energy-collector/internal/validator"
)

// Meter represents a single electricity meter.
type Meter struct {
	ID        string
	Location  string
	BaseLoad  float64 // base load in kW
	Fluctuation float64 // max random fluctuation
}

// Reading represents a single meter reading.
type Reading struct {
	MeterID    string    `json:"meter_id"`
	Location   string    `json:"location"`
	Timestamp  time.Time `json:"timestamp"`
	PowerKW    float64   `json:"power_kw"`
	VoltageV   float64   `json:"voltage_v"`
	CurrentA   float64   `json:"current_a"`
}

// Validate checks the reading against Rust validation library.
// Returns nil if valid, or a slice of error messages.
func (r *Reading) Validate() []string {
	_, errs := validator.ValidateReading(validator.ReadingInput{
		MeterID:   r.MeterID,
		Location:  r.Location,
		Timestamp: r.Timestamp.UnixMicro(),
		PowerKW:   r.PowerKW,
		VoltageV:  r.VoltageV,
		CurrentA:  r.CurrentA,
	})
	return errs
}

// Source manages a collection of simulated meters.
type Source struct {
	mu     sync.RWMutex
	Meters []Meter
	rng    *rand.Rand
}

// New creates a new Source with the given number of simulated meters.
func New(count int) *Source {
	s := &Source{
		Meters: make([]Meter, 0, count),
		rng:    rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	locations := []string{
		"Building A - Floor 1",
		"Building A - Floor 2",
		"Building B - Server Room",
		"Building B - Lobby",
		"Building C - Workshop",
		"Building C - Office",
		"Building D - Warehouse",
		"Building D - Charging Station",
	}
	for i := 0; i < count; i++ {
		loc := locations[i%len(locations)]
		s.Meters = append(s.Meters, Meter{
			ID:          fmt.Sprintf("MTR-%03d", i+1),
			Location:    loc,
			BaseLoad:    5.0 + float64(i)*1.5,
			Fluctuation: 2.0 + float64(i)*0.3,
		})
	}
	return s
}

// GetMeters returns a copy of all meters.
func (s *Source) GetMeters() []Meter {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Meter, len(s.Meters))
	copy(out, s.Meters)
	return out
}

// ReadMeters returns readings for the specified subset of meters.
// If meterIDs is empty, reads all meters.
func (s *Source) ReadMeters(meterIDs []string) []Reading {
	s.mu.RLock()
	defer s.mu.RUnlock()

	idSet := make(map[string]bool, len(meterIDs))
	for _, id := range meterIDs {
		idSet[id] = true
	}

	readings := make([]Reading, 0)
	now := time.Now()

	for _, m := range s.Meters {
		if len(idSet) > 0 && !idSet[m.ID] {
			continue
		}
		// Simulate realistic power readings
		fluctuation := (s.rng.Float64()*2 - 1) * m.Fluctuation
		powerKW := m.BaseLoad + fluctuation
		if powerKW < 0 {
			powerKW = 0
		}
		voltageV := 220.0 + (s.rng.Float64()*2-1)*10.0 // 210–230 V
		currentA := (powerKW * 1000) / voltageV

		readings = append(readings, Reading{
			MeterID:   m.ID,
			Location:  m.Location,
			Timestamp: now,
			PowerKW:   powerKW,
			VoltageV:  voltageV,
			CurrentA:  currentA,
		})
	}
	return readings
}