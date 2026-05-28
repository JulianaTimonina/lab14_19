#!/usr/bin/env python3
"""
Streamlit-дашборд для анализа энергопотребления в реальном времени.

Эмулирует счётчики электроэнергии (как async_collector.py) и отображает
агрегированную статистику и графики, обновляющиеся в реальном времени.

Usage:
    streamlit run python/dashboard.py
    streamlit run python/dashboard.py -- --meters=100 --interval=3
"""

import argparse
import math
import random
import sys
import time
from collections import defaultdict
from dataclasses import dataclass, asdict
from datetime import datetime, timedelta, timezone
from typing import Dict, List, Optional, Tuple

import streamlit as st
import pandas as pd
import plotly.express as px
import plotly.graph_objects as go
from plotly.subplots import make_subplots

# ============================================================
# Эмуляция счётчиков (аналогично async_collector.py)
# ============================================================

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
    id: str
    location: str
    base_load: float
    fluctuation: float


@dataclass
class Reading:
    meter_id: str
    location: str
    timestamp: str
    power_kw: float
    voltage_v: float
    current_a: float


class MeterSource:
    """Эмуляция счётчиков электроэнергии."""

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


# ============================================================
# Оконная агрегация (скользящее окно)
# ============================================================

class SlidingWindow:
    """Скользящее окно для хранения последних N секунд показаний."""

    def __init__(self, window_seconds: int = 300):
        self.window_seconds = window_seconds
        self.readings: List[Tuple[datetime, Reading]] = []

    def add(self, reading: Reading):
        ts = datetime.fromisoformat(reading.timestamp)
        self.readings.append((ts, reading))

    def prune(self):
        now = datetime.now(timezone.utc)
        cutoff = now - timedelta(seconds=self.window_seconds)
        self.readings = [(ts, r) for ts, r in self.readings if ts >= cutoff]

    def get_all(self) -> List[Reading]:
        self.prune()
        return [r for _, r in self.readings]

    def get_stats(self) -> dict:
        """Агрегированная статистика по всем показаниям в окне."""
        readings = self.get_all()
        if not readings:
            return {}

        powers = [r.power_kw for r in readings]
        voltages = [r.voltage_v for r in readings]
        currents = [r.current_a for r in readings]

        # По счётчикам
        meter_stats: Dict[str, dict] = {}
        for r in readings:
            if r.meter_id not in meter_stats:
                meter_stats[r.meter_id] = {
                    "meter_id": r.meter_id,
                    "location": r.location,
                    "powers": [],
                    "voltages": [],
                    "currents": [],
                }
            meter_stats[r.meter_id]["powers"].append(r.power_kw)
            meter_stats[r.meter_id]["voltages"].append(r.voltage_v)
            meter_stats[r.meter_id]["currents"].append(r.current_a)

        # По локациям
        location_stats: Dict[str, dict] = {}
        for r in readings:
            if r.location not in location_stats:
                location_stats[r.location] = {
                    "location": r.location,
                    "powers": [],
                    "count": 0,
                }
            location_stats[r.location]["powers"].append(r.power_kw)
            location_stats[r.location]["count"] += 1

        return {
            "total_readings": len(readings),
            "unique_meters": len(meter_stats),
            "unique_locations": len(location_stats),
            "power": {
                "min": round(min(powers), 3),
                "max": round(max(powers), 3),
                "avg": round(sum(powers) / len(powers), 3),
                "sum": round(sum(powers), 3),
                "total_kwh": round(sum(powers) * (self.window_seconds / 3600), 3),
            },
            "voltage": {
                "min": round(min(voltages), 2),
                "max": round(max(voltages), 2),
                "avg": round(sum(voltages) / len(voltages), 2),
            },
            "current": {
                "min": round(min(currents), 3),
                "max": round(max(currents), 3),
                "avg": round(sum(currents) / len(currents), 3),
            },
            "meter_stats": meter_stats,
            "location_stats": location_stats,
        }


# ============================================================
# Парсинг аргументов командной строки
# ============================================================

def parse_args():
    parser = argparse.ArgumentParser(description="Energy Dashboard")
    parser.add_argument("--meters", type=int, default=50, help="Number of simulated meters")
    parser.add_argument("--interval", type=float, default=3.0, help="Update interval in seconds")
    parser.add_argument("--window", type=int, default=300, help="Sliding window size in seconds")
    parser.add_argument("--theme", type=str, default="dark", choices=["dark", "light"], help="Dashboard theme")
    return parser.parse_args()


# ============================================================
# Инициализация состояния сессии
# ============================================================

def init_session_state(args):
    """Инициализировать состояние Streamlit-сессии."""
    if "source" not in st.session_state:
        st.session_state.source = MeterSource(args.meters)
    if "window" not in st.session_state:
        st.session_state.window = SlidingWindow(args.window)
    if "history" not in st.session_state:
        st.session_state.history = []  # Для временных рядов
    if "cycle_count" not in st.session_state:
        st.session_state.cycle_count = 0
    if "last_update" not in st.session_state:
        st.session_state.last_update = datetime.now()
    if "args" not in st.session_state:
        st.session_state.args = args


# ============================================================
# Функции для обновления данных
# ============================================================

def collect_data():
    """Собрать новые показания и добавить их в окно."""
    source: MeterSource = st.session_state.source
    window: SlidingWindow = st.session_state.window

    readings = source.read_all()
    for r in readings:
        window.add(r)

    st.session_state.cycle_count += 1
    st.session_state.last_update = datetime.now()

    # Сохраняем в историю для временных рядов (агрегированные метрики)
    stats = window.get_stats()
    if stats:
        st.session_state.history.append({
            "timestamp": datetime.now(),
            "total_power_kw": stats["power"]["sum"],
            "avg_power_kw": stats["power"]["avg"],
            "avg_voltage_v": stats["voltage"]["avg"],
            "avg_current_a": stats["current"]["avg"],
            "unique_meters": stats["unique_meters"],
            "total_readings": stats["total_readings"],
        })

    # Ограничиваем историю последними 100 точками
    if len(st.session_state.history) > 100:
        st.session_state.history = st.session_state.history[-100:]


# ============================================================
# UI-компоненты
# ============================================================

def render_metric_card(label: str, value: str, delta: str = "", help_text: str = ""):
    """Отрисовать карточку с метрикой."""
    st.metric(label=label, value=value, delta=delta, help=help_text)


def render_header(args):
    """Заголовок дашборда."""
    col1, col2, col3 = st.columns([3, 1, 1])
    with col1:
        st.title("⚡ Анализ энергопотребления")
        st.caption(f"Эмулируется {args.meters} счётчиков · "
                   f"Интервал обновления: {args.interval}с · "
                   f"Окно: {args.window}с ({args.window//60} мин)")
    with col2:
        st.metric(
            label="Циклов сбора",
            value=st.session_state.cycle_count,
        )
    with col3:
        last_upd = st.session_state.last_update
        st.metric(
            label="Последнее обновление",
            value=last_upd.strftime("%H:%M:%S"),
        )


def render_kpi_row(stats: dict):
    """Строка ключевых показателей."""
    if not stats:
        st.info("⏳ Ожидание данных...")
        return

    p = stats["power"]
    v = stats["voltage"]
    c = stats["current"]

    cols = st.columns(5)
    with cols[0]:
        render_metric_card(
            "⚡ Общая мощность",
            f"{p['sum']:.1f} кВт",
            help_text=f"Суммарная мощность всех счётчиков за окно {st.session_state.args.window}с"
        )
    with cols[1]:
        render_metric_card(
            "📊 Средняя мощность",
            f"{p['avg']:.2f} кВт",
            help_text="Средняя мощность на один счётчик"
        )
    with cols[2]:
        render_metric_card(
            "🔌 Среднее напряжение",
            f"{v['avg']:.1f} В",
            help_text=f"Диапазон: {v['min']}–{v['max']} В"
        )
    with cols[3]:
        render_metric_card(
            "💡 Средний ток",
            f"{c['avg']:.1f} А",
            help_text=f"Диапазон: {c['min']}–{c['max']} А"
        )
    with cols[4]:
        render_metric_card(
            "📏 Всего показаний",
            f"{stats['total_readings']:,}",
            help_text=f"Уникальных счётчиков: {stats['unique_meters']}"
        )


def render_power_distribution(stats: dict):
    """Гистограмма распределения мощности."""
    if not stats:
        return

    meter_stats = stats.get("meter_stats", {})
    if not meter_stats:
        return

    # Средняя мощность каждого счётчика
    meter_avgs = []
    for ms in meter_stats.values():
        avg_p = sum(ms["powers"]) / len(ms["powers"]) if ms["powers"] else 0
        meter_avgs.append({
            "meter_id": ms["meter_id"],
            "location": ms["location"],
            "avg_power_kw": round(avg_p, 3),
        })

    df = pd.DataFrame(meter_avgs)

    fig = px.histogram(
        df,
        x="avg_power_kw",
        nbins=20,
        title="📊 Распределение средней мощности по счётчикам",
        labels={"avg_power_kw": "Средняя мощность (кВт)", "count": "Количество счётчиков"},
        color_discrete_sequence=["#636EFA"],
    )
    fig.update_layout(
        bargap=0.1,
        xaxis_title="Средняя мощность (кВт)",
        yaxis_title="Количество счётчиков",
    )
    st.plotly_chart(fig, width='stretch')


def render_power_by_location(stats: dict):
    """Столбчатая диаграмма мощности по локациям."""
    if not stats:
        return

    loc_stats = stats.get("location_stats", {})
    if not loc_stats:
        return

    loc_data = []
    for ls in loc_stats.values():
        avg_p = sum(ls["powers"]) / len(ls["powers"]) if ls["powers"] else 0
        loc_data.append({
            "location": ls["location"],
            "avg_power_kw": round(avg_p, 3),
            "count": ls["count"],
        })

    df = pd.DataFrame(loc_data).sort_values("avg_power_kw", ascending=True)

    fig = px.bar(
        df,
        y="location",
        x="avg_power_kw",
        orientation="h",
        title="🏢 Средняя мощность по локациям",
        labels={"avg_power_kw": "Средняя мощность (кВт)", "location": "Локация"},
        color="avg_power_kw",
        color_continuous_scale="Viridis",
        text_auto=".2f",
    )
    fig.update_layout(yaxis_title=None)
    st.plotly_chart(fig, width='stretch')


def render_time_series():
    """Временной ряд агрегированных метрик."""
    history = st.session_state.history
    if len(history) < 2:
        st.info("⏳ Накопление данных для временного ряда...")
        return

    df = pd.DataFrame(history)

    fig = make_subplots(
        rows=3, cols=1,
        shared_xaxes=True,
        vertical_spacing=0.08,
        subplot_titles=("Суммарная мощность (кВт)", "Среднее напряжение (В)", "Средний ток (А)"),
    )

    fig.add_trace(
        go.Scatter(x=df["timestamp"], y=df["total_power_kw"], mode="lines+markers",
                   name="Мощность", line=dict(color="#636EFA", width=2)),
        row=1, col=1,
    )
    fig.add_trace(
        go.Scatter(x=df["timestamp"], y=df["avg_voltage_v"], mode="lines+markers",
                   name="Напряжение", line=dict(color="#00CC96", width=2)),
        row=2, col=1,
    )
    fig.add_trace(
        go.Scatter(x=df["timestamp"], y=df["avg_current_a"], mode="lines+markers",
                   name="Ток", line=dict(color="#EF553B", width=2)),
        row=3, col=1,
    )

    fig.update_layout(
        height=600,
        title_text="📈 Агрегированные метрики во времени",
        showlegend=False,
        hovermode="x unified",
    )
    fig.update_xaxes(title_text="Время", row=3, col=1)
    fig.update_yaxes(title_text="кВт", row=1, col=1)
    fig.update_yaxes(title_text="В", row=2, col=1)
    fig.update_yaxes(title_text="А", row=3, col=1)

    st.plotly_chart(fig, width='stretch')


def render_top_consumers(stats: dict):
    """Топ-10 потребителей."""
    if not stats:
        return

    meter_stats = stats.get("meter_stats", {})
    if not meter_stats:
        return

    meter_avgs = []
    for ms in meter_stats.values():
        avg_p = sum(ms["powers"]) / len(ms["powers"]) if ms["powers"] else 0
        max_p = max(ms["powers"]) if ms["powers"] else 0
        meter_avgs.append({
            "meter_id": ms["meter_id"],
            "location": ms["location"],
            "avg_power_kw": round(avg_p, 3),
            "max_power_kw": round(max_p, 3),
        })

    df = pd.DataFrame(meter_avgs)
    top10 = df.nlargest(10, "avg_power_kw")

    fig = px.bar(
        top10,
        x="meter_id",
        y="avg_power_kw",
        color="location",
        title="🏆 Топ-10 потребителей по средней мощности",
        labels={"avg_power_kw": "Средняя мощность (кВт)", "meter_id": "Счётчик"},
        text_auto=".2f",
        barmode="group",
    )
    fig.update_layout(xaxis_tickangle=-45)
    st.plotly_chart(fig, width='stretch')


def render_meter_table(stats: dict):
    """Таблица со статистикой по каждому счётчику."""
    if not stats:
        return

    meter_stats = stats.get("meter_stats", {})
    if not meter_stats:
        return

    rows = []
    for ms in meter_stats.values():
        avg_p = sum(ms["powers"]) / len(ms["powers"]) if ms["powers"] else 0
        min_p = min(ms["powers"]) if ms["powers"] else 0
        max_p = max(ms["powers"]) if ms["powers"] else 0
        avg_v = sum(ms["voltages"]) / len(ms["voltages"]) if ms["voltages"] else 0
        avg_c = sum(ms["currents"]) / len(ms["currents"]) if ms["currents"] else 0
        rows.append({
            "Счётчик": ms["meter_id"],
            "Локация": ms["location"],
            "Средняя мощность (кВт)": round(avg_p, 3),
            "Мин. мощность (кВт)": round(min_p, 3),
            "Макс. мощность (кВт)": round(max_p, 3),
            "Среднее напряжение (В)": round(avg_v, 2),
            "Средний ток (А)": round(avg_c, 3),
            "Показаний": len(ms["powers"]),
        })

    df = pd.DataFrame(rows)
    df = df.sort_values("Средняя мощность (кВт)", ascending=False)

    st.dataframe(
        df,
        width='stretch',
        hide_index=True,
        column_config={
            "Средняя мощность (кВт)": st.column_config.NumberColumn(format="%.3f"),
            "Мин. мощность (кВт)": st.column_config.NumberColumn(format="%.3f"),
            "Макс. мощность (кВт)": st.column_config.NumberColumn(format="%.3f"),
            "Среднее напряжение (В)": st.column_config.NumberColumn(format="%.2f"),
            "Средний ток (А)": st.column_config.NumberColumn(format="%.3f"),
        },
    )


def render_sidebar(args):
    """Боковая панель с настройками."""
    with st.sidebar:
        st.header("⚙️ Настройки")

        st.subheader("Параметры эмуляции")
        meters = st.number_input("Количество счётчиков", min_value=1, max_value=500,
                                  value=args.meters, step=10)
        interval = st.slider("Интервал обновления (с)", min_value=1, max_value=30,
                             value=int(args.interval), step=1)
        window = st.slider("Размер окна (с)", min_value=30, max_value=900,
                           value=args.window, step=30,
                           help="Скользящее окно для агрегации данных")

        st.subheader("Управление")
        col1, col2 = st.columns(2)
        with col1:
            if st.button("🔄 Обновить сейчас", use_container_width=True):
                collect_data()
                st.rerun()
        with col2:
            if st.button("🗑️ Сбросить данные", use_container_width=True):
                st.session_state.window = SlidingWindow(window)
                st.session_state.history = []
                st.session_state.cycle_count = 0
                st.rerun()

        st.subheader("Информация")
        st.info(
            f"**Счётчиков:** {meters}\n\n"
            f"**Локаций:** {len(LOCATIONS)}\n\n"
            f"**Окно:** {window}с ({window//60} мин)\n\n"
            f"**Циклов:** {st.session_state.cycle_count}\n\n"
            f"**Точек истории:** {len(st.session_state.history)}"
        )

        st.caption("Разработано для лабораторной работы №14\n"
                   "МТП, 6 семестр")

    return meters, interval, window


# ============================================================
# Главная функция
# ============================================================

def main():
    args = parse_args()

    # Настройка страницы
    st.set_page_config(
        page_title="Energy Dashboard",
        page_icon="⚡",
        layout="wide",
        initial_sidebar_state="expanded",
    )

    # Тема
    if args.theme == "dark":
        st.markdown("""
        <style>
        .stApp { background-color: #0E1117; }
        .stMetric { background-color: #1E1E1E; padding: 10px; border-radius: 8px; }
        </style>
        """, unsafe_allow_html=True)

    # Инициализация
    init_session_state(args)

    # Боковая панель
    meters, interval, window = render_sidebar(args)

    # Если параметры изменились — пересоздаём источник
    if meters != st.session_state.args.meters:
        st.session_state.source = MeterSource(meters)
        st.session_state.args.meters = meters
        st.session_state.window = SlidingWindow(window)
        st.session_state.history = []

    # Автоматический сбор данных
    collect_data()

    # Заголовок
    render_header(args)

    # KPI
    stats = st.session_state.window.get_stats()
    render_kpi_row(stats)

    # Графики
    col1, col2 = st.columns(2)
    with col1:
        render_power_distribution(stats)
    with col2:
        render_power_by_location(stats)

    render_time_series()
    render_top_consumers(stats)

    # Таблица
    with st.expander("📋 Детальная статистика по счётчикам", expanded=False):
        render_meter_table(stats)

    # Автообновление
    st.caption(f"🔄 Автообновление каждые {interval}с · "
               f"Данных в окне: {stats.get('total_readings', 0):,} · "
               f"Счётчиков: {stats.get('unique_meters', 0)}")

    # Перезапуск через interval секунд
    time.sleep(0.1)  # Небольшая задержка для завершения рендеринга
    st.rerun()


if __name__ == "__main__":
    main()