#!/usr/bin/env python3
"""Directly-follows summary of a case/activity/timestamp event log.

Uses pm4py.discover_dfg and pm4py.get_variants. Durations are medians from
pandas, not discover_performance_dfg. Loop folding is linear in the trace length.
"""

from __future__ import annotations

import argparse
import sys
import time

import pandas as pd

CASE = "case:concept:name"
ACTIVITY = "concept:name"
TIMESTAMP = "time:timestamp"

CASE_CANDIDATES = (CASE, "case_id", "caseid", "case")
ACTIVITY_CANDIDATES = (ACTIVITY, "activity", "activity_name", "event")
TIMESTAMP_CANDIDATES = (TIMESTAMP, "timestamp", "time", "event_time")


def log(message: str) -> None:
    print(message, flush=True)


def resolve_column(columns: list[str], explicit: str | None, candidates: tuple[str, ...], role: str) -> str:
    if explicit:
        if explicit in columns:
            return explicit
        matches = [column for column in columns if column.lower() == explicit.lower()]
        if len(matches) == 1:
            return matches[0]
        raise SystemExit(f"{role} column {explicit!r} is not in {columns}")

    by_lower = {}
    for column in columns:
        by_lower.setdefault(column.lower(), column)
    for candidate in candidates:
        if candidate in columns:
            return candidate
        found = by_lower.get(candidate.lower())
        if found:
            return found
    raise SystemExit(
        f"could not find a {role} column. pass --{role}. columns: {columns}"
    )


def parse_timestamps(series: pd.Series) -> pd.Series:
    errors: list[str] = []
    for kwargs in (
        {"utc": True, "format": "ISO8601"},
        {"utc": True, "format": "mixed"},
        {"utc": True},
    ):
        try:
            parsed = pd.to_datetime(series, **kwargs)
        except (ValueError, TypeError) as exc:
            errors.append(str(exc))
            continue
        if parsed.notna().all():
            return parsed
        errors.append(f"{kwargs}: {int(parsed.isna().sum())} unparsed values")
    raise SystemExit("could not parse timestamps:\n" + "\n".join(errors))


def compress(seq: tuple[str, ...]) -> tuple[str, ...]:
    items = list(seq)

    def one_pass(values: list[str], period: int) -> list[str]:
        out: list[str] = []
        index = 0
        size = len(values)
        while index < size:
            if index + 2 * period <= size:
                block = values[index : index + period]
                repeats = 1
                while (
                    index + (repeats + 1) * period <= size
                    and values[index + repeats * period : index + (repeats + 1) * period] == block
                ):
                    repeats += 1
                if repeats >= 2:
                    out.append("(" + " -> ".join(block) + f")×{repeats}")
                    index += repeats * period
                    continue
            out.append(values[index])
            index += 1
        return out

    for _ in range(8):
        progressed = False
        for period in (2, 3, 4, 5, 6):
            nxt = one_pass(items, period)
            if len(nxt) < len(items):
                items = nxt
                progressed = True
                break
        if not progressed:
            break
    return tuple(items)


def variant_activities(variant: object) -> tuple[str, ...]:
    if isinstance(variant, str):
        raise SystemExit(f"unexpected variant key type str: {variant[:80]!r}")
    return tuple(str(activity) for activity in variant)  # type: ignore[arg-type]


def analyze(frame: pd.DataFrame, label: str, args: argparse.Namespace) -> None:
    import pm4py

    cases = int(frame[CASE].nunique())
    log("")
    log("=" * 72)
    if label:
        log(f"GROUP {label}  events={len(frame)}  cases={cases}")
    else:
        log(f"events={len(frame)}  cases={cases}")
    if cases == 0:
        return

    started = time.perf_counter()
    _dfg, starts, ends = pm4py.discover_dfg(frame)
    log(f"discover_dfg {time.perf_counter() - started:.1f}s")
    log("start " + format_counts(starts))
    log("end " + format_counts(ends))

    same_case = frame[CASE].eq(frame[CASE].shift())
    same_activity = frame[ACTIVITY].eq(frame[ACTIVITY].shift())
    collapsed = frame.loc[~(same_case & same_activity)].copy()
    log(f"collapsed consecutive repeats {len(frame)} -> {len(collapsed)}")

    started = time.perf_counter()
    dfg, _, _ = pm4py.discover_dfg(collapsed)
    log(f"discover_dfg collapsed {time.perf_counter() - started:.1f}s")

    collapsed["next"] = collapsed.groupby(CASE, sort=False)[ACTIVITY].shift(-1)
    collapsed["dt"] = collapsed.groupby(CASE, sort=False)[TIMESTAMP].shift(-1)
    collapsed["dt"] = (collapsed["dt"] - collapsed[TIMESTAMP]).dt.total_seconds()
    edges = collapsed.dropna(subset=["next"])
    covered = edges.groupby([ACTIVITY, "next"], sort=False)[CASE].nunique()
    median = edges.groupby([ACTIVITY, "next"], sort=False)["dt"].median()

    rows = []
    for (source, target), follows in dfg.items():
        key = (source, target)
        case_count = int(covered.get(key, 0))
        rows.append(
            (
                case_count / cases,
                case_count,
                int(follows),
                float(median.get(key, float("nan"))),
                str(source),
                str(target),
            )
        )
    rows.sort(key=lambda row: (row[0], row[2]), reverse=True)
    selected = [row for row in rows if row[0] >= args.min_coverage]
    if len(selected) < min(15, len(rows)):
        selected = rows[: min(15, len(rows))]
    selected = selected[: args.top_edges]
    log(
        f"collapsed dfg (coverage >= {args.min_coverage:.0%}, "
        f"showing {len(selected)}/{len(rows)})"
    )
    for ratio, case_count, follows, median_s, source, target in selected:
        log(
            f"  {ratio:6.1%} cases ({case_count:5d})  follows {follows:6d}"
            f"  median {median_s:8.3f}s  {source} -> {target}"
        )

    started = time.perf_counter()
    variants = pm4py.get_variants(collapsed, max_repetitions=1)
    log(f"get_variants {time.perf_counter() - started:.1f}s  raw {len(variants)}")
    folded: dict[tuple[str, ...], int] = {}
    for variant, count in variants.items():
        key = compress(variant_activities(variant))
        folded[key] = folded.get(key, 0) + int(count)
    log(f"folded variants {len(folded)}")
    ranked = sorted(folded.items(), key=lambda item: item[1], reverse=True)
    shown_cases = 0
    for variant, count in ranked[: args.top_variants]:
        shown_cases += count
        text = " -> ".join(variant)
        if len(text) > 500:
            text = text[:500] + "..."
        log(f"  {count:5d} ({count / cases:5.1%})  {text}")
    shown_n = min(args.top_variants, len(ranked))
    if shown_n:
        log(f"  top{shown_n} coverage {shown_cases / cases:.1%}")

    per_case = frame.groupby(CASE).size()
    log(
        "events/case"
        f" p50={fmt_count(per_case.median())} p90={fmt_count(per_case.quantile(0.9))}"
        f" max={fmt_count(per_case.max())}"
    )
    counts = frame.groupby([CASE, ACTIVITY]).size().unstack(fill_value=0)
    log("activity count per case (p50 / p90 / max / cases-with), coverage-filtered")
    omitted = 0
    activity_rows = []
    for activity in counts.columns:
        column = counts[activity]
        with_activity = int((column > 0).sum())
        if with_activity / cases < args.min_coverage:
            omitted += 1
            continue
        activity_rows.append((with_activity, str(activity), column))
    activity_rows.sort(key=lambda item: item[0], reverse=True)
    for with_activity, activity, column in activity_rows:
        log(
            f"  {activity}  p50={fmt_count(column.median())}  p90={fmt_count(column.quantile(0.9))}"
            f"  max={fmt_count(column.max())}  with={with_activity}/{cases}"
        )
    if omitted:
        log(f"  omitted {omitted} activities below coverage {args.min_coverage:.0%}")

    span = frame.groupby(CASE)[TIMESTAMP].agg(["min", "max"])
    seconds = (span["max"] - span["min"]).dt.total_seconds()
    log(
        "case duration sec"
        f" p50={float(seconds.median()):.1f} p90={float(seconds.quantile(0.9)):.1f}"
        f" max={float(seconds.max()):.1f}"
    )


def fmt_count(value: float) -> str:
    number = float(value)
    if number.is_integer():
        return str(int(number))
    return f"{number:.1f}"


def format_counts(counts: dict) -> str:
    ordered = sorted(counts.items(), key=lambda item: item[1], reverse=True)
    return str({str(key): int(value) for key, value in ordered})


def load(args: argparse.Namespace) -> tuple[pd.DataFrame, str | None]:
    try:
        raw = pd.read_csv(args.log)
    except pd.errors.EmptyDataError as exc:
        raise SystemExit(f"empty log: {args.log}") from exc
    columns = [str(column) for column in raw.columns]
    raw.columns = columns
    case_col = resolve_column(columns, args.case, CASE_CANDIDATES, "case")
    activity_col = resolve_column(columns, args.activity, ACTIVITY_CANDIDATES, "activity")
    timestamp_col = resolve_column(columns, args.timestamp, TIMESTAMP_CANDIDATES, "timestamp")
    group_col = None
    if args.group:
        group_col = resolve_column(columns, args.group, (), "group")

    keep = [case_col, activity_col, timestamp_col]
    if group_col and group_col not in keep:
        keep.append(group_col)
    before = len(raw)
    raw = raw.dropna(subset=keep)
    dropped = before - len(raw)
    if dropped:
        log(f"dropped {dropped} rows with missing case, activity, timestamp, or group")

    frame = pd.DataFrame(
        {
            CASE: raw[case_col].astype(str),
            ACTIVITY: raw[activity_col].astype(str),
            TIMESTAMP: parse_timestamps(raw[timestamp_col]),
        }
    )
    if group_col:
        frame["__group"] = raw[group_col].astype(str).to_numpy()
    years = frame[TIMESTAMP].dt.year
    if len(frame) and (int(years.min()) < 1990 or int(years.max()) > 2100):
        log(f"warning: timestamp range {frame[TIMESTAMP].min()} .. {frame[TIMESTAMP].max()}")
    frame = frame.sort_values([CASE, TIMESTAMP], kind="mergesort")
    return frame, group_col


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("log", help="CSV event log")
    parser.add_argument("--case", help="case id column")
    parser.add_argument("--activity", help="activity column")
    parser.add_argument("--timestamp", help="timestamp column")
    parser.add_argument("--group", help="split the log by this column and analyze each value")
    parser.add_argument("--min-coverage", type=float, default=0.05)
    parser.add_argument("--top-edges", type=int, default=40)
    parser.add_argument("--top-variants", type=int, default=12)
    parser.add_argument("--top-activities", type=int, default=30)
    args = parser.parse_args()

    started = time.perf_counter()
    frame, group_col = load(args)
    log(f"loaded {len(frame)} rows in {time.perf_counter() - started:.1f}s from {args.log}")
    if frame.empty:
        raise SystemExit("no events left after dropping incomplete rows")
    log(f"cases {frame[CASE].nunique()}")
    log(f"from {frame[TIMESTAMP].min()} to {frame[TIMESTAMP].max()}")
    log(f"columns case={args.case or 'auto'} activity={args.activity or 'auto'} timestamp={args.timestamp or 'auto'}")
    log("activities")
    counts = frame[ACTIVITY].value_counts()
    for activity, count in counts.head(args.top_activities).items():
        log(f"  {int(count):7d}  {activity}")
    hidden = len(counts) - min(len(counts), args.top_activities)
    if hidden > 0:
        log(f"  ... {hidden} more activities")

    if group_col:
        log(f"group column {args.group}")
        grouped = frame.groupby("__group", sort=True)
        log("groups " + str({str(key): int(len(part)) for key, part in grouped}))
        for key, part in grouped:
            analyze(part.drop(columns="__group"), str(key), args)
    else:
        analyze(frame, "", args)
    log("DONE")


if __name__ == "__main__":
    main()
