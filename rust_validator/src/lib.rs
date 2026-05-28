//! Rust Validator — библиотека для валидации данных энергопотребления.
//!
//! Предоставляет функции для проверки:
//! - Формата ID счётчика (`MTR-XXX`)
//! - Диапазонов значений (мощность, напряжение, ток)
//! - Корректности временных меток
//! - Допустимых локаций
//!
//! Библиотека собирается как `cdylib` (динамическая) и `staticlib` (статическая)
//! для интеграции с Go через cgo.

use std::ffi::{CStr, CString};
use std::os::raw::c_char;

// ──────────────────────────────────────────────
// Константы валидации
// ──────────────────────────────────────────────

/// Минимальная допустимая мощность (кВт)
const MIN_POWER_KW: f64 = 0.0;
/// Максимальная допустимая мощность (кВт)
const MAX_POWER_KW: f64 = 150.0;
/// Минимальное допустимое напряжение (В)
const MIN_VOLTAGE_V: f64 = 100.0;
/// Максимальное допустимое напряжение (В)
const MAX_VOLTAGE_V: f64 = 300.0;
/// Минимальный допустимый ток (А)
const MIN_CURRENT_A: f64 = 0.0;
/// Максимальный допустимый ток (А)
const MAX_CURRENT_A: f64 = 500.0;
/// Максимальная допустимая длина ID счётчика
const MAX_METER_ID_LEN: usize = 16;
/// Максимальная допустимая длина названия локации
const MAX_LOCATION_LEN: usize = 64;

/// Допустимые локации
const VALID_LOCATIONS: &[&str] = &[
    "Building A - Floor 1",
    "Building A - Floor 2",
    "Building B - Server Room",
    "Building B - Lobby",
    "Building C - Workshop",
    "Building C - Office",
    "Building D - Warehouse",
    "Building D - Charging Station",
];

// ──────────────────────────────────────────────
// Типы результатов валидации
// ──────────────────────────────────────────────

/// Код результата валидации.
#[repr(C)]
#[derive(Debug, Clone, Copy, PartialEq)]
pub enum ValidationCode {
    /// Валидация прошла успешно
    Valid = 0,
    /// Некорректный формат ID счётчика
    InvalidMeterId = 1,
    /// Мощность вне допустимого диапазона
    PowerOutOfRange = 2,
    /// Напряжение вне допустимого диапазона
    VoltageOutOfRange = 3,
    /// Ток вне допустимого диапазона
    CurrentOutOfRange = 4,
    /// Некорректная временная метка
    InvalidTimestamp = 5,
    /// Некорректная локация
    InvalidLocation = 6,
    /// ID счётчика слишком длинный
    MeterIdTooLong = 7,
    /// Название локации слишком длинное
    LocationTooLong = 8,
    /// Внутренняя ошибка (например, JSON)
    InternalError = 99,
}

/// Результат валидации одного поля.
#[repr(C)]
pub struct ValidationResult {
    /// Код результата
    pub code: ValidationCode,
    /// Сообщение об ошибке (C-строка, владеющая памятью)
    pub message: *mut c_char,
}

// ──────────────────────────────────────────────
// Внутренние Rust-функции
// ──────────────────────────────────────────────

/// Проверяет формат ID счётчика: должен быть вида `MTR-XXX`, где XXX — цифры.
fn validate_meter_id(id: &str) -> Result<(), String> {
    if id.len() > MAX_METER_ID_LEN {
        return Err(format!(
            "meter_id too long: {} chars (max {})",
            id.len(),
            MAX_METER_ID_LEN
        ));
    }
    if !id.starts_with("MTR-") {
        return Err(format!(
            "invalid meter_id format: '{}' must start with 'MTR-'",
            id
        ));
    }
    let num_part = &id[4..];
    if num_part.is_empty() || !num_part.chars().all(|c| c.is_ascii_digit()) {
        return Err(format!(
            "invalid meter_id format: '{}' must be MTR- followed by digits",
            id
        ));
    }
    Ok(())
}

/// Проверяет, что мощность находится в допустимом диапазоне.
fn validate_power(power_kw: f64) -> Result<(), String> {
    if power_kw < MIN_POWER_KW || power_kw > MAX_POWER_KW {
        return Err(format!(
            "power_kw {:.3} out of range [{}, {}]",
            power_kw, MIN_POWER_KW, MAX_POWER_KW
        ));
    }
    Ok(())
}

/// Проверяет, что напряжение находится в допустимом диапазоне.
fn validate_voltage(voltage_v: f64) -> Result<(), String> {
    if voltage_v < MIN_VOLTAGE_V || voltage_v > MAX_VOLTAGE_V {
        return Err(format!(
            "voltage_v {:.3} out of range [{}, {}]",
            voltage_v, MIN_VOLTAGE_V, MAX_VOLTAGE_V
        ));
    }
    Ok(())
}

/// Проверяет, что ток находится в допустимом диапазоне.
fn validate_current(current_a: f64) -> Result<(), String> {
    if current_a < MIN_CURRENT_A || current_a > MAX_CURRENT_A {
        return Err(format!(
            "current_a {:.3} out of range [{}, {}]",
            current_a, MIN_CURRENT_A, MAX_CURRENT_A
        ));
    }
    Ok(())
}

/// Проверяет корректность локации.
fn validate_location(location: &str) -> Result<(), String> {
    if location.len() > MAX_LOCATION_LEN {
        return Err(format!(
            "location too long: {} chars (max {})",
            location.len(),
            MAX_LOCATION_LEN
        ));
    }
    if !VALID_LOCATIONS.contains(&location) {
        return Err(format!(
            "invalid location: '{}' is not in the allowed list",
            location
        ));
    }
    Ok(())
}

/// Проверяет корректность временной метки (Unix timestamp в микросекундах).
/// Допустимый диапазон: 2000-01-01 .. 2100-01-01
fn validate_timestamp_us(timestamp_us: i64) -> Result<(), String> {
    let min_ts: i64 = 946684800000000;   // 2000-01-01T00:00:00Z в микросекундах
    let max_ts: i64 = 4102444800000000;  // 2100-01-01T00:00:00Z в микросекундах
    if timestamp_us < min_ts || timestamp_us > max_ts {
        return Err(format!(
            "timestamp {} out of valid range [2000-01-01 .. 2100-01-01]",
            timestamp_us
        ));
    }
    Ok(())
}

// ──────────────────────────────────────────────
// Основная функция валидации (Rust API)
// ──────────────────────────────────────────────

/// Структура, представляющая одно показание счётчика для валидации.
#[derive(Debug, serde::Deserialize)]
pub struct ReadingInput {
    pub meter_id: String,
    pub location: String,
    pub timestamp: Option<i64>,
    pub power_kw: f64,
    pub voltage_v: f64,
    pub current_a: f64,
}

/// Результат полной валидации показания.
#[derive(Debug)]
pub struct ReadingValidation {
    pub is_valid: bool,
    pub errors: Vec<String>,
    /// Коды ошибок в том же порядке, что и errors.
    pub codes: Vec<ValidationCode>,
}

/// Валидирует одно показание счётчика.
/// Возвращает `ReadingValidation` со списком ошибок (если есть).
pub fn validate_reading(reading: &ReadingInput) -> ReadingValidation {
    let mut errors: Vec<String> = Vec::new();
    let mut codes: Vec<ValidationCode> = Vec::new();

    // Валидация meter_id
    if let Err(e) = validate_meter_id(&reading.meter_id) {
        errors.push(e);
        codes.push(ValidationCode::InvalidMeterId);
    }

    // Валидация location
    if let Err(e) = validate_location(&reading.location) {
        errors.push(e);
        codes.push(ValidationCode::InvalidLocation);
    }

    // Валидация timestamp
    if let Some(ts) = reading.timestamp {
        if let Err(e) = validate_timestamp_us(ts) {
            errors.push(e);
            codes.push(ValidationCode::InvalidTimestamp);
        }
    }

    // Валидация power_kw
    if let Err(e) = validate_power(reading.power_kw) {
        errors.push(e);
        codes.push(ValidationCode::PowerOutOfRange);
    }

    // Валидация voltage_v
    if let Err(e) = validate_voltage(reading.voltage_v) {
        errors.push(e);
        codes.push(ValidationCode::VoltageOutOfRange);
    }

    // Валидация current_a
    if let Err(e) = validate_current(reading.current_a) {
        errors.push(e);
        codes.push(ValidationCode::CurrentOutOfRange);
    }

    ReadingValidation {
        is_valid: errors.is_empty(),
        errors,
        codes,
    }
}

/// Валидирует JSON-строку с одним показанием счётчика.
/// Возвращает `ReadingValidation`.
pub fn validate_reading_json(json_str: &str) -> Result<ReadingValidation, String> {
    let reading: ReadingInput = serde_json::from_str(json_str)
        .map_err(|e| format!("failed to parse JSON: {}", e))?;
    Ok(validate_reading(&reading))
}

// ──────────────────────────────────────────────
// C-compatible API для вызова из Go через cgo
// ──────────────────────────────────────────────

/// Создаёт `ValidationResult` из кода и сообщения.
/// Вызывающий (Go) должен освободить память через `rust_validator_free_result`.
fn make_result(code: ValidationCode, msg: &str) -> ValidationResult {
    let c_msg = CString::new(msg).unwrap_or_else(|_| CString::new("unknown error").unwrap());
    ValidationResult {
        code,
        message: c_msg.into_raw(),
    }
}

/// Создаёт успешный результат валидации.
fn make_valid_result() -> ValidationResult {
    let c_msg = CString::new("ok").unwrap();
    ValidationResult {
        code: ValidationCode::Valid,
        message: c_msg.into_raw(),
    }
}

/// Освобождает память, выделенную под `ValidationResult`.
///
/// # Safety
/// `result` должен быть получен из любой функции `rust_validator_*` этой библиотеки.
#[no_mangle]
pub unsafe extern "C" fn rust_validator_free_result(result: *mut ValidationResult) {
    if result.is_null() {
        return;
    }
    let result = &mut *result;
    if !result.message.is_null() {
        let _ = CString::from_raw(result.message);
    }
}

/// Валидирует ID счётчика.
///
/// # Safety
/// `meter_id` должна быть валидной C-строкой (null-terminated).
#[no_mangle]
pub unsafe extern "C" fn rust_validator_check_meter_id(meter_id: *const c_char) -> ValidationResult {
    if meter_id.is_null() {
        return make_result(ValidationCode::InternalError, "meter_id is null");
    }
    let id = match CStr::from_ptr(meter_id).to_str() {
        Ok(s) => s,
        Err(_) => return make_result(ValidationCode::InternalError, "meter_id is not valid UTF-8"),
    };
    match validate_meter_id(id) {
        Ok(()) => make_valid_result(),
        Err(e) => make_result(ValidationCode::InvalidMeterId, &e),
    }
}

/// Валидирует значение мощности (кВт).
#[no_mangle]
pub extern "C" fn rust_validator_check_power(power_kw: f64) -> ValidationResult {
    match validate_power(power_kw) {
        Ok(()) => make_valid_result(),
        Err(e) => make_result(ValidationCode::PowerOutOfRange, &e),
    }
}

/// Валидирует значение напряжения (В).
#[no_mangle]
pub extern "C" fn rust_validator_check_voltage(voltage_v: f64) -> ValidationResult {
    match validate_voltage(voltage_v) {
        Ok(()) => make_valid_result(),
        Err(e) => make_result(ValidationCode::VoltageOutOfRange, &e),
    }
}

/// Валидирует значение тока (А).
#[no_mangle]
pub extern "C" fn rust_validator_check_current(current_a: f64) -> ValidationResult {
    match validate_current(current_a) {
        Ok(()) => make_valid_result(),
        Err(e) => make_result(ValidationCode::CurrentOutOfRange, &e),
    }
}

/// Валидирует локацию.
///
/// # Safety
/// `location` должна быть валидной C-строкой (null-terminated).
#[no_mangle]
pub unsafe extern "C" fn rust_validator_check_location(location: *const c_char) -> ValidationResult {
    if location.is_null() {
        return make_result(ValidationCode::InternalError, "location is null");
    }
    let loc = match CStr::from_ptr(location).to_str() {
        Ok(s) => s,
        Err(_) => return make_result(ValidationCode::InternalError, "location is not valid UTF-8"),
    };
    match validate_location(loc) {
        Ok(()) => make_valid_result(),
        Err(e) => make_result(ValidationCode::InvalidLocation, &e),
    }
}

/// Валидирует временную метку (в микросекундах).
#[no_mangle]
pub extern "C" fn rust_validator_check_timestamp(timestamp_us: i64) -> ValidationResult {
    match validate_timestamp_us(timestamp_us) {
        Ok(()) => make_valid_result(),
        Err(e) => make_result(ValidationCode::InvalidTimestamp, &e),
    }
}

/// Валидирует полное показание счётчика, переданное как JSON-строка.
///
/// Ожидаемый формат JSON:
/// ```json
/// {
///   "meter_id": "MTR-001",
///   "location": "Building A - Floor 1",
///   "timestamp": 1700000000000000,
///   "power_kw": 42.5,
///   "voltage_v": 220.0,
///   "current_a": 193.18
/// }
/// ```
///
/// # Safety
/// `json_str` должна быть валидной C-строкой (null-terminated).
#[no_mangle]
pub unsafe extern "C" fn rust_validator_check_reading(json_str: *const c_char) -> ValidationResult {
    if json_str.is_null() {
        return make_result(ValidationCode::InternalError, "json string is null");
    }
    let input = match CStr::from_ptr(json_str).to_str() {
        Ok(s) => s,
        Err(_) => return make_result(ValidationCode::InternalError, "json is not valid UTF-8"),
    };

    match validate_reading_json(input) {
        Ok(rv) => {
            if rv.is_valid {
                make_valid_result()
            } else {
                let combined = rv.errors.join("; ");
                // Возвращаем код первой ошибки вместо всегда InvalidMeterId
                let first_code = rv.codes.first().copied().unwrap_or(ValidationCode::InternalError);
                make_result(first_code, &combined)
            }
        }
        Err(e) => make_result(ValidationCode::InternalError, &e),
    }
}

// ──────────────────────────────────────────────
// Тесты
// ──────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_valid_meter_id() {
        assert!(validate_meter_id("MTR-001").is_ok());
        assert!(validate_meter_id("MTR-999").is_ok());
        assert!(validate_meter_id("MTR-12345").is_ok());
    }

    #[test]
    fn test_invalid_meter_id() {
        assert!(validate_meter_id("").is_err());
        assert!(validate_meter_id("MTR-").is_err());
        assert!(validate_meter_id("MTR-ABC").is_err());
        assert!(validate_meter_id("INV-001").is_err());
        assert!(validate_meter_id("mtr-001").is_err());
    }

    #[test]
    fn test_power_range() {
        assert!(validate_power(0.0).is_ok());
        assert!(validate_power(50.0).is_ok());
        assert!(validate_power(150.0).is_ok());
        assert!(validate_power(-1.0).is_err());
        assert!(validate_power(151.0).is_err());
    }

    #[test]
    fn test_voltage_range() {
        assert!(validate_voltage(100.0).is_ok());
        assert!(validate_voltage(220.0).is_ok());
        assert!(validate_voltage(300.0).is_ok());
        assert!(validate_voltage(99.0).is_err());
        assert!(validate_voltage(301.0).is_err());
    }

    #[test]
    fn test_current_range() {
        assert!(validate_current(0.0).is_ok());
        assert!(validate_current(250.0).is_ok());
        assert!(validate_current(500.0).is_ok());
        assert!(validate_current(-0.1).is_err());
        assert!(validate_current(501.0).is_err());
    }

    #[test]
    fn test_valid_location() {
        assert!(validate_location("Building A - Floor 1").is_ok());
        assert!(validate_location("Building D - Charging Station").is_ok());
    }

    #[test]
    fn test_invalid_location() {
        assert!(validate_location("Unknown Building").is_err());
        assert!(validate_location("").is_err());
    }

    #[test]
    fn test_timestamp_range() {
        // 2023-01-01T00:00:00Z в микросекундах
        let ts: i64 = 1672531200000000;
        assert!(validate_timestamp_us(ts).is_ok());
        // Слишком старый
        assert!(validate_timestamp_us(0).is_err());
        // Слишком будущий
        assert!(validate_timestamp_us(9999999999999999).is_err());
    }

    #[test]
    fn test_full_validation_valid() {
        let reading = ReadingInput {
            meter_id: "MTR-001".to_string(),
            location: "Building A - Floor 1".to_string(),
            timestamp: Some(1672531200000000),
            power_kw: 42.5,
            voltage_v: 220.0,
            current_a: 193.18,
        };
        let result = validate_reading(&reading);
        assert!(result.is_valid);
        assert!(result.errors.is_empty());
    }

    #[test]
    fn test_full_validation_invalid() {
        let reading = ReadingInput {
            meter_id: "BAD-ID".to_string(),
            location: "Nowhere".to_string(),
            timestamp: Some(0),
            power_kw: -5.0,
            voltage_v: 50.0,
            current_a: 600.0,
        };
        let result = validate_reading(&reading);
        assert!(!result.is_valid);
        assert!(result.errors.len() >= 5);
    }

    #[test]
    fn test_json_validation() {
        let json = r#"{
            "meter_id": "MTR-001",
            "location": "Building A - Floor 1",
            "timestamp": 1672531200000000,
            "power_kw": 42.5,
            "voltage_v": 220.0,
            "current_a": 193.18
        }"#;
        let result = validate_reading_json(json).unwrap();
        assert!(result.is_valid);
    }

    #[test]
    fn test_json_validation_invalid() {
        let json = r#"{
            "meter_id": "BAD",
            "location": "Unknown",
            "timestamp": 0,
            "power_kw": 999.0,
            "voltage_v": 0.0,
            "current_a": -1.0
        }"#;
        let result = validate_reading_json(json).unwrap();
        assert!(!result.is_valid);
    }

    #[test]
    fn test_validation_error_codes() {
        // Проверяем, что каждая ошибка возвращает свой код
        let reading = ReadingInput {
            meter_id: "BAD-ID".to_string(),
            location: "Building A - Floor 1".to_string(),
            timestamp: Some(1672531200000000),
            power_kw: 42.5,
            voltage_v: 220.0,
            current_a: 193.18,
        };
        let result = validate_reading(&reading);
        assert!(!result.is_valid);
        assert_eq!(result.codes[0], ValidationCode::InvalidMeterId);

        // Только power вне диапазона
        let reading2 = ReadingInput {
            meter_id: "MTR-001".to_string(),
            location: "Building A - Floor 1".to_string(),
            timestamp: Some(1672531200000000),
            power_kw: 999.0,
            voltage_v: 220.0,
            current_a: 193.18,
        };
        let result2 = validate_reading(&reading2);
        assert!(!result2.is_valid);
        assert_eq!(result2.codes[0], ValidationCode::PowerOutOfRange);

        // Только voltage вне диапазона
        let reading3 = ReadingInput {
            meter_id: "MTR-001".to_string(),
            location: "Building A - Floor 1".to_string(),
            timestamp: Some(1672531200000000),
            power_kw: 42.5,
            voltage_v: 0.0,
            current_a: 193.18,
        };
        let result3 = validate_reading(&reading3);
        assert!(!result3.is_valid);
        assert_eq!(result3.codes[0], ValidationCode::VoltageOutOfRange);

        // Только current вне диапазона
        let reading4 = ReadingInput {
            meter_id: "MTR-001".to_string(),
            location: "Building A - Floor 1".to_string(),
            timestamp: Some(1672531200000000),
            power_kw: 42.5,
            voltage_v: 220.0,
            current_a: 999.0,
        };
        let result4 = validate_reading(&reading4);
        assert!(!result4.is_valid);
        assert_eq!(result4.codes[0], ValidationCode::CurrentOutOfRange);

        // Только location невалидна
        let reading5 = ReadingInput {
            meter_id: "MTR-001".to_string(),
            location: "Unknown Place".to_string(),
            timestamp: Some(1672531200000000),
            power_kw: 42.5,
            voltage_v: 220.0,
            current_a: 193.18,
        };
        let result5 = validate_reading(&reading5);
        assert!(!result5.is_valid);
        assert_eq!(result5.codes[0], ValidationCode::InvalidLocation);

        // Только timestamp невалиден
        let reading6 = ReadingInput {
            meter_id: "MTR-001".to_string(),
            location: "Building A - Floor 1".to_string(),
            timestamp: Some(0),
            power_kw: 42.5,
            voltage_v: 220.0,
            current_a: 193.18,
        };
        let result6 = validate_reading(&reading6);
        assert!(!result6.is_valid);
        assert_eq!(result6.codes[0], ValidationCode::InvalidTimestamp);
    }

    #[test]
    fn test_check_reading_returns_correct_code() {
        // Проверяем, что C-функция возвращает правильный код для power
        // Используем validate_reading_json + проверку codes
        let json = r#"{
            "meter_id": "MTR-001",
            "location": "Building A - Floor 1",
            "timestamp": 1672531200000000,
            "power_kw": 999.0,
            "voltage_v": 220.0,
            "current_a": 193.18
        }"#;
        let result = validate_reading_json(json).unwrap();
        assert!(!result.is_valid);
        assert_eq!(result.codes[0], ValidationCode::PowerOutOfRange,
            "power error should return PowerOutOfRange, not InvalidMeterId");
    }
}
