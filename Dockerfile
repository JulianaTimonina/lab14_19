# Dockerfile для Go-сборщика данных энергопотребления
# Многоступенчатая сборка (multi-stage build)

# ---- Stage 1: Build ----
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app

# Копируем go.mod и go.sum для кэширования зависимостей
COPY go.mod go.sum ./
RUN go mod download

# Копируем исходный код
COPY . .

# Сборка collector (без Rust-валидации)
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/collector ./cmd/collector

# Сборка arrow-server
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/arrow-server ./cmd/arrowserver

# ---- Stage 2: Runtime ----
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

# Копируем бинарники из builder
COPY --from=builder /app/collector /usr/local/bin/collector
COPY --from=builder /app/arrow-server /usr/local/bin/arrow-server

# Создаём непривилегированного пользователя
RUN adduser -D -u 1001 energy
USER energy

# Порты
EXPOSE 50051

# По умолчанию запускаем collector
ENTRYPOINT ["collector"]
CMD ["-id=collector-1", "-endpoints=etcd:2379", "-meters=50", "-shards=5", "-interval=10s"]