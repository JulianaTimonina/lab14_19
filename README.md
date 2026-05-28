# Energy Collector — Распределённый сборщик данных энергопотребления

Система для распределённого сбора данных с эмулированных счётчиков электроэнергии. Несколько экземпляров Go-сборщика работают параллельно, координируясь через **etcd** для распределения шардов/источников данных.

Поддерживает **оконную агрегацию (tumbling window)** — сырые показания накапливаются на стороне Go и отправляются в лог уже агрегированными (суммы, средние, минимум/максимум), что снижает объём передаваемых данных.

Данные также доступны через **Apache Arrow Flight RPC** — высокопроизводительный протокол передачи колоночных данных. Go-сервер отдаёт показания в формате Arrow RecordBatch, а Python-клиент принимает и анализирует их.

## Архитектура

```
┌─────────────────────────────────────────────────────┐
│                     etcd                            │
│  (координация, лидерство, распределение шардов)     │
└──────┬──────────────┬──────────────┬────────────────┘
       │              │              │
┌──────▼──────┐ ┌─────▼──────┐ ┌────▼──────┐
│ Collector 1 │ │ Collector 2 │ │ Collector 3│
│ (node1)     │ │ (node2)     │ │ (node3)    │
│ Шарды: 1, 3 │ │ Шарды: 2, 5 │ │ Шард: 4    │
└──────┬──────┘ └──────┬───────┘ └──────┬─────┘
       │               │                │
       └───────┬───────┴────────┬───────┘
               │                │
        ┌──────▼──────┐  ┌──────▼──────┐
        │  Aggregator │  │  Aggregator │
        │ (tumbling   │  │ (tumbling   │
        │  window)    │  │  window)    │
        └──────┬──────┘  └──────┬──────┘
               │                │
               └───────┬────────┘
                       │
            ┌──────────▼──────────┐
            │  Source (эмулятор)  │
            │  50 счётчиков       │
            └──────────┬──────────┘
                       │
            ┌──────────▼──────────┐
            │  Arrow Flight Server│
            │  (gRPC + Arrow)     │
            │  порт 50051         │
            └──────────┬──────────┘
                       │
            ┌──────────▼──────────┐
            │  Python Arrow       │
            │  Flight Client      │
            └─────────────────────┘
```

### Компоненты

- **Source** ([`internal/source/source.go`](internal/source/source.go)) — эмулятор счётчиков электроэнергии. Генерирует реалистичные показания: мощность (кВт), напряжение (В), ток (А) с флуктуациями.
- **Coordinator** ([`internal/coordinator/coordinator.go`](internal/coordinator/coordinator.go)) — etcd-координация: регистрация инстансов, лидерство, создание и ребалансировка шардов.
- **Aggregator** ([`internal/aggregator/aggregator.go`](internal/aggregator/aggregator.go)) — оконная агрегация (tumbling window). Поддерживает два режима: временное окно (каждые N секунд) и окон по количеству записей (каждые M записей на счётчик).
- **Collector** ([`internal/collector/collector.go`](internal/collector/collector.go)) — сборщик данных: получает назначенные шарды из etcd, читает показания с эмулятора, передаёт в агрегатор (если включён) и выводит результат в лог.
- **Arrow Server** ([`internal/arrowserver/server.go`](internal/arrowserver/server.go)) — Apache Arrow Flight RPC сервер, отдающий показания счётчиков в колоночном формате Arrow.
- **Arrow Client** ([`python/arrow_client.py`](python/arrow_client.py)) — Python-клиент для получения данных через Arrow Flight RPC с выводом статистики и бенчмаркингом.
- **Main** ([`cmd/collector/main.go`](cmd/collector/main.go)) — точка входа для etcd-сборщика с CLI-флагами.
- **Arrow Server Main** ([`cmd/arrowserver/main.go`](cmd/arrowserver/main.go)) — точка входа для Arrow Flight сервера.
- **Rust Validator** ([`rust_validator/`](rust_validator/)) — библиотека на Rust для валидации данных энергопотребления. Проверяет формат ID счётчика, диапазоны значений (мощность, напряжение, ток), корректность временных меток и локаций. Интегрирована в Go-сборщик через cgo.
- **Go Validator Wrapper** ([`internal/validator/validator.go`](internal/validator/validator.go)) — Go-обёртка над Rust-библиотекой валидации через cgo.

## Требования

- Go 1.21+
- etcd (локально или в Docker)
- Python 3.8+ (для Arrow клиента)
- Rust toolchain (cargo, rustc) — только для сборки с валидацией
- Make (опционально)
- GCC (MinGW на Windows) — для cgo при сборке с валидацией

## Быстрый старт

### 1. Запуск etcd

**Через Docker:**
```bash
make docker-etcd
```

**Или вручную** (если etcd установлен локально):
```bash
etcd
```

### 2. Сборка

**Без валидации (обычная сборка):**
```bash
make build
```

**С Rust-валидацией данных:**
```bash
make build-with-rust
```

> **Примечание:** Для сборки с Rust-валидацией требуется установленный Rust toolchain и GCC (MinGW на Windows). На Windows также требуется скопировать `rust_validator.dll` в директорию с исполняемым файлом или в `C:\rust_lib\`.

### 3. Запуск нескольких сборщиков

В разных терминалах:

```bash
# Терминал 1
make run-node1

# Терминал 2
make run-node2

# Терминал 3
make run-node3
```

Или одной командой:
```bash
make run-all
```

### 4. Ручной запуск с параметрами

```bash
go run ./cmd/collector \
  -id=custom-node \
  -endpoints=localhost:2379 \
  -meters=100 \
  -shards=10 \
  -interval=5s
```

## Apache Arrow Flight RPC

Apache Arrow — это кросс-языковой колоночный формат данных, оптимизированный для аналитических нагрузок. Flight RPC — это протокол поверх gRPC для высокопроизводительной передачи Arrow-данных между сервисами.

### Запуск Arrow Flight сервера

```bash
# Через Make
make run-arrow

# Или вручную
go run ./cmd/arrowserver -port=50051 -meters=50 -interval=10s
```

Сервер запускается на порту 50051 и сразу начинает отдавать данные эмулированных счётчиков через Arrow Flight RPC.

### Установка Python-зависимостей

```bash
pip install -r python/requirements.txt
```

### Запуск Python-клиента

```bash
# Получение данных и вывод статистики
make run-arrow-client

# Или вручную
python python/arrow_client.py --server localhost:50051
```

### Бенчмаркинг производительности

```bash
# Запуск с бенчмарком (5 итераций)
make run-arrow-bench

# Или вручную с настройкой итераций
python python/arrow_client.py --server localhost:50051 --benchmark --iterations=10
```

### Пример вывода Python-клиента

```
Connecting to Arrow Flight server at localhost:50051...

============================================================
📊 ENERGY DATA — Arrow Flight RPC
============================================================
  Server:       localhost:50051
  Rows:         50
  Columns:      6
  Column names: ['meter_id', 'location', 'timestamp', 'power_kw', 'voltage_v', 'current_a']
============================================================

📋 Schema:
  • meter_id: string
  • location: string
  • timestamp: timestamp[us]
  • power_kw: double
  • voltage_v: double
  • current_a: double

📈 Statistics:
  Power (kW):   min=3.45, max=78.23, avg=40.12
  Voltage (V):  min=210.15, max=229.87, avg=220.04
  Current (A):  min=15.02, max=354.67, avg=182.34
  Unique meters: 50

💾 Memory usage:
  Arrow table:  2,400 bytes (2.3 KB)
============================================================
```

### Сравнение форматов: Arrow vs JSON

| Характеристика | JSON | Apache Arrow |
|---------------|------|-------------|
| Формат | Текстовый (строчный) | Бинарный (колоночный) |
| Размер данных (50 записей) | ~8-10 KB | ~2-3 KB |
| Скорость сериализации | Медленная ( reflection) | Быстрая (zero-copy) |
| Скорость десериализации | Медленная (парсинг) | Быстрая (zero-copy) |
| Типизация | Слабая (строки) | Строгая (схема) |
| Сжатие | Нет | Встроенное (словари, RLE) |
| Потоковая передача | Нет (весь файл) | Да (RecordBatch) |
| Языковая поддержка | Все языки | C++, Go, Python, Java, Rust и др. |

## Оконная агрегация (Tumbling Window)

Для снижения объёма передаваемых данных можно включить оконную агрегацию на стороне Go-сборщика. Вместо отправки каждого отдельного показания, сборщик накапливает данные в окне и отправляет агрегированный результат.

### Режимы агрегации

| Режим | Флаг | Описание |
|-------|------|----------|
| **Временное окно** | `-agg-window=30s` | Агрегация каждые N секунд. Все показания, накопленные за окно, сворачиваются в одну запись на счётчик. |
| **Окно по количеству** | `-agg-count=100` | Агрегация после накопления M записей на каждый счётчик. |

### Примеры запуска с агрегацией

**Временное окно (30 секунд):**
```bash
make run-agg-time
# или вручную:
go run ./cmd/collector -id=node1 -endpoints=localhost:2379 -meters=50 -shards=5 -interval=10s -agg-window=30s
```

**Окно по количеству (100 записей на счётчик):**
```bash
make run-agg-count
# или вручную:
go run ./cmd/collector -id=node1 -endpoints=localhost:2379 -meters=50 -shards=5 -interval=10s -agg-count=100
```

**Три узла с временным окном:**
```bash
make run-agg-time-all
```

### Пример вывода с агрегацией

```
2026/05/28 14:15:23.123456 [node1] registered in etcd
2026/05/28 14:15:23.234567 [node1] tumbling window aggregator enabled: type=time, window_size=30s, max_records=0
2026/05/28 14:15:23.345678 [node1] rebalanced 5 shards across 3 collectors
2026/05/28 14:15:33.456789 [node1] AGGREGATED {"meter_id":"MTR-001","location":"Building A - Floor 1","window_start":"...","window_end":"...","count":3,"sum_power_kw":18.69,"avg_power_kw":6.23,"min_power_kw":5.87,"max_power_kw":6.58,"avg_voltage_v":224.5,"avg_current_a":27.75}
2026/05/28 14:15:33.456789 [node1] window completed: 20 aggregated records (raw: 60, total raw: 60)
```

Ключевые отличия от сырого режима:
- Вместо `DATA` выводится `AGGREGATED` с префиксом
- Одна агрегированная запись заменяет несколько сырых (в примере выше: 3 сырых → 1 агрегированная)
- Поля: `count` (сколько сырых записей вошло), `sum_power_kw`, `avg_power_kw`, `min_power_kw`, `max_power_kw`, `avg_voltage_v`, `avg_current_a`
- При завершении работы (`Ctrl+C`) выполняется `final flush` — вывод остатков незакрытого окна

### Как это работает

1. **Регистрация**: Каждый сборщик регистрируется в etcd с lease (TTL=10s) и запускает heartbeat.
2. **Лидерство**: Инстансы участвуют в leader election. Лидер инициализирует шарды и выполняет ребалансировку.
3. **Шардирование**: Все счётчики распределяются по N шардам. Шарды сохраняются в etcd.
4. **Назначение**: Лидер распределяет шарды между активными сборщиками (round-robin).
5. **Сбор данных**: Каждый сборщик читает свои шарды из etcd, получает показания с эмулятора.
6. **Агрегация (опционально)**: Если включён агрегатор, показания накапливаются в tumbling window. По заполнении окна (по времени или по количеству записей) формируется агрегированный JSON и выводится в лог.
7. **Сырой режим (по умолчанию)**: Если агрегатор не включён, каждое показание выводится отдельно как `DATA`.
8. **Динамика**: При появлении/исчезновении сборщика лидер автоматически перераспределяет шарды.
9. **Arrow Flight RPC**: Параллельно с etcd-сборкой работает Arrow Flight сервер, который отдаёт показания в колоночном формате через gRPC. Python-клиент может получать эти данные для анализа и бенчмаркинга.

## Rust-валидация данных

Библиотека [`rust_validator`](rust_validator/) реализована на Rust и предоставляет функции для валидации данных энергопотребления. Она интегрирована в Go-сборщик через механизм cgo.

### Что проверяется

| Проверка | Описание | Допустимые значения |
|----------|----------|-------------------|
| **ID счётчика** | Формат `MTR-XXX`, где XXX — цифры | `MTR-001`, `MTR-999`, макс. 16 символов |
| **Мощность (кВт)** | Диапазон значений | 0.0 – 150.0 кВт |
| **Напряжение (В)** | Диапазон значений | 100.0 – 300.0 В |
| **Ток (А)** | Диапазон значений | 0.0 – 500.0 А |
| **Временная метка** | Unix timestamp в микросекундах | 2000-01-01 – 2100-01-01 |
| **Локация** | Должна быть из списка допустимых | "Building A - Floor 1", "Building D - Charging Station" и др. |

### Сборка Rust-библиотеки

```bash
# Сборка (release)
make rust-build

# Запуск тестов
make rust-test

# Очистка
make rust-clean
```

### Запуск с валидацией

```bash
# Через Make (автоматически собирает Rust и Go)
make run-with-rust

# Или вручную (после сборки Rust)
go run -ldflags="-r rust_validator/target/release" ./cmd/collector \
  -id=node1 -endpoints=localhost:2379 -meters=50 -shards=5 -interval=10s -validate
```

При включённой валидации (`-validate`) каждое показание счётчика проверяется через Rust-библиотеку. В случае обнаружения некорректных данных в лог выводится предупреждение:

```
2026/05/28 14:15:33.456789 [node1] VALIDATION WARNING: meter=MTR-001 errors=["power_kw -1.234 out of range [0, 150]"]
```

Валидация не блокирует обработку данных — некорректные показания логируются как предупреждения, но не отбрасываются.

### Структура Rust-библиотеки

```
rust_validator/
├── Cargo.toml          # Манифест Rust-проекта
├── src/
│   └── lib.rs          # Основной код: функции валидации и C-compatible API
└── target/
    └── release/
        ├── rust_validator.dll      # Динамическая библиотека (Windows)
        ├── rust_validator.dll.lib  # Библиотека импорта (Windows)
        └── rust_validator.lib      # Статическая библиотека (Windows)
```

### C-compatible API

Библиотека экспортирует следующие C-функции для вызова из Go:

| Функция | Описание |
|---------|----------|
| `rust_validator_check_meter_id` | Проверка ID счётчика |
| `rust_validator_check_power` | Проверка мощности |
| `rust_validator_check_voltage` | Проверка напряжения |
| `rust_validator_check_current` | Проверка тока |
| `rust_validator_check_location` | Проверка локации |
| `rust_validator_check_timestamp` | Проверка временной метки |
| `rust_validator_check_reading` | Проверка полного показания (JSON) |
| `rust_validator_free_result` | Освобождение памяти результата |

## Параметры командной строки

### Collector (etcd-сборщик)

| Флаг | По умолчанию | Описание |
|------|-------------|----------|
| `-id` | `collector-{hostname}-{random}` | Уникальный ID инстанса |
| `-endpoints` | `localhost:2379` | etcd endpoints (через запятую) |
| `-meters` | `50` | Количество эмулированных счётчиков |
| `-shards` | `5` | Количество шардов |
| `-interval` | `10s` | Интервал сбора данных |
| `-agg-window` | `0` | Временное окно агрегации (например `30s`). Включает time-based tumbling window. |
| `-agg-count` | `0` | Окно агрегации по количеству записей (например `100`). Включает count-based tumbling window. |
| `-validate` | `false` | Включить Rust-валидацию данных (требует сборки с Rust-библиотекой) |

> **Примечание:** Флаги `-agg-window` и `-agg-count` взаимоисключающие. Если указаны оба, приоритет у `-agg-window`.

### Arrow Server

| Флаг | По умолчанию | Описание |
|------|-------------|----------|
| `-port` | `50051` | gRPC порт для Arrow Flight RPC |
| `-meters` | `50` | Количество эмулированных счётчиков |
| `-interval` | `10s` | Интервал генерации данных |

### Arrow Client (Python)

| Флаг | По умолчанию | Описание |
|------|-------------|----------|
| `--server` / `-s` | `localhost:50051` | Адрес Arrow Flight сервера |
| `--benchmark` / `-b` | `false` | Запустить бенчмарк производительности |
| `--iterations` / `-n` | `5` | Количество итераций бенчмарка |

## Пример вывода (сырой режим)

```
2026/05/28 14:15:23.123456 [node1] registered in etcd
2026/05/28 14:15:23.234567 [node1] created shard 1 with 10 meters
2026/05/28 14:15:23.234567 [node1] created shard 2 with 10 meters
2026/05/28 14:15:23.345678 [node1] rebalanced 5 shards across 3 collectors
2026/05/28 14:15:23.345678 [node1] collector node1 has 2 shards
2026/05/28 14:15:23.345678 [node1] collector node2 has 2 shards
2026/05/28 14:15:23.345678 [node1] collector node3 has 1 shards
2026/05/28 14:15:33.456789 [node1] DATA {"meter_id":"MTR-001","location":"Building A - Floor 1","timestamp":"...","power_kw":6.23,"voltage_v":224.5,"current_a":27.75}
2026/05/28 14:15:33.456789 [node1] collected 20 readings from 2 shards (total: 20)
```

## Структура проекта

```
├── cmd/
│   ├── collector/
│   │   └── main.go              # Точка входа etcd-сборщика
│   └── arrowserver/
│       └── main.go              # Точка входа Arrow Flight сервера
├── internal/
│   ├── source/
│   │   └── source.go            # Эмулятор счётчиков (с методом Validate())
│   ├── coordinator/
│   │   └── coordinator.go       # etcd-координация
│   ├── aggregator/
│   │   └── aggregator.go        # Оконная агрегация (tumbling window)
│   ├── collector/
│   │   └── collector.go         # Логика сборщика (с поддержкой валидации)
│   ├── validator/
│   │   └── validator.go         # Go-обёртка над Rust-библиотекой (cgo)
│   └── arrowserver/
│       └── server.go            # Apache Arrow Flight RPC сервер
├── rust_validator/
│   ├── Cargo.toml               # Манифест Rust-проекта
│   └── src/
│       └── lib.rs               # Rust-библиотека валидации
├── python/
│   ├── arrow_client.py          # Python-клиент для Arrow Flight
│   └── requirements.txt         # Python-зависимости
├── Makefile                     # Сборочные цели
├── go.mod / go.sum              # Go-модуль
└── README.md                    # Этот файл
```

## Команды Makefile

### Основные команды

| Команда | Описание |
|---------|----------|
| `make build` | Сборка бинарника collector |
| `make build-all` | Кросс-компиляция (linux/darwin/windows) |
| `make run` | Запуск одного сборщика (сырой режим) |
| `make run-node1/2/3` | Запуск конкретного узла (сырой режим) |
| `make run-all` | Запуск 3 узлов одновременно (сырой режим) |
| `make run-agg-time` | Запуск с time-based окном (30s) |
| `make run-agg-count` | Запуск с count-based окном (100 записей) |
| `make run-agg-time-node1/2/3` | Запуск узла с time-based окном |
| `make run-agg-time-all` | Запуск 3 узлов с time-based окном |

### Rust-валидация

| Команда | Описание |
|---------|----------|
| `make rust-build` | Сборка Rust-библиотеки валидации |
| `make rust-test` | Запуск тестов Rust-библиотеки |
| `make rust-clean` | Очистка артефактов Rust |
| `make build-with-rust` | Сборка Go-сборщика с Rust-валидацией |
| `make build-all-with-rust` | Кросс-компиляция с Rust-валидацией |
| `make run-with-rust` | Запуск сборщика с Rust-валидацией |

### Apache Arrow Flight RPC

| Команда | Описание |
|---------|----------|
| `make build-arrow` | Сборка Arrow Flight сервера |
| `make run-arrow` | Запуск Arrow Flight сервера (порт 50051) |
| `make run-arrow-client` | Запуск Python-клиента |
| `make run-arrow-bench` | Запуск Python-клиента с бенчмарком |
| `make pip-install` | Установка Python-зависимостей |

### Прочие

| Команда | Описание |
|---------|----------|
| `make docker-etcd` | Запуск etcd в Docker |
| `make clean` | Очистка артефактов сборки |
| `make test` | Запуск тестов |
| `make lint` | Запуск линтера |
| `make deps` | Обновление зависимостей |