#!/usr/bin/env python3
"""
Arrow Flight Client for Energy Collector.

Connects to the Go Arrow Flight RPC server, retrieves electricity meter
readings in Apache Arrow columnar format, and displays statistics about
the received data.

Usage:
    python arrow_client.py --server localhost:50051

Requirements:
    pip install pyarrow
"""

import argparse
import time
import sys

import pyarrow as pa
import pyarrow.flight as flight


class EnergyDataClient:
    """Client for fetching energy meter data via Arrow Flight RPC."""

    def __init__(self, server_addr: str):
        self.server_addr = server_addr
        self.client = flight.FlightClient(f"grpc://{server_addr}")

    def fetch_readings(self) -> pa.Table:
        """
        Fetch all meter readings from the Arrow Flight server.

        Returns:
            pyarrow.Table with columns: meter_id, location, timestamp,
            power_kw, voltage_v, current_a
        """
        # Get FlightInfo for the data stream
        descriptor = flight.FlightDescriptor.for_path("all_meters")
        info = self.client.get_flight_info(descriptor)

        # Read all endpoints
        tables = []
        for endpoint in info.endpoints:
            reader = self.client.do_get(endpoint.ticket)
            tables.append(reader.read_all())

        if not tables:
            return pa.table({})

        # Combine all tables
        result = pa.concat_tables(tables)
        return result

    def print_stats(self, table: pa.Table) -> None:
        """Print statistics about the received Arrow table."""
        num_rows = table.num_rows
        num_cols = table.num_columns

        print(f"\n{'='*60}")
        print(f"📊 ENERGY DATA — Arrow Flight RPC")
        print(f"{'='*60}")
        print(f"  Server:       {self.server_addr}")
        print(f"  Rows:         {num_rows}")
        print(f"  Columns:      {num_cols}")
        print(f"  Column names: {table.column_names}")
        print(f"{'='*60}")

        # Schema info
        print(f"\n📋 Schema:")
        for field in table.schema:
            print(f"  • {field.name}: {field.type}")

        # Data statistics
        print(f"\n📈 Statistics:")
        if num_rows > 0:
            power = table.column("power_kw").to_pylist()
            voltage = table.column("voltage_v").to_pylist()
            current = table.column("current_a").to_pylist()

            print(f"  Power (kW):   min={min(power):.2f}, "
                  f"max={max(power):.2f}, "
                  f"avg={sum(power)/len(power):.2f}")
            print(f"  Voltage (V):  min={min(voltage):.2f}, "
                  f"max={max(voltage):.2f}, "
                  f"avg={sum(voltage)/len(voltage):.2f}")
            print(f"  Current (A):  min={min(current):.2f}, "
                  f"max={max(current):.2f}, "
                  f"avg={sum(current)/len(current):.2f}")

            # Unique meters
            unique_meters = set(table.column("meter_id").to_pylist())
            print(f"  Unique meters: {len(unique_meters)}")

        # Memory usage
        total_bytes = table.nbytes
        print(f"\n💾 Memory usage:")
        print(f"  Arrow table:  {total_bytes:,} bytes "
              f"({total_bytes / 1024:.1f} KB)")

        print(f"{'='*60}\n")

    def benchmark(self, num_iterations: int = 5) -> None:
        """
        Run benchmark: fetch data multiple times and measure performance.

        Args:
            num_iterations: Number of iterations to run
        """
        print(f"\n{'='*60}")
        print(f"⏱️  BENCHMARK — Arrow Flight RPC")
        print(f"{'='*60}")
        print(f"  Iterations: {num_iterations}")
        print(f"{'='*60}")

        times = []
        row_counts = []
        byte_sizes = []

        for i in range(num_iterations):
            start = time.perf_counter()
            table = self.fetch_readings()
            elapsed = time.perf_counter() - start

            times.append(elapsed)
            row_counts.append(table.num_rows)
            byte_sizes.append(table.nbytes)

            print(f"  Iteration {i+1}: {table.num_rows:>4} rows, "
                  f"{table.nbytes:>8,} bytes, "
                  f"{elapsed*1000:>6.1f} ms")

        avg_time = sum(times) / len(times)
        avg_rows = sum(row_counts) / len(row_counts)
        avg_bytes = sum(byte_sizes) / len(byte_sizes)

        print(f"{'='*60}")
        print(f"  Average:      {avg_rows:>4.0f} rows, "
              f"{avg_bytes:>8,.0f} bytes, "
              f"{avg_time*1000:>6.1f} ms")
        print(f"  Throughput:   {avg_bytes / avg_time / 1024 / 1024:.2f} MB/s")
        print(f"{'='*60}\n")


def main():
    parser = argparse.ArgumentParser(
        description="Arrow Flight Client for Energy Collector"
    )
    parser.add_argument(
        "--server", "-s",
        default="localhost:50051",
        help="Arrow Flight server address (default: localhost:50051)"
    )
    parser.add_argument(
        "--benchmark", "-b",
        action="store_true",
        help="Run performance benchmark"
    )
    parser.add_argument(
        "--iterations", "-n",
        type=int,
        default=5,
        help="Number of benchmark iterations (default: 5)"
    )
    args = parser.parse_args()

    client = EnergyDataClient(args.server)

    try:
        # Fetch and display data
        print(f"Connecting to Arrow Flight server at {args.server}...")
        table = client.fetch_readings()
        client.print_stats(table)

        # Run benchmark if requested
        if args.benchmark:
            client.benchmark(args.iterations)

    except flight.FlightUnavailableError as e:
        print(f"❌ Error: Cannot connect to server at {args.server}")
        print(f"   Make sure the Arrow Flight server is running.")
        print(f"   Details: {e}")
        sys.exit(1)
    except Exception as e:
        print(f"❌ Error: {e}")
        sys.exit(1)


if __name__ == "__main__":
    main()