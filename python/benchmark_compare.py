#!/usr/bin/env python3
"""
Сравнение производительности Go vs Python для сбора данных энергопотребления.

Запускает Go-сборщик и Python-сборщик с одинаковыми параметрами,
измеряет скорость сбора, потребление памяти и CPU, и строит графики.

Usage:
    python benchmark_compare.py --meters=50 --cycles=10
    python benchmark_compare.py --meters=100 --cycles=20 --output=./results
"""

import argparse
import asyncio
import json
import logging
import os
import subprocess
import sys
import time
from dataclasses import dataclass, asdict, field
from typing import List, Optional

try:
    import psutil
except ImportError:
    psutil = None
    print("Warning: psutil not installed. Memory/CPU stats will be limited.")

try:
    import matplotlib
    matplotlib.use("Agg")  # Non-interactive backend
    import matplotlib.pyplot as plt
except ImportError:
    plt = None
    print("Warning: matplotlib not installed. Graphs will not be generated.")

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
    datefmt="%Y-%m-%d %H:%M:%S",
)
logger = logging.getLogger("benchmark")


@dataclass
class BenchmarkResult:
    """Результаты одного прогона бенчмарка."""
    language: str
    meters: int
    cycles: int
    cycle_times_ms: List[float] = field(default_factory=list)
    memory_mb: float = 0.0
    cpu_percent: float = 0.0
    total_readings: int = 0
    total_time_sec: float = 0.0


def measure_process(pid: int, duration: float) -> tuple:
    """Измерить потребление памяти и CPU процесса."""
    if psutil is None:
        return 0.0, 0.0

    try:
        proc = psutil.Process(pid)
        cpu_samples = []
        mem_samples = []
        start = time.time()
        while time.time() - start < duration:
            try:
                cpu_samples.append(proc.cpu_percent(interval=0.5))
                mem_samples.append(proc.memory_info().rss / 1024 / 1024)
            except (psutil.NoSuchProcess, psutil.AccessDenied):
                break
        avg_mem = sum(mem_samples) / len(mem_samples) if mem_samples else 0
        avg_cpu = sum(cpu_samples) / len(cpu_samples) if cpu_samples else 0
        return avg_mem, avg_cpu
    except Exception as e:
        logger.warning(f"Failed to measure process {pid}: {e}")
        return 0.0, 0.0


def run_go_benchmark(meters: int, cycles: int) -> BenchmarkResult:
    """
    Запустить Go-сборщик в режиме бенчмарка (без etcd, только сбор данных).

    Создаёт временную Go-программу для бенчмарка, которая использует
    тот же internal/source пакет, что и основной сборщик.
    """
    result = BenchmarkResult(language="Go", meters=meters, cycles=cycles)

    # Создаём временный Go-бенчмарк
    bench_code = f'''package main

import (
    "fmt"
    "time"
    "github.com/yliana-efimova/energy-collector/internal/source"
)

func main() {{
    s := source.New({meters})
    meterIDs := make([]string, {meters})
    for i, m := range s.GetMeters() {{
        meterIDs[i] = m.ID
    }}

    var totalTime time.Duration
    for i := 0; i < {cycles}; i++ {{
        start := time.Now()
        readings := s.ReadMeters(meterIDs)
        elapsed := time.Since(start)
        totalTime += elapsed
        fmt.Printf("CYCLE %d: %d readings in %v\\n", i+1, len(readings), elapsed)
    }}

    avg := totalTime / {cycles}
    fmt.Printf("TOTAL: %d cycles, %v total, %v avg\\n", {cycles}, totalTime, avg)
}}
'''

    bench_file = "_go_bench_main.go"
    try:
        with open(bench_file, "w") as f:
            f.write(bench_code)

        # Компилируем
        logger.info("Compiling Go benchmark...")
        compile_start = time.time()
        subprocess.run(
            ["go", "build", "-o", "_go_bench.exe", bench_file],
            capture_output=True, text=True, check=True,
            cwd=os.path.dirname(os.path.abspath(__file__)) or ".",
        )
        compile_time = time.time() - compile_start
        logger.info(f"Go benchmark compiled in {compile_time:.2f}s")

        # Запускаем и измеряем
        logger.info(f"Running Go benchmark ({cycles} cycles, {meters} meters)...")
        start = time.time()
        proc = subprocess.Popen(
            ["./_go_bench.exe"],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )

        # Измеряем ресурсы в фоне
        if psutil:
            mem, cpu = measure_process(proc.pid, 5.0)
            result.memory_mb = mem
            result.cpu_percent = cpu

        stdout, stderr = proc.communicate()
        elapsed = time.time() - start
        result.total_time_sec = elapsed

        if stderr:
            logger.warning(f"Go benchmark stderr: {stderr}")

        # Парсим вывод
        for line in stdout.strip().split("\n"):
            line = line.strip()
            if line.startswith("CYCLE"):
                parts = line.split()
                # CYCLE 1: 50 readings in 42.3µs
                time_str = parts[-1].rstrip("sµ")
                try:
                    if "µs" in parts[-1]:
                        ms = float(time_str) / 1000
                    elif "ms" in parts[-1]:
                        ms = float(time_str)
                    else:
                        ms = float(time_str) * 1000
                    result.cycle_times_ms.append(ms)
                except ValueError:
                    pass
            elif line.startswith("TOTAL"):
                pass

        result.total_readings = meters * cycles
        logger.info(f"Go benchmark completed in {elapsed:.2f}s")

    except subprocess.CalledProcessError as e:
        logger.error(f"Go benchmark failed: {e}")
        logger.error(f"stdout: {e.stdout}")
        logger.error(f"stderr: {e.stderr}")
    except Exception as e:
        logger.error(f"Go benchmark error: {e}")
    finally:
        # Очистка
        for f in [bench_file, "_go_bench.exe"]:
            if os.path.exists(f):
                try:
                    os.remove(f)
                except OSError:
                    pass

    return result


async def run_python_benchmark(meters: int, cycles: int) -> BenchmarkResult:
    """Запустить Python-сборщик в режиме бенчмарка."""
    result = BenchmarkResult(language="Python", meters=meters, cycles=cycles)

    # Импортируем модули Python-сборщика
    sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
    from async_collector import MeterSource, StatsCollector

    source = MeterSource(meters)
    stats = StatsCollector()

    logger.info(f"Running Python benchmark ({cycles} cycles, {meters} meters)...")

    # Измеряем память до
    mem_before = 0
    if psutil:
        mem_before = psutil.Process(os.getpid()).memory_info().rss / 1024 / 1024

    start = time.time()
    cpu_samples = []

    for i in range(cycles):
        cycle_start = time.perf_counter()
        readings = source.read_all()
        elapsed = time.perf_counter() - cycle_start
        stats.record_cycle(len(readings), elapsed)
        result.cycle_times_ms.append(elapsed * 1000)
        logger.debug(f"  Cycle {i+1}: {len(readings)} readings in {elapsed*1000:.1f}ms")

        if psutil:
            cpu_samples.append(psutil.Process(os.getpid()).cpu_percent(interval=0))

    elapsed = time.time() - start
    result.total_time_sec = elapsed
    result.total_readings = stats.readings_count

    # Измеряем память после
    if psutil:
        mem_after = psutil.Process(os.getpid()).memory_info().rss / 1024 / 1024
        result.memory_mb = mem_after - mem_before
        result.cpu_percent = sum(cpu_samples) / len(cpu_samples) if cpu_samples else 0

    logger.info(f"Python benchmark completed in {elapsed:.2f}s")
    return result


def print_comparison(go_result: BenchmarkResult, py_result: BenchmarkResult):
    """Вывести сравнительную таблицу."""
    print(f"\n{'='*70}")
    print(f"📊 GO vs PYTHON — PERFORMANCE COMPARISON")
    print(f"{'='*70}")
    print(f"  Configuration: {go_result.meters} meters, {go_result.cycles} cycles")
    print(f"{'='*70}")
    print(f"  {'Metric':<35} {'Go':<15} {'Python':<15} {'Speedup':<10}")
    print(f"  {'-'*70}")

    # Среднее время цикла
    go_avg = sum(go_result.cycle_times_ms) / len(go_result.cycle_times_ms) if go_result.cycle_times_ms else 0
    py_avg = sum(py_result.cycle_times_ms) / len(py_result.cycle_times_ms) if py_result.cycle_times_ms else 0
    speedup = py_avg / go_avg if go_avg > 0 else 0
    print(f"  {'Avg cycle time (ms)':<35} {go_avg:<15.3f} {py_avg:<15.3f} {speedup:<10.2f}x")

    # Общее время
    print(f"  {'Total time (s)':<35} {go_result.total_time_sec:<15.3f} {py_result.total_time_sec:<15.3f} "
          f"{py_result.total_time_sec / go_result.total_time_sec if go_result.total_time_sec > 0 else 0:<10.2f}x")

    # Пропускная способность
    go_tp = go_result.total_readings / go_result.total_time_sec if go_result.total_time_sec > 0 else 0
    py_tp = py_result.total_readings / py_result.total_time_sec if py_result.total_time_sec > 0 else 0
    tp_speedup = go_tp / py_tp if py_tp > 0 else 0
    print(f"  {'Throughput (readings/sec)':<35} {go_tp:<15.0f} {py_tp:<15.0f} {tp_speedup:<10.2f}x")

    # Память
    print(f"  {'Memory (MB)':<35} {go_result.memory_mb:<15.2f} {py_result.memory_mb:<15.2f} "
          f"{py_result.memory_mb / go_result.memory_mb if go_result.memory_mb > 0 else 0:<10.2f}x")

    # CPU
    print(f"  {'CPU (%)':<35} {go_result.cpu_percent:<15.1f} {py_result.cpu_percent:<15.1f}")
    print(f"{'='*70}\n")


def save_results(go_result: BenchmarkResult, py_result: BenchmarkResult, output_dir: str):
    """Сохранить результаты в JSON."""
    os.makedirs(output_dir, exist_ok=True)
    data = {
        "config": {
            "meters": go_result.meters,
            "cycles": go_result.cycles,
        },
        "go": asdict(go_result),
        "python": asdict(py_result),
    }
    path = os.path.join(output_dir, "benchmark_results.json")
    with open(path, "w") as f:
        json.dump(data, f, indent=2)
    logger.info(f"Results saved to {path}")


def plot_results(go_result: BenchmarkResult, py_result: BenchmarkResult, output_dir: str):
    """Построить графики сравнения."""
    if plt is None:
        logger.warning("matplotlib not installed, skipping graphs")
        return

    os.makedirs(output_dir, exist_ok=True)

    # 1. Сравнение времени циклов
    fig, axes = plt.subplots(2, 2, figsize=(14, 10))

    # График 1: Время каждого цикла
    ax = axes[0, 0]
    ax.plot(range(1, len(go_result.cycle_times_ms) + 1), go_result.cycle_times_ms,
            "b-o", label="Go", markersize=4)
    ax.plot(range(1, len(py_result.cycle_times_ms) + 1), py_result.cycle_times_ms,
            "r-s", label="Python", markersize=4)
    ax.set_xlabel("Cycle")
    ax.set_ylabel("Time (ms)")
    ax.set_title("Cycle Time Comparison")
    ax.legend()
    ax.grid(True, alpha=0.3)

    # График 2: Среднее время цикла (столбцы)
    ax = axes[0, 1]
    labels = ["Go", "Python"]
    go_avg = sum(go_result.cycle_times_ms) / len(go_result.cycle_times_ms)
    py_avg = sum(py_result.cycle_times_ms) / len(py_result.cycle_times_ms)
    bars = ax.bar(labels, [go_avg, py_avg], color=["blue", "red"], alpha=0.7)
    ax.set_ylabel("Avg Cycle Time (ms)")
    ax.set_title("Average Cycle Time")
    for bar, val in zip(bars, [go_avg, py_avg]):
        ax.text(bar.get_x() + bar.get_width() / 2, bar.get_height() + 0.01,
                f"{val:.3f}ms", ha="center", va="bottom", fontsize=10)
    ax.grid(True, alpha=0.3, axis="y")

    # График 3: Throughput
    ax = axes[1, 0]
    go_tp = go_result.total_readings / go_result.total_time_sec
    py_tp = py_result.total_readings / py_result.total_time_sec
    bars = ax.bar(labels, [go_tp, py_tp], color=["blue", "red"], alpha=0.7)
    ax.set_ylabel("Readings/sec")
    ax.set_title("Throughput")
    for bar, val in zip(bars, [go_tp, py_tp]):
        ax.text(bar.get_x() + bar.get_width() / 2, bar.get_height() + 0.1,
                f"{val:.0f}", ha="center", va="bottom", fontsize=10)
    ax.grid(True, alpha=0.3, axis="y")

    # График 4: Память
    ax = axes[1, 1]
    bars = ax.bar(labels, [go_result.memory_mb, py_result.memory_mb],
                  color=["blue", "red"], alpha=0.7)
    ax.set_ylabel("Memory (MB)")
    ax.set_title("Memory Usage")
    for bar, val in zip(bars, [go_result.memory_mb, py_result.memory_mb]):
        ax.text(bar.get_x() + bar.get_width() / 2, bar.get_height() + 0.01,
                f"{val:.2f} MB", ha="center", va="bottom", fontsize=10)
    ax.grid(True, alpha=0.3, axis="y")

    plt.suptitle(f"Go vs Python: {go_result.meters} meters, {go_result.cycles} cycles",
                 fontsize=14, fontweight="bold")
    plt.tight_layout()

    path = os.path.join(output_dir, "benchmark_comparison.png")
    plt.savefig(path, dpi=150, bbox_inches="tight")
    logger.info(f"Graph saved to {path}")
    plt.close()


async def main():
    parser = argparse.ArgumentParser(description="Go vs Python Benchmark")
    parser.add_argument("--meters", type=int, default=50, help="Number of simulated meters")
    parser.add_argument("--cycles", type=int, default=10, help="Number of benchmark cycles")
    parser.add_argument("--output", "-o", default="./benchmark_results",
                        help="Output directory for results and graphs")
    parser.add_argument("--skip-go", action="store_true", help="Skip Go benchmark")
    parser.add_argument("--skip-python", action="store_true", help="Skip Python benchmark")
    args = parser.parse_args()

    print(f"\n{'='*70}")
    print(f"🚀 GO vs PYTHON BENCHMARK")
    print(f"{'='*70}")
    print(f"  Meters: {args.meters}")
    print(f"  Cycles: {args.cycles}")
    print(f"{'='*70}\n")

    go_result = None
    py_result = None

    if not args.skip_go:
        logger.info("Starting Go benchmark...")
        go_result = run_go_benchmark(args.meters, args.cycles)
        logger.info(f"Go done: avg cycle={sum(go_result.cycle_times_ms)/len(go_result.cycle_times_ms):.3f}ms")
    else:
        logger.info("Skipping Go benchmark")

    if not args.skip_python:
        logger.info("Starting Python benchmark...")
        py_result = await run_python_benchmark(args.meters, args.cycles)
        logger.info(f"Python done: avg cycle={sum(py_result.cycle_times_ms)/len(py_result.cycle_times_ms):.3f}ms")
    else:
        logger.info("Skipping Python benchmark")

    if go_result and py_result:
        print_comparison(go_result, py_result)
        save_results(go_result, py_result, args.output)
        plot_results(go_result, py_result, args.output)
    elif go_result:
        print(f"\nGo results: {json.dumps(asdict(go_result), indent=2)}")
    elif py_result:
        print(f"\nPython results: {json.dumps(asdict(py_result), indent=2)}")

    print("\n✅ Benchmark complete!")


if __name__ == "__main__":
    asyncio.run(main())