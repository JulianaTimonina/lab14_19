#!/usr/bin/env python3
"""
Python-анализатор потоковых данных энергопотребления из Kafka.

Читает показания счётчиков из Kafka-топика, выполняет оконную обработку
(скользящее окно 5 минут) и выводит агрегированную статистику.

Usage:
    python kafka_analyzer.py --broker=localhost:9092 --topic=energy-readings
    python kafka_analyzer.py --broker=localhost:9092 --topic=energy-readings --window=300
"""

import argparse
import asyncio
import json
import logging
import signal
import sys
from collections import defaultdict
from datetime import datetime, timezone, timedelta
from typing import Dict, List, Optional, Tuple

try:
    from aiokafka import AIOKafkaConsumer
except ImportError:
    print("Please install aiokafka: pip install aiokafka")
    sys.exit(1)

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
    datefmt="%Y-%m-%d %H:%M:%S",
)
logger = logging.getLogger("kafka_analyzer")


class SlidingWindow:
    """
    Скользящее окно для агрегации показаний счётчиков.

    Хранит показания за последние `window_seconds` секунд и вычисляет
    агрегированную статистику (сумма, среднее, мин, макс) для каждого счётчика.
    """

    def __init__(self, window_seconds: int = 300):
        self.window_seconds = window_seconds
        # meter_id -> list of (timestamp, reading_dict)
        self.buckets: Dict[str, List[Tuple[datetime, dict]]] = defaultdict(list)
        self._total_readings = 0
        self._window_readings = 0

    def add(self, reading: dict):
        """Добавить показание в окно."""
        ts_str = reading.get("timestamp")
        if ts_str:
            try:
                ts = datetime.fromisoformat(ts_str)
            except (ValueError, TypeError):
                ts = datetime.now(timezone.utc)
        else:
            ts = datetime.now(timezone.utc)

        meter_id = reading.get("meter_id", "unknown")
        self.buckets[meter_id].append((ts, reading))
        self._total_readings += 1
        self._window_readings += 1

    def prune(self):
        """Удалить устаревшие показания из окна."""
        now = datetime.now(timezone.utc)
        cutoff = now - timedelta(seconds=self.window_seconds)
        pruned = 0
        for meter_id in list(self.buckets.keys()):
            original = len(self.buckets[meter_id])
            self.buckets[meter_id] = [
                (ts, r) for ts, r in self.buckets[meter_id] if ts >= cutoff
            ]
            pruned += original - len(self.buckets[meter_id])
            if not self.buckets[meter_id]:
                del self.buckets[meter_id]
        self._window_readings -= pruned
        return pruned

    def get_stats(self) -> Dict[str, dict]:
        """
        Получить агрегированную статистику по всем счётчикам в окне.

        Returns:
            dict: meter_id -> {
                "meter_id": str,
                "location": str,
                "count": int,
                "avg_power_kw": float,
                "min_power_kw": float,
                "max_power_kw": float,
                "avg_voltage_v": float,
                "avg_current_a": float,
                "window_start": str,
                "window_end": str,
            }
        """
        now = datetime.now(timezone.utc)
        window_start = now - timedelta(seconds=self.window_seconds)
        stats = {}

        for meter_id, readings in self.buckets.items():
            if not readings:
                continue

            powers = [r["power_kw"] for _, r in readings]
            voltages = [r["voltage_v"] for _, r in readings]
            currents = [r["current_a"] for _, r in readings]
            location = readings[0][1].get("location", "unknown")

            stats[meter_id] = {
                "meter_id": meter_id,
                "location": location,
                "count": len(powers),
                "avg_power_kw": round(sum(powers) / len(powers), 3),
                "min_power_kw": round(min(powers), 3),
                "max_power_kw": round(max(powers), 3),
                "avg_voltage_v": round(sum(voltages) / len(voltages), 2),
                "avg_current_a": round(sum(currents) / len(currents), 3),
                "window_start": window_start.isoformat(),
                "window_end": now.isoformat(),
            }

        return stats

    @property
    def total_readings(self) -> int:
        return self._total_readings

    @property
    def window_readings(self) -> int:
        return self._window_readings

    @property
    def meter_count(self) -> int:
        return len(self.buckets)


class KafkaAnalyzer:
    """Анализатор потоковых данных из Kafka."""

    def __init__(self, broker: str, topic: str, group_id: str,
                 window_seconds: int = 300, report_interval: int = 30):
        self.broker = broker
        self.topic = topic
        self.group_id = group_id
        self.window = SlidingWindow(window_seconds)
        self.report_interval = report_interval
        self.consumer: Optional[AIOKafkaConsumer] = None
        self.running = True
        self._read_count = 0

    async def start(self):
        """Запустить анализатор."""
        self.consumer = AIOKafkaConsumer(
            self.topic,
            bootstrap_servers=self.broker,
            group_id=self.group_id,
            value_deserializer=lambda v: json.loads(v.decode("utf-8")),
            auto_offset_reset="latest",
            enable_auto_commit=True,
        )

        await self.consumer.start()
        logger.info(f"Connected to Kafka: {self.broker}, topic: {self.topic}")
        logger.info(f"Sliding window: {self.window.window_seconds}s, report interval: {self.report_interval}s")

        # Запускаем фоновую задачу для периодического отчёта
        report_task = asyncio.create_task(self._report_loop())

        try:
            async for msg in self.consumer:
                if not self.running:
                    break
                reading = msg.value
                self.window.add(reading)
                self._read_count += 1

                if self._read_count % 1000 == 0:
                    logger.debug(f"Processed {self._read_count} readings, "
                                 f"window has {self.window.window_readings} active")
        finally:
            report_task.cancel()
            await self.consumer.stop()

    async def _report_loop(self):
        """Периодический вывод статистики."""
        while self.running:
            await asyncio.sleep(self.report_interval)
            self._print_report()

    def _print_report(self):
        """Вывести отчёт о состоянии окна."""
        pruned = self.window.prune()
        stats = self.window.get_stats()

        print(f"\n{'='*70}")
        print(f"📊 KAFKA ANALYZER — Sliding Window Report")
        print(f"{'='*70}")
        print(f"  Window:           {self.window.window_seconds}s")
        print(f"  Total processed:  {self.window.total_readings:,}")
        print(f"  Active in window: {self.window.window_readings:,}")
        print(f"  Pruned this cycle: {pruned}")
        print(f"  Active meters:    {self.window.meter_count}")
        print(f"{'='*70}")

        if stats:
            # Топ-5 счётчиков по средней мощности
            sorted_meters = sorted(stats.values(), key=lambda x: x["avg_power_kw"], reverse=True)
            print(f"\n  Top 5 meters by avg power:")
            print(f"  {'Meter ID':<12} {'Location':<30} {'Count':<8} {'Avg kW':<10} {'Min kW':<10} {'Max kW':<10}")
            print(f"  {'-'*80}")
            for s in sorted_meters[:5]:
                print(f"  {s['meter_id']:<12} {s['location']:<30} {s['count']:<8} "
                      f"{s['avg_power_kw']:<10.3f} {s['min_power_kw']:<10.3f} {s['max_power_kw']:<10.3f}")

            # Общая статистика
            all_powers = [s["avg_power_kw"] for s in stats.values()]
            print(f"\n  Overall:")
            print(f"    Meters in window:  {len(stats)}")
            print(f"    Avg power (avg):   {sum(all_powers)/len(all_powers):.3f} kW")
            print(f"    Min power (min):   {min(s['min_power_kw'] for s in stats.values()):.3f} kW")
            print(f"    Max power (max):   {max(s['max_power_kw'] for s in stats.values()):.3f} kW")
        else:
            print(f"\n  No data in current window.")

        print(f"{'='*70}\n")

    def stop(self):
        self.running = False


async def main():
    parser = argparse.ArgumentParser(description="Kafka Energy Data Analyzer")
    parser.add_argument("--broker", "-b", default="localhost:9092",
                        help="Kafka broker address")
    parser.add_argument("--topic", "-t", default="energy-readings",
                        help="Kafka topic name")
    parser.add_argument("--group-id", "-g", default="energy-analyzer",
                        help="Kafka consumer group ID")
    parser.add_argument("--window", "-w", type=int, default=300,
                        help="Sliding window size in seconds (default: 300 = 5 min)")
    parser.add_argument("--report-interval", "-r", type=int, default=30,
                        help="Report interval in seconds (default: 30)")
    args = parser.parse_args()

    analyzer = KafkaAnalyzer(
        broker=args.broker,
        topic=args.topic,
        group_id=args.group_id,
        window_seconds=args.window,
        report_interval=args.report_interval,
    )

    loop = asyncio.get_running_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        try:
            loop.add_signal_handler(sig, analyzer.stop)
        except NotImplementedError:
            pass

    print(f"\n{'='*70}")
    print(f"🔌 KAFKA ENERGY ANALYZER")
    print(f"{'='*70}")
    print(f"  Broker:           {args.broker}")
    print(f"  Topic:            {args.topic}")
    print(f"  Group ID:         {args.group_id}")
    print(f"  Sliding window:   {args.window}s ({args.window//60} min)")
    print(f"  Report interval:  {args.report_interval}s")
    print(f"{'='*70}\n")

    try:
        await analyzer.start()
    except KeyboardInterrupt:
        pass
    finally:
        analyzer.stop()
        logger.info("Analyzer stopped")


if __name__ == "__main__":
    asyncio.run(main())