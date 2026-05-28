# Тимонина Юлиана, группа 221131, вариант 19 (повышенная сложность)
# Energy Collector — Распределённый сборщик данных энергопотребления

Система для распределённого сбора данных с эмулированных счётчиков электроэнергии. Несколько экземпляров Go-сборщика работают параллельно, координируясь через **etcd** для распределения шардов/источников данных.

## Структура проекта

```
.
├── cmd/
│   ├── collector/              # Точка входа Go-сборщика
│   └── arrowserver/            # Точка входа Arrow Flight сервера
├── internal/
│   ├── aggregator/             # Tumbling window агрегация
│   │   ├── aggregator.go
│   │   └── aggregator_test.go  # 15 тестов
│   ├── arrowserver/            # Apache Arrow Flight RPC сервер
│   ├── collector/              # Логика сбора данных
│   ├── coordinator/            # etcd-координация
│   ├── kafkautil/              # Kafka producer/consumer
│   ├── source/                 # Эмуляция счётчиков
│   │   ├── source.go
│   │   └── source_test.go      # 13 тестов
│   └── validator/              # Go-обёртка Rust-валидатора
├── python/
│   ├── arrow_client.py         # Python Arrow Flight клиент
│   ├── async_collector.py      # Python asyncio сборщик
│   ├── kafka_analyzer.py       # Python Kafka анализатор
│   ├── benchmark_compare.py    # Сравнение Go vs Python
│   ├── dashboard.py            # Streamlit-дашборд
│   └── requirements.txt        # Python зависимости
├── rust_validator/             # Rust библиотека валидации
│   └── src/lib.rs              # 19 тестов
├── k8s/                        # Kubernetes манифесты
│   ├── namespace.yaml
│   ├── etcd-deployment.yaml
│   ├── kafka-deployment.yaml
│   ├── collector-deployment.yaml
│   ├── arrow-server-deployment.yaml
│   └── hpa.yaml
├── .streamlit/
│   └── config.toml             # Конфигурация Streamlit (headless)
├── Dockerfile                  # Dockerfile для Go-сборщика
├── Makefile                    # Цели сборки и запуска
├── go.mod / go.sum
├── PROMPT_LOG.md               # Лог промптов для воспроизведения
└── README.md
```

## Компоненты

| Компонент | Описание |
|-----------|----------|
| [`internal/source/source.go`](internal/source/source.go) | Эмулятор счётчиков электроэнергии. Генерирует показания: мощность (кВт), напряжение (В), ток (А) с флуктуациями. |
| [`internal/coordinator/coordinator.go`](internal/coordinator/coordinator.go) | etcd-координация: регистрация инстансов, лидерство, ребалансировка шардов. |
| [`internal/aggregator/aggregator.go`](internal/aggregator/aggregator.go) | Оконная агрегация (tumbling window): time-based (каждые N сек) и count-based (каждые M записей). |
| [`internal/collector/collector.go`](internal/collector/collector.go) | Сборщик: получает шарды из etcd, читает показания, агрегирует, выводит в лог или Kafka. |
| [`internal/kafkautil/kafka.go`](internal/kafkautil/kafka.go) | Kafka producer/consumer для потоковой передачи показаний. |
| [`internal/arrowserver/server.go`](internal/arrowserver/server.go) | Apache Arrow Flight RPC сервер, отдающий данные в колоночном формате. |
| [`internal/validator/validator.go`](internal/validator/validator.go) | Go-обёртка над Rust-библиотекой валидации через cgo. |
| [`python/arrow_client.py`](python/arrow_client.py) | Python-клиент для Arrow Flight RPC со статистикой и бенчмаркингом. |
| [`python/async_collector.py`](python/async_collector.py) | Python-сборщик на asyncio/aiohttp с HTTP и Kafka-выводом. |
| [`python/kafka_analyzer.py`](python/kafka_analyzer.py) | Python-анализатор Kafka со скользящим окном (5 мин). |
| [`python/benchmark_compare.py`](python/benchmark_compare.py) | Сравнение Go vs Python: время, память, CPU, графики. |
| [`python/dashboard.py`](python/dashboard.py) | Streamlit-дашборд с Plotly-графиками и автообновлением. |
| [`rust_validator/src/lib.rs`](rust_validator/src/lib.rs) | Rust-библиотека валидации с C-compatible API. Проверяет ID, мощность, напряжение, ток, timestamp, локацию. |

## Требования

- Go 1.21+, etcd, Python 3.8+, Kafka 3.x (опционально)
- Rust toolchain + GCC (MinGW на Windows) — только для сборки с валидацией
- Docker, kubectl + minikube/k3s (для K8s)

## Быстрый старт

```bash
# 1. Запуск etcd
make docker-etcd

# 2. Сборка
make build                    # без валидации
make build-with-rust          # с Rust-валидацией

# 3. Запуск сборщиков (3 терминала или одной командой)
make run-node1   # терминал 1
make run-node2   # терминал 2
make run-node3   # терминал 3
# или
make run-all
```

### Ручной запуск

```bash
go run ./cmd/collector -id=node1 -endpoints=localhost:2379 -meters=100 -shards=10 -interval=5s
```

## Потоковая передача через Kafka

```bash
# Go-сборщик → Kafka
make run-kafka-collector

# Python-анализатор ← Kafka
make run-kafka-analyzer
```

Анализатор читает показания из Kafka-топика и выводит агрегированную статистику за скользящее окно (5 мин) каждые 30 секунд.

## Apache Arrow Flight RPC

```bash
# Сервер
make run-arrow

# Python-клиент
make run-arrow-client

# Бенчмарк (5 итераций)
make run-arrow-bench
```

## Streamlit-дашборд

```bash
make run-dashboard
# или: streamlit run python/dashboard.py -- --meters=50 --interval=5 --window=300
```

Дашборд доступен по адресу `http://localhost:8501`. Автообновление через `<meta http-equiv="refresh">`.

Параметры: `--meters` (10–200), `--interval` (1–60 с), `--window` (10–600 с).

## Сравнение Go vs Python

```bash
make run-python-bench
# или: python python/benchmark_compare.py --meters=50 --cycles=10 --output=./benchmark_results
```

Измеряет: среднее время цикла, пропускную способность, память, CPU. Результаты в JSON и PNG.

## Оконная агрегация

```bash
# Time-based (каждые 30 с)
go run ./cmd/collector -id=node1 -endpoints=localhost:2379 -meters=50 -shards=5 -interval=10s -agg-window=30s

# Count-based (каждые 100 записей)
go run ./cmd/collector -id=node1 -endpoints=localhost:2379 -meters=50 -shards=5 -interval=10s -agg-count=100
```

## Развёртывание в Kubernetes

```bash
make docker-build     # сборка образа
make k8s-apply        # развёртывание
make k8s-status       # проверка
make k8s-hpa          # мониторинг HPA
make k8s-delete       # удаление
```

### Компоненты K8s

| Ресурс | Описание |
|--------|----------|
| [`k8s/namespace.yaml`](k8s/namespace.yaml) | Namespace `energy-system` |
| [`k8s/etcd-deployment.yaml`](k8s/etcd-deployment.yaml) | etcd для координации |
| [`k8s/kafka-deployment.yaml`](k8s/kafka-deployment.yaml) | Kafka + Zookeeper |
| [`k8s/collector-deployment.yaml`](k8s/collector-deployment.yaml) | Go-сборщик (2 реплики) |
| [`k8s/arrow-server-deployment.yaml`](k8s/arrow-server-deployment.yaml) | Arrow Flight сервер |
| [`k8s/hpa.yaml`](k8s/hpa.yaml) | HPA: CPU 50%, Memory 70%, 1–10 реплик |

## CLI-флаги

### Collector

| Флаг | По умолчанию | Описание |
|------|-------------|----------|
| `-id` | auto | ID инстанса |
| `-endpoints` | `localhost:2379` | etcd endpoints |
| `-meters` | `50` | Количество счётчиков |
| `-shards` | `5` | Количество шардов |
| `-interval` | `10s` | Интервал сбора |
| `-agg-window` | `0` | Временное окно агрегации |
| `-agg-count` | `0` | Окно по количеству записей |
| `-validate` | `false` | Rust-валидация |
| `-kafka` | `false` | Kafka-вывод |
| `-kafka-brokers` | `localhost:9092` | Kafka brokers |
| `-kafka-topic` | `energy-readings` | Kafka топик |

### Arrow Server

| Флаг | По умолчанию | Описание |
|------|-------------|----------|
| `-port` | `50051` | gRPC порт |
| `-meters` | `50` | Количество счётчиков |
| `-interval` | `10s` | Интервал генерации |