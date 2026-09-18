"""Daily, local-only aggregate statistics for turn-state probe sources.

The database deliberately contains counters only: a UTC day, account ID,
configured model name, and a validated source label.  It never stores proxy
endpoints, credentials, turn-state values, response bodies, or raw errors.

The resulting source comparison is observational.  In particular, the caller
may sample static sources before dynamic ones, so these counters do not make a
causal claim about any source.
"""

from __future__ import annotations

from datetime import date, datetime, timezone
from pathlib import Path
import re
import sqlite3
from typing import Any, Dict, Optional, Union


_SOURCE_RE = re.compile(r"^[a-z0-9][a-z0-9_-]{0,63}$")
_MAX_SQLITE_INTEGER = (1 << 63) - 1
_COUNT_FIELDS = (
    "attempts",
    "http_200",
    "state_292",
    "target_hits",
    "persisted",
    "errors",
    "header_ms_total",
)


def _utc_day() -> str:
    """Return the current calendar day in UTC, independent of local time."""
    return datetime.now(timezone.utc).date().isoformat()


def _require_int(value: object, name: str, minimum: int = 0) -> int:
    if isinstance(value, bool) or not isinstance(value, int):
        raise TypeError(f"{name} must be an integer")
    if value < minimum or value > _MAX_SQLITE_INTEGER:
        raise ValueError(f"{name} is outside the supported range")
    return value


def _normalise_source(source: object) -> str:
    """Return a canonical safe source label without retaining endpoint data."""
    if not isinstance(source, str):
        raise TypeError("source must be a string")
    candidate = source.strip().lower()
    if not _SOURCE_RE.fullmatch(candidate):
        raise ValueError("source must be a safe slug of at most 64 characters")
    return candidate


def _require_model(model: object) -> str:
    # Model names are deliberately not enumerated or slug-normalised: the
    # manager supports custom model names.  SQLite parameters keep them data.
    if not isinstance(model, str):
        raise TypeError("model must be a string")
    return model


def _coerce_day(value: Optional[Union[str, date]], name: str) -> Optional[str]:
    if value is None:
        return None
    if isinstance(value, datetime):
        return value.date().isoformat()
    if isinstance(value, date):
        return value.isoformat()
    if isinstance(value, str):
        try:
            return date.fromisoformat(value).isoformat()
        except ValueError as exc:
            raise ValueError(f"{name} must be an ISO UTC day (YYYY-MM-DD)") from exc
    raise TypeError(f"{name} must be an ISO UTC day or date")


def _empty_counts() -> Dict[str, Any]:
    counts: Dict[str, Any] = {field: 0 for field in _COUNT_FIELDS}
    counts["status_counts"] = {}
    return counts


def _add_counts(destination: Dict[str, Any], source: Dict[str, Any]) -> None:
    for field in _COUNT_FIELDS:
        destination[field] += int(source[field])
    for status, count in source["status_counts"].items():
        destination["status_counts"][status] = (
            destination["status_counts"].get(status, 0) + int(count)
        )


def _reported_counts(counts: Dict[str, Any]) -> Dict[str, Any]:
    """Convert an internal aggregate into a stable, JSON-safe report object."""
    attempts = counts["attempts"]
    target_hits = counts["target_hits"]

    def rate(numerator: int, denominator: int = attempts) -> float:
        return numerator / denominator if denominator else 0.0

    return {
        **{field: int(counts[field]) for field in _COUNT_FIELDS},
        "status_counts": {
            str(status): int(count)
            for status, count in sorted(counts["status_counts"].items())
        },
        "http_200_rate": rate(counts["http_200"]),
        "state_292_rate": rate(counts["state_292"]),
        "target_hit_rate": rate(target_hits),
        "persisted_rate": rate(counts["persisted"]),
        "persisted_per_target_hit_rate": rate(counts["persisted"], target_hits),
        "error_rate": rate(counts["errors"]),
        "header_ms_average": rate(counts["header_ms_total"]),
    }


class ProbeStats:
    """Persist daily source-level counters in ``state_dir/probe-stats.sqlite3``.

    ``record_attempt`` is one atomic SQLite transaction, including its status
    counter.  ``record_persisted`` is intentionally separate: a valid target
    response and a database-validated save are different outcomes.
    """

    def __init__(self, state_dir: Path, read_only: bool = False):
        self.state_dir = Path(state_dir)
        self.path = self.state_dir / "probe-stats.sqlite3"
        if read_only:
            self._connection = sqlite3.connect(self.path.resolve().as_uri() + "?mode=ro", uri=True, timeout=5.0)
        else:
            self.state_dir.mkdir(parents=True, exist_ok=True)
            self._connection = sqlite3.connect(str(self.path), timeout=5.0)
            self._initialise_schema()

    def _initialise_schema(self) -> None:
        # No error is swallowed here or in the write/report paths.  The manager
        # can therefore surface storage failures instead of silently losing data.
        with self._connection:
            self._connection.executescript(
                """
                CREATE TABLE IF NOT EXISTS probe_stats_daily (
                    day TEXT NOT NULL,
                    account_id INTEGER NOT NULL,
                    model TEXT NOT NULL,
                    source TEXT NOT NULL,
                    attempts INTEGER NOT NULL DEFAULT 0,
                    http_200 INTEGER NOT NULL DEFAULT 0,
                    state_292 INTEGER NOT NULL DEFAULT 0,
                    target_hits INTEGER NOT NULL DEFAULT 0,
                    persisted INTEGER NOT NULL DEFAULT 0,
                    errors INTEGER NOT NULL DEFAULT 0,
                    header_ms_total INTEGER NOT NULL DEFAULT 0,
                    PRIMARY KEY (day, account_id, model, source)
                );

                CREATE TABLE IF NOT EXISTS probe_stats_status_daily (
                    day TEXT NOT NULL,
                    account_id INTEGER NOT NULL,
                    model TEXT NOT NULL,
                    source TEXT NOT NULL,
                    status INTEGER NOT NULL,
                    count INTEGER NOT NULL DEFAULT 0,
                    PRIMARY KEY (day, account_id, model, source, status)
                );
                """
            )

    def close(self) -> None:
        self._connection.close()

    def __del__(self) -> None:
        """Avoid leaking a short-lived CLI/test connection at interpreter exit."""
        try:
            self._connection.close()
        except (AttributeError, sqlite3.Error):
            # Destructors cannot report a useful operational failure.  Normal
            # init, write, and report paths deliberately still propagate it.
            pass

    def __enter__(self) -> "ProbeStats":
        return self

    def __exit__(self, exc_type: object, exc: object, traceback: object) -> None:
        self.close()

    def record_attempt(
        self,
        account: int,
        model: str,
        source: str,
        status: int,
        state_len: int,
        header_ms: int,
        target_hit: bool = False,
    ) -> None:
        """Atomically add one safe, aggregate-only probe observation.

        ``target_hit`` must only be passed after the caller validates the target
        state.  A target hit on a non-200 response is rejected so it cannot
        inflate the comparison report.
        """
        account_id = _require_int(account, "account")
        model_name = _require_model(model)
        source_name = _normalise_source(source)
        http_status = _require_int(status, "status")
        observed_state_len = _require_int(state_len, "state_len")
        observed_header_ms = _require_int(header_ms, "header_ms")
        if not isinstance(target_hit, bool):
            raise TypeError("target_hit must be a boolean")
        if target_hit and http_status != 200:
            raise ValueError("target_hit requires an HTTP 200 response")

        day = _utc_day()
        values = (
            day,
            account_id,
            model_name,
            source_name,
            1,
            int(http_status == 200),
            int(observed_state_len == 292),
            int(target_hit),
            int(http_status != 200),
            observed_header_ms,
        )
        with self._connection:
            self._connection.execute(
                """
                INSERT INTO probe_stats_daily (
                    day, account_id, model, source, attempts, http_200,
                    state_292, target_hits, errors, header_ms_total
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                ON CONFLICT(day, account_id, model, source) DO UPDATE SET
                    attempts = attempts + excluded.attempts,
                    http_200 = http_200 + excluded.http_200,
                    state_292 = state_292 + excluded.state_292,
                    target_hits = target_hits + excluded.target_hits,
                    errors = errors + excluded.errors,
                    header_ms_total = header_ms_total + excluded.header_ms_total
                """,
                values,
            )
            self._connection.execute(
                """
                INSERT INTO probe_stats_status_daily (
                    day, account_id, model, source, status, count
                ) VALUES (?, ?, ?, ?, ?, 1)
                ON CONFLICT(day, account_id, model, source, status) DO UPDATE SET
                    count = count + 1
                """,
                (day, account_id, model_name, source_name, http_status),
            )

    def record_persisted(self, account: int, model: str, source: str) -> None:
        """Atomically count one database-validated state save for this UTC day."""
        account_id = _require_int(account, "account")
        model_name = _require_model(model)
        source_name = _normalise_source(source)
        day = _utc_day()
        with self._connection:
            self._connection.execute(
                """
                INSERT INTO probe_stats_daily (
                    day, account_id, model, source, persisted
                ) VALUES (?, ?, ?, ?, 1)
                ON CONFLICT(day, account_id, model, source) DO UPDATE SET
                    persisted = persisted + 1
                """,
                (day, account_id, model_name, source_name),
            )

    def report(
        self,
        start_day: Optional[Union[str, date]] = None,
        end_day: Optional[Union[str, date]] = None,
    ) -> Dict[str, Any]:
        """Return JSON-safe totals, source groups, dimension groups, and days.

        Dates are inclusive UTC ``YYYY-MM-DD`` values.  With no dates supplied,
        the report spans every recorded day.  All reported rates are fractions;
        zero-attempt groups use ``0.0`` rather than ``None``.
        """
        start = _coerce_day(start_day, "start_day")
        end = _coerce_day(end_day, "end_day")
        if start is not None and end is not None and start > end:
            raise ValueError("start_day must not be after end_day")

        where = []
        parameters = []
        if start is not None:
            where.append("day >= ?")
            parameters.append(start)
        if end is not None:
            where.append("day <= ?")
            parameters.append(end)
        predicate = f" WHERE {' AND '.join(where)}" if where else ""

        # SELECT does not reliably begin a transaction under sqlite3's legacy
        # transaction control.  Start one explicitly so the counters and status
        # breakdown come from one snapshot when the daemon writes concurrently.
        try:
            self._connection.execute("BEGIN")
            rows = self._connection.execute(
                """
                SELECT day, account_id, model, source, attempts, http_200, state_292,
                       target_hits, persisted, errors, header_ms_total
                FROM probe_stats_daily
                """
                + predicate
                + " ORDER BY day, account_id, model, source",
                parameters,
            ).fetchall()
            status_rows = self._connection.execute(
                """
                SELECT day, account_id, model, source, status, count
                FROM probe_stats_status_daily
                """
                + predicate
                + " ORDER BY day, account_id, model, source, status",
                parameters,
            ).fetchall()
            self._connection.commit()
        except Exception:
            self._connection.rollback()
            raise

        statuses: Dict[tuple, Dict[int, int]] = {}
        for day, account_id, model_name, source_name, status, count in status_rows:
            key = (day, account_id, model_name, source_name)
            statuses.setdefault(key, {})[int(status)] = int(count)

        totals = _empty_counts()
        by_source: Dict[str, Dict[str, Any]] = {}
        by_combo: Dict[tuple, Dict[str, Any]] = {}
        by_day: Dict[str, Dict[str, Any]] = {}

        for row in rows:
            (
                day,
                account_id,
                model_name,
                source_name,
                attempts,
                http_200,
                state_292,
                target_hits,
                persisted,
                errors,
                header_ms_total,
            ) = row
            # All writes validate this label.  Rechecking preserves the report's
            # redaction boundary even if a local database was altered externally.
            source_name = _normalise_source(source_name)
            combo = (int(account_id), model_name, source_name)
            values = {
                "attempts": int(attempts),
                "http_200": int(http_200),
                "state_292": int(state_292),
                "target_hits": int(target_hits),
                "persisted": int(persisted),
                "errors": int(errors),
                "header_ms_total": int(header_ms_total),
                "status_counts": statuses.get((day, account_id, model_name, source_name), {}),
            }
            _add_counts(totals, values)
            _add_counts(by_source.setdefault(source_name, _empty_counts()), values)
            _add_counts(by_combo.setdefault(combo, _empty_counts()), values)

            day_groups = by_day.setdefault(
                day,
                {
                    "totals": _empty_counts(),
                    "by_source": {},
                    "by_combo": {},
                },
            )
            _add_counts(day_groups["totals"], values)
            _add_counts(
                day_groups["by_source"].setdefault(source_name, _empty_counts()), values
            )
            _add_counts(day_groups["by_combo"].setdefault(combo, _empty_counts()), values)

        def combinations(groups: Dict[tuple, Dict[str, Any]]) -> list:
            return [
                {
                    "account": account_id,
                    "model": model_name,
                    "source": source_name,
                    **_reported_counts(counts),
                }
                for (account_id, model_name, source_name), counts in sorted(groups.items())
            ]

        per_day = []
        for day in sorted(by_day):
            groups = by_day[day]
            per_day.append(
                {
                    "day": day,
                    "totals": _reported_counts(groups["totals"]),
                    "by_source": {
                        source_name: _reported_counts(counts)
                        for source_name, counts in sorted(groups["by_source"].items())
                    },
                    "by_account_model_source": combinations(groups["by_combo"]),
                }
            )

        observed_days = sorted(by_day)
        return {
            "date_range": {
                "start_day": start if start is not None else (observed_days[0] if observed_days else None),
                "end_day": end if end is not None else (observed_days[-1] if observed_days else None),
            },
            "totals": _reported_counts(totals),
            "by_source": {
                source_name: _reported_counts(counts)
                for source_name, counts in sorted(by_source.items())
            },
            "by_account_model_source": combinations(by_combo),
            "per_day": per_day,
        }
