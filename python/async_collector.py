#!/usr/bin/env python3
"""
Python-сборщик данных энергопотребления (asyncio/aiohttp).

Эмулирует счётчики электроэнергии и отправляет показания через HTTP
на Kafka REST Proxy или напрямую в Kafka. Предназначен для сравнения
производительности с Go-версией сборщика.

Usage:
    python async_collector.py --meters=50 --interval=5 --kafka-broker=localhost:9092
    python async_collector.py --meters=50 --interval=5 --http-url=http://localhost:8080/api/readings
"""

import argparse
import asyncio
import json
import logging
import random
import signal
import sys
import time
from dataclasses import dataclass, asdict
from datetime import datetime, timezone
from typing import List, Optional

try:
    import aiohttp
except ImportError:
    print("Please install aiohttp: pip install aiohttp")
    sys.exit(1)

try:
    from aiokafka import AIOKafkaProducer
except ImportError:
    AIOKafkaProducer = None

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
    datefmt="%Y-%m-%d %H:%M:%S",
)
logger = logging.getLogger("async_collector")


LOCATIONS = [
    "Building A - Floor 1",
    "Building A - Floor 2",
    "Building B - Server Room",
    "Building B - Lobby",
    "Building C - Workshop",
    "Building C - Office",
    "Building D - Warehouse",
    "Building D - Charging Station",
]


@dataclass
class Meter:
    """Эмулируемый счётчик электроэнергии."""
    id: str
    location: str
    base_load: float
    fluctuation: float


@dataclass
class Reading:
    """Показание счётчика."""
    meter_id: str
    location: str
    timestamp: str  # ISO format
    power_kw: float
    voltage_v: float
    current_a: float


class MeterSource:
    """Источник данных — эмуляция счётчиков."""

    def __init__(self, count: int):
        self.meters: List[Meter] = []
        for i in range(count):
            loc = LOCATIONS[i % len(LOCATIONS)]
            self.meters.append(Meter(
                id=f"MTR-{i+1:03d}",
                location=loc,
                base_load=5.0 + float(i) * 1.5,
                fluctuation=2.0 + float(i) * 0.3,
            ))
        self.rng = random.Random(time.time_ns())

    def read_all(self) -> List[Reading]:
        """Сгенерировать показания всех счётчиков."""
        now = datetime.now(timezone.utc).isoformat()
        readings = []
        for m in self.meters:
            fluctuation = (self.rng.random() * 2 - 1) * m.fluctuation
            power_kw = max(0, m.base_load + fluctuation)
            voltage_v = 220.0 + (self.rng.random() * 2 - 1) * 10.0
            current_a = (power_kw * 1000) / voltage_v if voltage_v > 0 else 0

            readings.append(Reading(
                meter_id=m.id,
                location=m.location,
                timestamp=now,
                power_kw=round(power_kw, 3),
                voltage_v=round(voltage_v, 2),
                current_a=round(current_a, 3),
            ))
        return readings


class StatsCollector:
    """Сбор статистики производительности."""

    def __init__(self):
        self.readings_count = 0
        self.total_time = 0.0
        self.start_time = time.monotonic()
        self.cycle_times: List[float] = []

    def record_cycle(self, count: int, elapsed: float):
        self.readings_count += count
        self.total_time += elapsed
        self.cycle_times.append(elapsed)

    def report(self) -> dict:
        elapsed = time.monotonic() - self.start_time
        avg_cycle = sum(self.cycle_times) / len(self.cycle_times) if self.cycle_times else 0
        return {
            "total_readings": self.readings_count,
            "total_time_sec": round(elapsed, 2),
            "cycles": len(self.cycle_times),
            "avg_cycle_time_ms": round(avg_cycle * 1000, 2),
            "readings_per_sec": round(self.readings_count / elapsed, 2) if elapsed > 0 else 0,
        }


class BaseCollector:
    """Базовый класс сборщика."""

    def __init__(self, source: MeterSource, interval: float, stats: StatsCollector):
        self.source = source
        self.interval = interval
        self.stats = stats
        self.running = True

    async def collect_cycle(self) -> List[Reading]:
        """Один цикл сбора данных."""
        readings = self.source.read_all()
        return readings

    async def send_readings(self, readings: List[Reading]):
        """Отправить показания. Должен быть переопределён."""
        raise NotImplementedError

    async def run(self):
        """Основной цикл сбора."""
        logger.info(f"Starting collector with {len(self.source.meters)} meters, interval={self.interval}s")
        while self.running:
            cycle_start = time.perf_counter()
            try:
                readings = await self.collect_cycle()
                await self.send_readings(readings)
                elapsed = time.perf_counter() - cycle_start
                self.stats.record_cycle(len(readings), elapsed)
                logger.debug(f"Cycle: {len(readings)} readings in {elapsed*1000:.1f}ms")
            except Exception as e:
                logger.error(f"Cycle error: {e}")

            await asyncio.sleep(self.interval)

    def stop(self):
        self.running = False


class HTTPCollector(BaseCollector):
    """Сборщик, отправляющий данные через HTTP."""

    def __init__(self, source: MeterSource, interval: float, stats: StatsCollector,
                 http_url: str, batch_size: int = 50):
        super().__init__(source, interval, stats)
        self.http_url = http_url
        self.batch_size = batch_size

    async def send_readings(self, readings: List[Reading]):
        async with aiohttp.ClientSession() as session:
            # Отправляем батчами
            for i in range(0, len(readings), self.batch_size):
                batch = readings[i:i + self.batch_size]
                data = [asdict(r) for r in batch]
                async with session.post(
                    self.http_url,
                    json=data,
                    headers={"Content-Type": "application/json"},
                    timeout=aiohttp.ClientTimeout(total=10),
                ) as resp:
                    if resp.status >= 400:
                        text = await resp.text()
                        logger.warning(f"HTTP {resp.status}: {text[:200]}")


class KafkaCollector(BaseCollector):
    """Сборщик, отправляющий данные в Kafka."""

    def __init__(self, source: MeterSource, interval: float, stats: StatsCollector,
                 kafka_broker: str, topic: str = "energy-readings"):
        super().__init__(source, interval, stats)
        self.kafka_broker = kafka_broker
        self.topic = topic
        self.producer: Optional[AIOKafkaProducer] = None

    async def send_readings(self, readings: List[Reading]):
        if self.producer is None:
            self.producer = AIOKafkaProducer(
                bootstrap_servers=self.kafka_broker,
                value_serializer=lambda v: json.dumps(v).encode("utf-8"),
            )
            await self.producer.start()

        for r in readings:
            key = r.meter_id.encode("utf-8")
            value = asdict(r)
            await self.producer.send(self.topic, key=key, value=value)

    async def run(self):
        try:
            await super().run()
        finally:
            if self.producer:
                await self.producer.stop()


async def main():
    parser = argparse.ArgumentParser(description="Python Async Energy Collector")
    parser.add_argument("--meters", type=int, default=50, help="Number of simulated meters")
    parser.add_argument("--interval", type=float, default=5.0, help="Collection interval in seconds")
    parser.add_argument("--http-url", type=str, default="", help="HTTP endpoint for readings")
    parser.add_argument("--kafka-broker", type=str, default="", help="Kafka broker address")
    parser.add_argument("--kafka-topic", type=str, default="energy-readings", help="Kafka topic name")
    parser.add_argument("--duration", type=int, default=0, help="Duration in seconds (0 = infinite)")
    parser.add_argument("--benchmark", action="store_true", help="Run benchmark mode")
    parser.add_argument("--benchmark-cycles", type=int, default=10, help="Benchmark cycles")
    args = parser.parse_args()

    source = MeterSource(args.meters)
    stats = StatsCollector()

    if args.kafka_broker and AIOKafkaProducer is not None:
        collector = KafkaCollector(source, args.interval, stats, args.kafka_broker, args.kafka_topic)
        logger.info(f"Kafka mode: broker={args.kafka_broker}, topic={args.kafka_topic}")
    elif args.http_url:
        collector = HTTPCollector(source, args.interval, stats, args.http_url)
        logger.info(f"HTTP mode: url={args.http_url}")
    else:
        # Режим только для бенчмарка (без отправки)
        collector = BaseCollector(source, args.interval, stats)
        logger.info("Benchmark-only mode (no output)")

    if args.benchmark:
        logger.info(f"Benchmark mode: {args.benchmark_cycles} cycles")
        for i in range(args.benchmark_cycles):
            cycle_start = time.perf_counter()
            readings = await collector.collect_cycle()
            elapsed = time.perf_counter() - cycle_start
            stats.record_cycle(len(readings), elapsed)
            logger.info(f"  Cycle {i+1}: {len(readings)} readings in {elapsed*1000:.1f}ms")
    else:
        # Запуск в штатном режиме
        loop = asyncio.get_running_loop()
        for sig in (signal.SIGINT, signal.SIGTERM):
            try:
                loop.add_signal_handler(sig, collector.stop)
            except NotImplementedError:
                # Windows не поддерживает add_signal_handler
                pass

        task = asyncio.create_task(collector.run())

        if args.duration > 0:
            await asyncio.sleep(args.duration)
            collector.stop()
        else:
            await task

    # Отчёт
    report = stats.report()
    print(f"\n{'='*60}")
    print(f"📊 PYTHON COLLECTOR PERFORMANCE")
    print(f"{'='*60}")
    print(f"  Meters:           {args.meters}")
    print(f"  Interval:         {args.interval}s")
    print(f"  Total readings:   {report['total_readings']:,}")
    print(f"  Total time:       {report['total_time_sec']}s")
    print(f"  Cycles:           {report['cycles']}")
    print(f"  Avg cycle time:   {report['avg_cycle_time_ms']}ms")
    print(f"  Throughput:       {report['readings_per_sec']} readings/sec")
    print(f"{'='*60}\n")


if __name__ == "__main__":
    asyncio.run(main())