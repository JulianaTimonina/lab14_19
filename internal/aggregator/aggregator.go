// Package aggregator implements tumbling window aggregation for energy readings.
// It collects raw readings over time-based or count-based windows and produces
// aggregated results (sum, average, min, max) to reduce data volume.
package aggregator

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/yliana-efimova/energy-collector/internal/source"
)

// WindowType defines the tumbling window trigger mode.
type WindowType int

const (
	// WindowTime triggers aggregation after a fixed duration.
	WindowTime WindowType = iota
	// WindowCount triggers aggregation after a fixed number of readings.
	WindowCount
)

// String returns the human-readable name of the window type.
func (w WindowType) String() string {
	switch w {
	case WindowTime:
		return "time"
	case WindowCount:
		return "count"
	default:
		return "unknown"
	}
}

// Config defines the tumbling window configuration.
type Config struct {
	// Type selects the window trigger mode (time-based or count-based).
	Type WindowType
	// WindowSize is the duration for time-based windows (e.g., 30s).
	// Only used when Type == WindowTime.
	WindowSize time.Duration
	// MaxRecords is the maximum number of records per window for count-based windows.
	// Only used when Type == WindowCount.
	MaxRecords int
}

// AggregatedReading represents a single aggregated result for one meter.
type AggregatedReading struct {
	MeterID       string    `json:"meter_id"`
	Location      string    `json:"location"`
	WindowStart   time.Time `json:"window_start"`
	WindowEnd     time.Time `json:"window_end"`
	Count         int       `json:"count"`
	SumPowerKW    float64   `json:"sum_power_kw"`
	AvgPowerKW    float64   `json:"avg_power_kw"`
	MinPowerKW    float64   `json:"min_power_kw"`
	MaxPowerKW    float64   `json:"max_power_kw"`
	AvgVoltageV   float64   `json:"avg_voltage_v"`
	AvgCurrentA   float64   `json:"avg_current_a"`
}

// meterWindow holds the raw readings accumulated for a single meter within one window.
type meterWindow struct {
	meterID   string
	location  string
	startTime time.Time
	readings  []source.Reading
}

// Aggregator manages tumbling windows for all meters.
type Aggregator struct {
	cfg    Config
	mu     sync.Mutex
	windows map[string]*meterWindow // keyed by meter ID
	windowStart time.Time           // start time of the current window
}

// New creates a new Aggregator with the given configuration.
func New(cfg Config) (*Aggregator, error) {
	if cfg.Type == WindowTime && cfg.WindowSize <= 0 {
		return nil, fmt.Errorf("aggregator: window size must be positive for time-based window")
	}
	if cfg.Type == WindowCount && cfg.MaxRecords <= 0 {
		return nil, fmt.Errorf("aggregator: max records must be positive for count-based window")
	}
	return &Aggregator{
		cfg:         cfg,
		windows:     make(map[string]*meterWindow),
		windowStart: time.Now(),
	}, nil
}

// Add inserts a reading into the aggregator. If the window is complete,
// it returns the aggregated readings and starts a new window.
// Otherwise, it returns nil, meaning the window is still accumulating.
func (a *Aggregator) Add(reading source.Reading) []AggregatedReading {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Check if we need to flush the current window (time-based trigger)
	if a.cfg.Type == WindowTime {
		if time.Since(a.windowStart) >= a.cfg.WindowSize {
			result := a.flushLocked()
			a.windowStart = time.Now()
			return result
		}
	}

	// Get or create the per-meter window
	mw, ok := a.windows[reading.MeterID]
	if !ok {
		mw = &meterWindow{
			meterID:   reading.MeterID,
			location:  reading.Location,
			startTime: a.windowStart,
			readings:  make([]source.Reading, 0, 16),
		}
		a.windows[reading.MeterID] = mw
	}

	mw.readings = append(mw.readings, reading)

	// Check if we need to flush (count-based trigger)
	if a.cfg.Type == WindowCount && len(mw.readings) >= a.cfg.MaxRecords {
		result := a.flushLocked()
		a.windowStart = time.Now()
		return result
	}

	return nil
}

// Flush forces the current window to close and returns all aggregated readings,
// regardless of whether the window is full. This is used during shutdown.
func (a *Aggregator) Flush() []AggregatedReading {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.flushLocked()
}

// flushLocked performs the actual aggregation and resets the window.
// Must be called with a.mu held.
func (a *Aggregator) flushLocked() []AggregatedReading {
	if len(a.windows) == 0 {
		return nil
	}

	windowEnd := time.Now()
	results := make([]AggregatedReading, 0, len(a.windows))

	for _, mw := range a.windows {
		if len(mw.readings) == 0 {
			continue
		}

		agg := AggregatedReading{
			MeterID:     mw.meterID,
			Location:    mw.location,
			WindowStart: mw.startTime,
			WindowEnd:   windowEnd,
			Count:       len(mw.readings),
			MinPowerKW:  math.MaxFloat64,
		}

		var totalVoltage, totalCurrent float64

		for _, r := range mw.readings {
			agg.SumPowerKW += r.PowerKW
			totalVoltage += r.VoltageV
			totalCurrent += r.CurrentA

			if r.PowerKW < agg.MinPowerKW {
				agg.MinPowerKW = r.PowerKW
			}
			if r.PowerKW > agg.MaxPowerKW {
				agg.MaxPowerKW = r.PowerKW
			}
		}

		n := float64(agg.Count)
		agg.AvgPowerKW = agg.SumPowerKW / n
		agg.AvgVoltageV = totalVoltage / n
		agg.AvgCurrentA = totalCurrent / n

		results = append(results, agg)
	}

	// Reset windows
	a.windows = make(map[string]*meterWindow)
	a.windowStart = time.Now()

	return results
}

// GetConfig returns a copy of the aggregator configuration.
func (a *Aggregator) GetConfig() Config {
	return a.cfg
}