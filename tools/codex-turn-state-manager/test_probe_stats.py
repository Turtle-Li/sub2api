#!/usr/bin/env python3
"""Focused tests for local aggregate source statistics."""

import importlib.util
from pathlib import Path
from tempfile import TemporaryDirectory
import unittest
from unittest import mock


HERE = Path(__file__).resolve().parent
HELPER_PATH = HERE / "probe_stats.py"
SPEC = importlib.util.spec_from_file_location("probe_stats_under_test", HELPER_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"could not load helper at {HELPER_PATH}")
probe_stats = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(probe_stats)


class ProbeStatsTests(unittest.TestCase):
    def test_persists_aggregate_and_reports_source_rates(self) -> None:
        with TemporaryDirectory() as directory:
            stats = probe_stats.ProbeStats(Path(directory) / "new-state-dir")
            self.assertTrue(stats.path.exists())

            stats.record_attempt(7, "custom/model", "Static", 200, 292, 25, True)
            stats.record_persisted(7, "custom/model", "static")
            stats.record_attempt(7, "custom/model", "static", 503, 0, 50)
            stats.record_attempt(8, "other-model", "rainproxy", 200, 312, 10)

            report = stats.report()
            self.assertEqual(report["totals"]["attempts"], 3)
            self.assertEqual(report["totals"]["http_200"], 2)
            self.assertEqual(report["totals"]["state_292"], 1)
            self.assertEqual(report["totals"]["target_hits"], 1)
            self.assertEqual(report["totals"]["persisted"], 1)
            self.assertEqual(report["totals"]["errors"], 1)
            self.assertEqual(report["totals"]["header_ms_total"], 85)
            self.assertAlmostEqual(report["totals"]["target_hit_rate"], 1 / 3)
            self.assertEqual(report["totals"]["status_counts"], {"200": 2, "503": 1})

            static = report["by_source"]["static"]
            self.assertEqual(static["attempts"], 2)
            self.assertEqual(static["target_hits"], 1)
            self.assertEqual(static["persisted"], 1)
            self.assertAlmostEqual(static["http_200_rate"], 0.5)
            self.assertAlmostEqual(static["error_rate"], 0.5)
            self.assertEqual(report["by_source"]["rainproxy"]["state_292"], 0)

            stats.close()
            reopened = probe_stats.ProbeStats(Path(directory) / "new-state-dir")
            self.assertEqual(reopened.report()["totals"]["attempts"], 3)
            self.assertEqual(reopened.report()["totals"]["persisted"], 1)
            reopened.close()

    def test_target_hit_and_persisted_save_are_distinct(self) -> None:
        with TemporaryDirectory() as directory:
            stats = probe_stats.ProbeStats(Path(directory))
            stats.record_attempt(11, "arbitrary/model-name", "proxora", 200, 292, 4, True)

            before_save = stats.report()["totals"]
            self.assertEqual(before_save["target_hits"], 1)
            self.assertEqual(before_save["persisted"], 0)

            stats.record_persisted(11, "arbitrary/model-name", "proxora")
            stats.record_attempt(11, "arbitrary/model-name", "proxora", 200, 292, 6)
            after_save = stats.report()["totals"]
            self.assertEqual(after_save["state_292"], 2)
            self.assertEqual(after_save["target_hits"], 1)
            self.assertEqual(after_save["persisted"], 1)
            self.assertAlmostEqual(after_save["persisted_per_target_hit_rate"], 1.0)
            stats.close()

    def test_groups_by_utc_day_and_filters_inclusive_date_range(self) -> None:
        with TemporaryDirectory() as directory:
            stats = probe_stats.ProbeStats(Path(directory))
            with mock.patch.object(probe_stats, "_utc_day", return_value="2026-09-17"):
                stats.record_attempt(1, "custom:model", "static", 200, 292, 1, True)
            with mock.patch.object(probe_stats, "_utc_day", return_value="2026-09-18"):
                stats.record_attempt(2, "custom:model", "rainproxy", 429, 0, 2)
                stats.record_persisted(2, "custom:model", "rainproxy")

            report = stats.report("2026-09-18", "2026-09-18")
            self.assertEqual(report["date_range"], {"start_day": "2026-09-18", "end_day": "2026-09-18"})
            self.assertEqual(report["totals"]["attempts"], 1)
            self.assertEqual(report["totals"]["persisted"], 1)
            self.assertEqual(len(report["per_day"]), 1)
            self.assertEqual(report["per_day"][0]["day"], "2026-09-18")
            self.assertEqual(
                report["by_account_model_source"][0]["account"], 2
            )
            self.assertEqual(report["by_account_model_source"][0]["source"], "rainproxy")
            stats.close()

    def test_rejects_unsafe_source_without_recording_it(self) -> None:
        with TemporaryDirectory() as directory:
            stats = probe_stats.ProbeStats(Path(directory))
            with self.assertRaises(ValueError):
                stats.record_attempt(1, "model", "https://user:token@example.test", 200, 292, 1)
            with self.assertRaises(ValueError):
                stats.record_persisted(1, "model", "x" * 65)
            self.assertEqual(stats.report()["totals"]["attempts"], 0)
            self.assertEqual(stats.report()["totals"]["persisted"], 0)
            stats.close()


if __name__ == "__main__":
    unittest.main()
