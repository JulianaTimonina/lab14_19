# Energy Collector — Распределённый сборщик данных энергопотребления

Система для распределённого сбора данных с эмулированных счётчиков электроэнергии. Несколько экземпляров Go-сборщика работают параллельно, координируясь через **etcd** для распределения шардов/источников данных.

Поддерживает **оконную агрегацию (tumbling window)** — сырые показания накапливаются на стороне Go и отправляются агрегированными (суммы, средние, минимум/максимум), что снижает объём передаваемых данных.

Данные также доступны через **Apache Arrow Flight RPC** — высокопроизводительный протокол передачи колоночных данных. Go-сервер отдаёт показания в формате Arrow RecordBatch, а Python-клиент принимает и анализирует их.

## Потоковая передача через Kafka

Go-сборщик может отправлять показания в **Kafka**-топик, а Python-анализатор читает их оттуда и выполняет **оконную обработку (скользящее окно 5 минут)** в реальном времени.

## Развёртывание в Kubernetes

Go-сборщик упакован в Docker-образ. Конвейер разворачивается в **minikube/k3s** с **HPA (Horizontal Pod Autoscaler)** на основе загрузки CPU и памяти.

## Сравнение производительности Go vs Python

Реализованы бенчмарки для сравнения скорости сбора, потребления памяти и CPU между Go- и Python-версиями при одинаковой нагрузке. Результаты оформляются в виде отчёта с графиками.

## Архитектура

```
┌─────────────────────────────────────────────────────────────────────┐
│                           etcd                                      │
│            (координация, лидерство, распределение шардов)            │
└──────┬──────────────┬──────────────┬────────────────────────────────┘
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

         ┌──────────────────┐     ┌──────────────────┐
         │  Go Collector    │────►│     Kafka        │
         │  (Kafka output)  │     │  energy-readings │
         └──────────────────┘     └────────┬─────────┘
                                           │
                                    ┌──────▼──────────┐
                                    │ Python Analyzer │
                                    │ (sliding window │
                                    │  5 min)         │
                                    └─────────────────┘
```

### Компоненты

- **Source** ([`internal/source/source.go`](internal/source/source.go)) — эмулятор счётчиков электроэнергии. Генерирует реалистичные показания: мощность (кВт), напряжение (В), ток (А) с флуктуациями.
- **Coordinator** ([`internal/coordinator/coordinator.go`](internal/coordinator/coordinator.go)) — etcd-координация: регистрация инстансов, лидерство, создание и ребалансировка шардов.
- **Aggregator** ([`internal/aggregator/aggregator.go`](internal/aggregator/aggregator.go)) — оконная агрегация (tumbling window). Поддерживает два режима: временное окно (каждые N секунд) и окно по количеству записей (каждые M записей на счётчик).
- **Collector** ([`internal/collector/collector.go`](internal/collector/collector.go)) — сборщик данных: получает назначенные шарды из etcd, читает показания с эмулятора, передаёт в агрегатор (если включён) и выводит результат в лог или Kafka.
- **Kafka Producer** ([`internal/kafkautil/kafka.go`](internal/kafkautil/kafka.go)) — отправляет показания счётчиков в Kafka-топик для потоковой обработки.
- **Arrow Server** ([`internal/arrowserver/server.go`](internal/arrowserver/server.go)) — Apache Arrow Flight RPC сервер, отдающий показания счётчиков в колоночном формате Arrow.
- **Arrow Client** ([`python/arrow_client.py`](python/arrow_client.py)) — Python-клиент для получения данных через Arrow Flight RPC с выводом статистики и бенчмаркингом.
- **Async Collector** ([`python/async_collector.py`](python/async_collector.py)) — Python-сборщик данных на asyncio/aiohttp. Эмулирует счётчики и отправляет показания через HTTP или Kafka.
- **Kafka Analyzer** ([`python/kafka_analyzer.py`](python/kafka_analyzer.py)) — Python-анализатор потоковых данных из Kafka со скользящим окном (5 минут по умолчанию).
- **Benchmark Compare** ([`python/benchmark_compare.py`](python/benchmark_compare.py)) — скрипт сравнения производительности Go vs Python с построением графиков.
- **Main** ([`cmd/collector/main.go`](cmd/collector/main.go)) — точка входа для etcd-сборщика с CLI-флагами.
- **Arrow Server Main** ([`cmd/arrowserver/main.go`](cmd/arrowserver/main.go)) — точка входа для Arrow Flight сервера.
- **Rust Validator** ([`rust_validator/`](rust_validator/)) — библиотека на Rust для валидации данных энергопотребления. Проверяет формат ID счётчика, диапазоны значений (мощность, напряжение, ток), корректность временных меток и локаций. Интегрирована в Go-сборщик через cgo.
- **Go Validator Wrapper** ([`internal/validator/validator.go`](internal/validator/validator.go)) — Go-обёртка над Rust-библиотекой валидации через cgo.

## Требования

- Go 1.21+
- etcd (локально или в Docker)
- Python 3.8+ (для Arrow клиента, Kafka анализатора, бенчмарков)
- Kafka 3.x (для потоковой передачи данных)
- Rust toolchain (cargo, rustc) — только для сборки с валидацией
- Make (опционально)
- Docker (для сборки образа и Kubernetes)
- kubectl + minikube/k3s (для развёртывания в Kubernetes)
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

### 2. Запуск Kafka (опционально, для потоковой передачи)

```bash
make docker-kafka
```

### 3. Сборка

**Без валидации (обычная сборка):**
```bash
make build
```

**С Rust-валидацией данных:**
```bash
make build-with-rust
```

> **Примечание:** Для сборки с Rust-валидацией требуется установленный Rust toolchain и GCC (MinGW на Windows). На Windows также требуется скопировать `rust_validator.dll` в директорию с исполняемым файлом или в `C:\rust_lib\`.

### 4. Запуск нескольких сборщиков

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

### 5. Ручной запуск с параметрами

```bash
go run ./cmd/collector \
  -id=custom-node \
  -endpoints=localhost:2379 \
  -meters=100 \
  -shards=10 \
  -interval=5s
```

## Потоковая передача через Kafka

### Запуск Go-сборщика с Kafka-выводом

```bash
# Требуется: etcd + Kafka
make run-kafka-collector
```

Или вручную:
```bash
go run ./cmd/collector \
  -id=kafka-node1 \
  -endpoints=localhost:2379 \
  -meters=50 \
  -shards=5 \
  -interval=10s \
  -kafka \
  -kafka-brokers=localhost:9092 \
  -kafka-topic=energy-readings
```

### Запуск Python-анализатора Kafka

```bash
make run-kafka-analyzer
```

Или вручную:
```bash
python python/kafka_analyzer.py \
  --broker=localhost:9092 \
  --topic=energy-readings \
  --window=300
```

Анализатор читает показания из Kafka-топика и выводит агрегированную статистику за скользящее окно (по умолчанию 5 минут) каждые 30 секунд.

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
```

## Сравнение производительности Go vs Python

### Запуск бенчмарка

```bash
# Установка зависимостей
pip install -r python/requirements.txt

# Запуск сравнения (50 счётчиков, 10 циклов)
make run-python-bench

# Или вручную с настройкой параметров
python python/benchmark_compare.py --meters=50 --cycles=10 --output=./benchmark_results

# Только Python-бенчмарк
python python/benchmark_compare.py --skip-go --meters=100 --cycles=20

# Только Go-бенчмарк
python python/benchmark_compare.py --skip-python --meters=100 --cycles=20
```

### Результаты

Бенчмарк измеряет:
- **Среднее время цикла** (ms) — время генерации показаний всех счётчиков
- **Пропускная способность** (readings/sec) — количество показаний в секунду
- **Потребление памяти** (MB) — прирост памяти во время работы
- **Загрузка CPU** (%) — средняя загрузка процессора

Результаты сохраняются в JSON и в виде графиков (PNG) в директорию `./benchmark_results/`.

### Пример графика

После запуска бенчмарка графики сохраняются в `./benchmark_results/benchmark_comparison.png`:

![Benchmark Comparison](benchmark_results/benchmark_comparison.png)

## Python-сборщик данных

### Запуск в режиме бенчмарка (без отправки)

```bash
make run-python-collector
```

### Запуск с отправкой через HTTP

```bash
python python/async_collector.py \
  --meters=50 \
  --interval=5 \
  --http-url=http://localhost:8080/api/readings
```

### Запуск с отправкой в Kafka

```bash
python python/async_collector.py \
  --meters=50 \
  --interval=5 \
  --kafka-broker=localhost:9092 \
  --kafka-topic=energy-readings
```

## Развёртывание в Kubernetes

### Сборка Docker-образа

```bash
make docker-build
```

### Развёртывание в minikube/k3s

```bash
# Применить все манифесты
make k8s-apply

# Проверить статус
make k8s-status

# Отслеживать HPA
make k8s-hpa
```

### Удаление

```bash
make k8s-delete
```

### Компоненты Kubernetes

| Ресурс | Описание |
|--------|----------|
| [`k8s/namespace.yaml`](k8s/namespace.yaml) | Namespace `energy-system` |
| [`k8s/etcd-deployment.yaml`](k8s/etcd-deployment.yaml) | etcd для координации сборщиков |
| [`k8s/kafka-deployment.yaml`](k8s/kafka-deployment.yaml) | Kafka + Zookeeper для потоковой передачи |
| [`k8s/collector-deployment.yaml`](k8s/collector-deployment.yaml) | Go-сборщик (2 реплики) с Kafka-выводом |
| [`k8s/arrow-server-deployment.yaml`](k8s/arrow-server-deployment.yaml) | Arrow Flight сервер |
| [`k8s/hpa.yaml`](k8s/hpa.yaml) | HPA: автоскалирование по CPU (50%) и памяти (70%) |

### HPA (Horizontal Pod Autoscaler)

HPA настроен на:
- **CPU**: масштабирование при утилизации > 50%
- **Memory**: масштабирование при утилизации > 70%
- **Min replicas**: 1
- **Max replicas**: 10
- **Stabilization window**: 60s для scale-down, 30s для scale-up

## Оконная агрегация (Tumbling Window)

### Временное окно

```bash
go run ./cmd/collector -id=node1 -endpoints=localhost:2379 -meters=50 -shards=5 -interval=10s -agg-window=30s
```

### Окно по количеству записей

```bash
go run ./cmd/collector -id=node1 -endpoints=localhost:2379 -meters=50 -shards=5 -interval=10s -agg-count=100
```

## CLI-флаги

### Collector (`cmd/collector/main.go`)

| Флаг | По умолчанию | Описание |
|------|-------------|----------|
| `-id` | auto | Уникальный ID инстанса сборщика |
| `-endpoints` | `localhost:2379` | etcd endpoints (через запятую) |
| `-meters` | `50` | Количество эмулированных счётчиков |
| `-shards` | `5` | Количество шардов |
| `-interval` | `10s` | Интервал сбора данных |
| `-agg-window` | `0` | Временное окно агрегации (например, `30s`) |
| `-agg-count` | `0` | Окно агрегации по количеству записей |
| `-validate` | `false` | Включить Rust-валидацию данных |
| `-kafka` | `false` | Включить Kafka-вывод |
| `-kafka-brokers` | `localhost:9092` | Kafka broker адреса (через запятую) |
| `-kafka-topic` | `energy-readings` | Kafka топик |

### Arrow Server (`cmd/arrowserver/main.go`)

| Флаг | По умолчанию | Описание |
|------|-------------|----------|
| `-port` | `50051` | gRPC порт для Arrow Flight RPC |
| `-meters` | `50` | Количество эмулированных счётчиков |
| `-interval` | `10s` | Интервал генерации данных |

## Структура проекта

```
.
├── cmd/
│   ├── collector/          # Точка входа Go-сборщика
│   └── arrowserver/        # Точка входа Arrow Flight сервера
├── internal/
│   ├── aggregator/         # Tumbling window агрегация
│   ├── arrowserver/        # Apache Arrow Flight RPC сервер
│   ├── collector/          # Логика сбора данных
│   ├── coordinator/        # etcd-координация
│   ├── kafkautil/          # Kafka producer/consumer
│   ├── source/             # Эмуляция счётчиков
│   └── validator/          # Go-обёртка Rust-валидатора
├── python/
│   ├── arrow_client.py     # Python Arrow Flight клиент
│   ├── async_collector.py  # Python asyncio сборщик
│   ├── kafka_analyzer.py   # Python Kafka анализатор
│   ├── benchmark_compare.py# Сравнение Go vs Python
│   └── requirements.txt    # Python зависимости
├── rust_validator/         # Rust библиотека валидации
├── k8s/                    # Kubernetes манифесты
│   ├── namespace.yaml
│   ├── etcd-deployment.yaml
│   ├── kafka-deployment.yaml
│   ├── collector-deployment.yaml
│   ├── arrow-server-deployment.yaml
│   └── hpa.yaml
├── Dockerfile              # Dockerfile для Go-сборщика
├── Makefile                # Цели сборки и запуска
├── go.mod
└── go.sum