// Package validator provides data validation for energy meter readings.
// It wraps the Rust validation library (rust_validator) via cgo.
//
// The Rust library is compiled as a static library (rust_validator.lib on Windows,
// librust_validator.a on Unix) and linked at build time.
//
// Build requirements:
//   - Rust toolchain (cargo, rustc)
//   - CGo enabled (set CGO_ENABLED=1)
//   - Library path pointing to the compiled Rust library
//
// Example build:
//   cd rust_validator && cargo build --release
//   go build -o build/collector.exe -ldflags="-r rust_validator/target/release" ./cmd/collector
package validator

/*
#cgo LDFLAGS: -L${SRCDIR}/../.. -lrust_validator -lm
#cgo windows LDFLAGS: -LC:/rust_lib -lrust_validator -lws2_32 -luserenv -lntdll -static-libgcc
#cgo linux LDFLAGS: -L${SRCDIR}/../../rust_validator/target/release -lrust_validator -lm -ldl
#cgo darwin LDFLAGS: -L${SRCDIR}/../../rust_validator/target/release -lrust_validator -lm -ldl

#include <stdlib.h>

// Validation result codes
typedef enum {
    VALID = 0,
    INVALID_METER_ID = 1,
    POWER_OUT_OF_RANGE = 2,
    VOLTAGE_OUT_OF_RANGE = 3,
    CURRENT_OUT_OF_RANGE = 4,
    INVALID_TIMESTAMP = 5,
    INVALID_LOCATION = 6,
    METER_ID_TOO_LONG = 7,
    LOCATION_TOO_LONG = 8,
    INTERNAL_ERROR = 99,
} ValidationCode;

typedef struct {
    ValidationCode code;
    char* message;
} ValidationResult;

// Free a ValidationResult allocated by Rust
void rust_validator_free_result(ValidationResult* result);

// Validate individual fields
ValidationResult rust_validator_check_meter_id(const char* meter_id);
ValidationResult rust_validator_check_power(double power_kw);
ValidationResult rust_validator_check_voltage(double voltage_v);
ValidationResult rust_validator_check_current(double current_a);
ValidationResult rust_validator_check_location(const char* location);
ValidationResult rust_validator_check_timestamp(long long timestamp_us);

// Validate a complete reading as JSON
ValidationResult rust_validator_check_reading(const char* json_str);
*/
import "C"
import (
	"encoding/json"
	"fmt"
	"unsafe"
)

// ValidationCode matches the Rust enum.
type ValidationCode int

const (
	Valid              ValidationCode = 0
	InvalidMeterID     ValidationCode = 1
	PowerOutOfRange    ValidationCode = 2
	VoltageOutOfRange  ValidationCode = 3
	CurrentOutOfRange  ValidationCode = 4
	InvalidTimestamp   ValidationCode = 5
	InvalidLocation    ValidationCode = 6
	MeterIDTooLong     ValidationCode = 7
	LocationTooLong    ValidationCode = 8
	InternalError      ValidationCode = 99
)

// String returns a human-readable name for the validation code.
func (c ValidationCode) String() string {
	switch c {
	case Valid:
		return "valid"
	case InvalidMeterID:
		return "invalid_meter_id"
	case PowerOutOfRange:
		return "power_out_of_range"
	case VoltageOutOfRange:
		return "voltage_out_of_range"
	case CurrentOutOfRange:
		return "current_out_of_range"
	case InvalidTimestamp:
		return "invalid_timestamp"
	case InvalidLocation:
		return "invalid_location"
	case MeterIDTooLong:
		return "meter_id_too_long"
	case LocationTooLong:
		return "location_too_long"
	case InternalError:
		return "internal_error"
	default:
		return "unknown"
	}
}

// Result holds the outcome of a validation check.
type Result struct {
	Valid   bool
	Code    ValidationCode
	Message string
}

// ReadingInput represents a meter reading to validate.
type ReadingInput struct {
	MeterID   string  `json:"meter_id"`
	Location  string  `json:"location"`
	Timestamp int64   `json:"timestamp,omitempty"`
	PowerKW   float64 `json:"power_kw"`
	VoltageV  float64 `json:"voltage_v"`
	CurrentA  float64 `json:"current_a"`
}

// convertResult converts a C ValidationResult to a Go Result.
func convertResult(cResult C.ValidationResult) Result {
	msg := C.GoString(cResult.message)
	C.rust_validator_free_result(&cResult)
	return Result{
		Valid:   cResult.code == C.VALID,
		Code:    ValidationCode(cResult.code),
		Message: msg,
	}
}

// CheckMeterID validates a meter ID format.
func CheckMeterID(meterID string) Result {
	cID := C.CString(meterID)
	defer C.free(unsafe.Pointer(cID))

	cResult := C.rust_validator_check_meter_id(cID)
	return convertResult(cResult)
}

// CheckPower validates a power value (kW).
func CheckPower(powerKW float64) Result {
	cResult := C.rust_validator_check_power(C.double(powerKW))
	return convertResult(cResult)
}

// CheckVoltage validates a voltage value (V).
func CheckVoltage(voltageV float64) Result {
	cResult := C.rust_validator_check_voltage(C.double(voltageV))
	return convertResult(cResult)
}

// CheckCurrent validates a current value (A).
func CheckCurrent(currentA float64) Result {
	cResult := C.rust_validator_check_current(C.double(currentA))
	return convertResult(cResult)
}

// CheckLocation validates a location string.
func CheckLocation(location string) Result {
	cLoc := C.CString(location)
	defer C.free(unsafe.Pointer(cLoc))

	cResult := C.rust_validator_check_location(cLoc)
	return convertResult(cResult)
}

// CheckTimestamp validates a timestamp (microseconds since Unix epoch).
func CheckTimestamp(timestampUs int64) Result {
	cResult := C.rust_validator_check_timestamp(C.longlong(timestampUs))
	return convertResult(cResult)
}

// CheckReading validates a complete meter reading.
// Returns a slice of validation errors (empty slice means valid).
func CheckReading(reading ReadingInput) []string {
	data, err := json.Marshal(reading)
	if err != nil {
		return []string{fmt.Sprintf("failed to marshal reading: %v", err)}
	}

	cJSON := C.CString(string(data))
	defer C.free(unsafe.Pointer(cJSON))

	cResult := C.rust_validator_check_reading(cJSON)
	result := convertResult(cResult)

	if result.Valid {
		return nil
	}

	// The Rust library joins multiple errors with "; "
	// We split them back for the Go API
	return []string{result.Message}
}

// ValidateReading performs full validation of a reading and returns
// a boolean indicating validity and a slice of error messages.
func ValidateReading(reading ReadingInput) (bool, []string) {
	errs := CheckReading(reading)
	return len(errs) == 0, errs
}