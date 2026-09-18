#!/usr/bin/env python3
"""Mock-only coverage for the rotating-residential manager candidate.

The tests deliberately construct a manager from a throwaway config and replace
both the Sub2API host and the probe function.  They must never contact a proxy,
the upstream API, or PostgreSQL.
"""

import base64
import copy
import importlib.util
import io
import json
from pathlib import Path
import struct
import tempfile
import time
import unittest
from contextlib import redirect_stdout
from typing import Any, Dict, Iterable, List, Tuple
from unittest import mock


HERE = Path(__file__).resolve().parent
MANAGER_PATH = HERE / "manager.py"
SPEC = importlib.util.spec_from_file_location("rotating_manager_under_test", MANAGER_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"could not load candidate manager at {MANAGER_PATH}")
manager_module = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(manager_module)


def valid_state(length: int = 292, issued_at: int = None) -> str:
    """Build a timestamp-valid, Fernet-shaped token of an exact common length."""
    if length % 4:
        raise ValueError("test state lengths must be a multiple of four")
    issued_at = int(time.time()) if issued_at is None else issued_at
    raw_len = length * 3 // 4
    if raw_len < 9:
        raise ValueError("test state is too short")
    raw = b"\x80" + struct.pack(">Q", issued_at) + b"x" * (raw_len - 9)
    state = base64.urlsafe_b64encode(raw).decode("ascii")
    if len(state) != length:
        raise AssertionError(f"expected {length}, got {len(state)}")
    return state


def probe_result(status: int, state: str = "", error: str = None) -> Dict[str, Any]:
    return {
        "http_status": status,
        "state": state,
        "state_len": len(state),
        "served_model": "",
        "header_ms": 1,
        "error": error,
    }


class FakeNotifier:
    def __init__(self) -> None:
        self.sent: List[Tuple[str, str]] = []
        self.cleared: List[str] = []

    def send(self, key: str, message: str, force: bool = False) -> bool:
        self.sent.append((key, message))
        return True

    def clear(self, key: str) -> None:
        self.cleared.append(key)


class FakeHost:
    def __init__(
        self,
        pins: Dict[int, Dict[str, Dict[str, Any]]] = None,
        write_results: Iterable[Any] = (),
    ) -> None:
        self.pins = copy.deepcopy(pins or {})
        self.write_results = list(write_results)
        self.fetch_calls: List[Tuple[int, str]] = []
        self.write_calls: List[Dict[str, Any]] = []

    def fetch_account(self, account_id: int, account_name: str) -> Dict[str, Any]:
        self.fetch_calls.append((account_id, account_name))
        return {
            "token": "test-token",
            "account": "test-account",
            "device": "test-device",
            "version": "0.154.0",
        }

    def read_pinned_states(self, account_id: int) -> Dict[str, Dict[str, Any]]:
        return copy.deepcopy(self.pins.get(int(account_id), {}))

    def write_pinned_state(
        self,
        account_id: int,
        model: str,
        state: str,
        expires_at_iso: str,
        state_len: int,
    ) -> bool:
        self.write_calls.append(
            {
                "account_id": account_id,
                "model": model,
                "state": state,
                "expires_at_iso": expires_at_iso,
                "state_len": state_len,
            }
        )
        result = self.write_results.pop(0) if self.write_results else True
        if isinstance(result, BaseException):
            raise result
        if result:
            self.pins.setdefault(int(account_id), {})[model.lower()] = {
                "state": state,
                "state_len": state_len,
                "expires_at": expires_at_iso,
            }
        return bool(result)


class RotatingManagerTests(unittest.TestCase):
    def setUp(self) -> None:
        self._temporary_dirs: List[tempfile.TemporaryDirectory] = []

    def tearDown(self) -> None:
        for directory in reversed(self._temporary_dirs):
            directory.cleanup()

    def _account(self, models: List[Dict[str, Any]]) -> Dict[str, Any]:
        return {"id": 7, "name": "test-account", "models": models}

    def _model(
        self,
        name: str = "model-a",
        target_len: int = 292,
        require_exact: bool = True,
    ) -> Dict[str, Any]:
        return {
            "name": name,
            "target_state_len": target_len,
            "require_exact_len": require_exact,
        }

    def _config(
        self,
        accounts: List[Dict[str, Any]],
        proxies: Dict[str, Any],
        state_dir: str,
    ) -> Dict[str, Any]:
        return {
            "state_dir": state_dir,
            "failure_backoff_seconds": 300,
            "refresh_advance_minutes": 15,
            "max_probes_per_pass": 8,
            "proxy_cooldown_seconds": 900,
            "request_timeout_seconds": 1,
            "accounts": accounts,
            "proxies": proxies,
        }

    def _manager(
        self,
        accounts: List[Dict[str, Any]],
        proxies: Dict[str, Any],
        pins: Dict[int, Dict[str, Dict[str, Any]]] = None,
        write_results: Iterable[Any] = (),
    ) -> Tuple[Any, FakeHost, Path]:
        directory = tempfile.TemporaryDirectory()
        self._temporary_dirs.append(directory)
        root = Path(directory.name)
        config_path = root / "config.json"
        config_path.write_text(
            json.dumps(self._config(accounts, proxies, str(root / "state"))),
            encoding="utf-8",
        )
        candidate = manager_module.StateManager(config_path)
        host = FakeHost(pins, write_results)
        candidate.host = host
        candidate.notifier = FakeNotifier()
        return candidate, host, root

    def _dynamic_proxies(self, include_static: bool = False) -> Dict[str, Any]:
        config: Dict[str, Any] = {
            "sources": [
                {
                    "type": "rotating_residential",
                    "enabled": True,
                    "gateway": "rotating.invalid:1234",
                    "user": "rotation-user",
                    "password": "test-password",
                    "country": "us",
                    "session_count": 10,
                }
            ]
        }
        if include_static:
            config["static_proxies"] = ["http://static.invalid:8080"]
            config["sources"].append(
                {"type": "webshare_url", "enabled": True, "url": "https://ignored.invalid/list"}
            )
        return config

    @staticmethod
    def _slot(model: str = "model-a") -> str:
        return f"7:{model}"

    def _run_until_not_continuing(self, candidate: Any, force_first: bool = False) -> int:
        total = 0
        force = force_first
        for _ in range(32):
            with redirect_stdout(io.StringIO()):
                total += candidate.run_check_and_refresh(force=force)
            force = False
            if not candidate._continue_harvest:
                return total
        self.fail("manager continued harvesting for more than 32 mock rounds")

    def test_static_sources_are_loaded_before_sessionless_gateway(self) -> None:
        accounts = [self._account([self._model()])]
        with mock.patch.object(manager_module.urllib.request, "urlopen") as urlopen:
            urlopen.return_value.__enter__.return_value.read.return_value = b"http://download.invalid:8080\n"
            candidate, _host, root = self._manager(
                accounts, self._dynamic_proxies(include_static=True)
            )
        urlopen.assert_called_once()
        self.assertTrue(candidate._rotating)
        self.assertEqual(len(candidate.proxies), 3)
        self.assertIn("static.invalid", candidate.proxies[0])
        self.assertEqual(candidate._dynamic_proxies, [candidate.proxies[-1]])
        self.assertNotIn("-session-", candidate.proxies[-1])

        calls: List[Tuple[str, str]] = []

        def one_normal_miss(proxy: str, _creds: Dict[str, Any], model: str, **_kwargs: Any) -> Dict[str, Any]:
            calls.append((proxy, model))
            return probe_result(200, valid_state(312))

        with mock.patch.object(manager_module, "probe_turn_state", side_effect=one_normal_miss):
            with redirect_stdout(io.StringIO()):
                candidate.run_check_and_refresh()
        self.assertEqual(calls, [(candidate.proxies[0], "model-a")])
        self.assertEqual(candidate._proxy_usage, {})
        self.assertFalse((root / "state" / "proxy-usage.json").exists())

    def test_each_model_exhausts_static_once_then_dynamic_until_success(self):
        accounts = [self._account([self._model("a"), self._model("b")])]
        proxies = self._dynamic_proxies()
        proxies["static_proxies"] = ["http://s1.invalid:80", "http://s2.invalid:80"]
        candidate, host, _ = self._manager(accounts, proxies)
        calls = []
        def probe(proxy, creds, model, **kwargs):
            calls.append((proxy, model))
            return probe_result(200, valid_state(312))
        with mock.patch.object(manager_module, "probe_turn_state", side_effect=probe):
            for _ in range(4):
                candidate.run_check_and_refresh()
        for model in ("a", "b"):
            self.assertEqual([p for p,m in calls if m == model],
                [*proxies["static_proxies"], candidate._dynamic_proxies[0], candidate._dynamic_proxies[0]])
        with mock.patch.object(manager_module, "probe_turn_state", return_value=probe_result(200, valid_state(292))):
            self.assertEqual(candidate.run_check_and_refresh(), 2)
        self.assertEqual(candidate._static_pending, {})
        host.pins = {}
        calls.clear()
        with mock.patch.object(manager_module, "probe_turn_state", side_effect=probe):
            candidate.run_check_and_refresh()
        self.assertEqual(calls, [(proxies["static_proxies"][0], "a"), (proxies["static_proxies"][0], "b")])

    def test_restart_preserves_outstanding_server_rate_wait(self):
        candidate, host, root = self._manager([self._account([self._model()])], self._dynamic_proxies())
        now = int(time.time())
        (root / "state").mkdir(exist_ok=True)
        (root / "state" / "probe-diagnostics.json").write_text(json.dumps({
            "7:model-a": {"at": now, "http_status": 429, "retry_delay_seconds": 900}}))
        restored = manager_module.StateManager(root / "config.json")
        restored.host = host
        with mock.patch.object(manager_module, "probe_turn_state") as probe:
            restored.run_check_and_refresh(force=True)
        probe.assert_not_called()
        self.assertEqual(restored._retry_after["7:model-a"], now + 900)

    def test_two_dynamic_providers_alternate_for_each_model_after_static(self):
        account = self._account([self._model("a"), self._model("b")])
        cfg = self._dynamic_proxies()
        cfg["static_proxies"] = ["http://static.invalid:80"]
        candidate, host, root = self._manager([account], cfg)
        endpoint = root / "provider.json"
        endpoint.write_text(json.dumps({"proxy_url": "http://u:p_country-us@second.invalid:80"}))
        candidate.config["proxies"]["sources"].append({"type":"rotating_residential", "name":"proxora", "endpoint_file":str(endpoint)})
        candidate._refresh_proxies()
        self.assertEqual(len(candidate._dynamic_proxies), 2)
        calls=[]
        def probe(proxy, creds, model, **kwargs):
            calls.append((proxy,model))
            return probe_result(200, valid_state(312))
        with mock.patch.object(manager_module, "probe_turn_state", side_effect=probe):
            for _ in range(5): candidate.run_check_and_refresh()
        for model in ("a", "b"):
            self.assertEqual([p for p,m in calls if m==model],
                [cfg["static_proxies"][0], *candidate._dynamic_proxies, *candidate._dynamic_proxies])
        self.assertEqual(candidate._proxy_sources[candidate._dynamic_proxies[1]], "proxora")
        report=json.dumps(candidate.stats.report())
        self.assertNotIn("p_country-us", report)
        self.assertNotIn("test-token", report)
        self.assertIn("rainproxy", report)
        self.assertIn("proxora", report)

    def test_provider_file_missing_does_not_break_existing_source(self):
        candidate, host, root = self._manager([self._account([self._model()])], self._dynamic_proxies())
        candidate.config["proxies"]["sources"].append({"type":"rotating_residential", "name":"proxora", "endpoint_file":str(root/"missing")})
        candidate._refresh_proxies()
        self.assertEqual(len(candidate._dynamic_proxies), 1)

    def test_proxy_health_check_keeps_credentials_out_of_logs_and_argv(self):
        candidate, host, root = self._manager([self._account([self._model()])], self._dynamic_proxies())
        output=io.StringIO()
        with mock.patch.object(manager_module.subprocess, "run", return_value=mock.Mock(stdout="192.0.2.1")) as run:
            with redirect_stdout(output): candidate.test_proxies()
        for secret in ("rotation-user", "test-password"):
            self.assertNotIn(secret, output.getvalue())
            self.assertNotIn(secret, str(run.call_args.args))
        self.assertIn("test-password", run.call_args.kwargs["input"])
        self.assertNotIn("rotation-user", manager_module.mask_proxy(candidate.proxies[0]))

    def test_malformed_provider_file_does_not_break_existing_source(self):
        candidate, host, root = self._manager([self._account([self._model()])], self._dynamic_proxies())
        endpoint=root/"bad.json"
        candidate.config["proxies"]["sources"].append({"type":"rotating_residential", "name":"proxora", "endpoint_file":str(endpoint)})
        for raw in (42, "http://", "http://u:p@host:notaport", "http://u:p@host:80\nheader"):
            endpoint.write_text(json.dumps({"proxy_url":raw}))
            candidate._refresh_proxies()
            self.assertEqual(len(candidate._dynamic_proxies), 1)

    def test_target_hit_and_persisted_are_separate_on_database_failure(self):
        account=self._account([self._model()])
        candidate, host, root = self._manager([account], self._dynamic_proxies(), write_results=[False])
        with mock.patch.object(manager_module, "probe_turn_state", return_value=probe_result(200, valid_state(292))):
            candidate.run_check_and_refresh()
        totals=candidate.stats.report()["totals"]
        self.assertEqual(totals["attempts"], 1)
        self.assertEqual(totals["target_hits"], 1)
        self.assertEqual(totals["persisted"], 0)

    def test_static_only_exception_is_counted_and_keeps_backoff(self):
        candidate, host, root = self._manager([self._account([self._model()])],
            {"static_proxies":["http://static.invalid:80"],"sources":[]})
        with mock.patch.object(manager_module,"probe_turn_state",side_effect=OSError("mock failure")):
            candidate.run_check_and_refresh()
        totals=candidate.stats.report()["totals"]
        self.assertEqual(totals["attempts"],1)
        self.assertEqual(totals["errors"],1)
        self.assertGreater(candidate._retry_after["7:model-a"],time.time())

    def test_static_pool_retains_legacy_multi_proxy_harvest(self) -> None:
        accounts = [self._account([self._model()])]
        proxies = {
            "static_proxies": ["http://first.invalid:8001", "http://second.invalid:8002"],
            "sources": [],
        }
        candidate, _host, _root = self._manager(accounts, proxies)
        seen: List[str] = []
        responses = iter([probe_result(200, valid_state(312)), probe_result(200, valid_state(292))])

        def static_probe(proxy: str, _creds: Dict[str, Any], _model: str, **_kwargs: Any) -> Dict[str, Any]:
            seen.append(proxy)
            return next(responses)

        with mock.patch.object(manager_module, "probe_turn_state", side_effect=static_probe):
            with redirect_stdout(io.StringIO()):
                harvested = candidate.harvest(accounts[0], accounts[0]["models"][0])

        self.assertFalse(candidate._rotating)
        self.assertIsNotNone(harvested)
        self.assertEqual(seen, candidate.proxies)
        self.assertEqual(set(candidate._proxy_usage), {candidate._proxy_identity(p) for p in candidate.proxies})

    def test_seven_normal_312_misses_continue_without_a_300_second_backoff(self) -> None:
        accounts = [self._account([self._model()])]
        candidate, host, _root = self._manager(accounts, self._dynamic_proxies())
        responses = iter([probe_result(200, valid_state(312)) for _ in range(7)] + [probe_result(200, valid_state(292))])
        calls: List[str] = []

        def rotating_probe(_proxy: str, _creds: Dict[str, Any], model: str, **_kwargs: Any) -> Dict[str, Any]:
            calls.append(model)
            return next(responses)

        with mock.patch.object(manager_module, "probe_turn_state", side_effect=rotating_probe):
            with redirect_stdout(io.StringIO()):
                first_updated = candidate.run_check_and_refresh()
            self.assertEqual(first_updated, 0)
            self.assertTrue(candidate._continue_harvest)
            self.assertEqual(candidate._harvest_retry_delay, 0)
            self.assertLessEqual(candidate._retry_after.get(self._slot(), 0), time.time() + 1)
            remaining_updated = self._run_until_not_continuing(candidate)

        self.assertEqual(remaining_updated, 1)
        self.assertEqual(calls, ["model-a"] * 8)
        self.assertEqual(len(host.write_calls), 1)
        self.assertNotIn(self._slot(), candidate._retry_after)

    def test_normal_miss_moves_to_the_next_model_and_successes_skip_next_round(self) -> None:
        models = [self._model("model-a"), self._model("model-b")]
        accounts = [self._account(models)]
        candidate, host, _root = self._manager(accounts, self._dynamic_proxies())
        responses = iter(
            [
                probe_result(200, valid_state(312)),  # model-a: normal miss
                probe_result(200, valid_state(292)),  # model-b: success
                probe_result(200, valid_state(292)),  # next round: model-a success
            ]
        )
        calls: List[str] = []

        def rotating_probe(_proxy: str, _creds: Dict[str, Any], model: str, **_kwargs: Any) -> Dict[str, Any]:
            calls.append(model)
            return next(responses)

        with mock.patch.object(manager_module, "probe_turn_state", side_effect=rotating_probe):
            with redirect_stdout(io.StringIO()):
                first_updated = candidate.run_check_and_refresh()
            self.assertEqual(first_updated, 1)
            self.assertTrue(candidate._continue_harvest)
            with redirect_stdout(io.StringIO()):
                second_updated = candidate.run_check_and_refresh()

        self.assertEqual(second_updated, 1)
        self.assertFalse(candidate._continue_harvest)
        self.assertEqual(calls, ["model-a", "model-b", "model-a"])
        self.assertEqual([call["model"] for call in host.write_calls], ["model-b", "model-a"])

    def test_real_4xx_errors_wait_instead_of_rotating_until_one_works(self) -> None:
        for status in (400, 401, 403, 407, 429):
            with self.subTest(status=status):
                accounts = [self._account([self._model()])]
                candidate, _host, _root = self._manager(accounts, self._dynamic_proxies())
                candidate._creds_cache[7] = (time.time(), {"token": "cached"})
                calls: List[str] = []

                def rejected(_proxy: str, _creds: Dict[str, Any], _model: str, **_kwargs: Any) -> Dict[str, Any]:
                    calls.append("called")
                    return probe_result(status)

                with mock.patch.object(manager_module, "probe_turn_state", side_effect=rejected):
                    with redirect_stdout(io.StringIO()):
                        updated = candidate.run_check_and_refresh()

                self.assertEqual(updated, 0)
                self.assertEqual(calls, ["called"])
                self.assertFalse(candidate._continue_harvest)
                self.assertEqual(candidate._harvest_retry_delay, candidate.failure_backoff_seconds)
                self.assertGreater(candidate._retry_after[self._slot()], time.time())
                if status == 401:
                    self.assertNotIn(7, candidate._creds_cache)

    def test_429_retry_after_extends_the_wait_and_does_not_continue(self) -> None:
        accounts = [self._account([self._model()])]
        candidate, _host, _root = self._manager(accounts, self._dynamic_proxies())
        response = probe_result(429)
        response["retry_after"] = "900"
        before = time.time()

        with mock.patch.object(manager_module, "probe_turn_state", return_value=response) as probe:
            with redirect_stdout(io.StringIO()):
                updated = candidate.run_check_and_refresh()

        self.assertEqual(updated, 0)
        self.assertEqual(probe.call_count, 1)
        self.assertFalse(candidate._continue_harvest)
        self.assertGreaterEqual(candidate._harvest_retry_delay, 900)
        self.assertGreaterEqual(candidate._retry_after[self._slot()], before + 899)

    def test_transport_failure_sleeps_briefly_then_continues_on_a_new_round(self) -> None:
        accounts = [self._account([self._model()])]
        candidate, _host, _root = self._manager(accounts, self._dynamic_proxies())

        with mock.patch.object(
            manager_module,
            "probe_turn_state",
            return_value=probe_result(0, error="simulated transport failure"),
        ) as probe, mock.patch.object(manager_module.time, "sleep") as sleep:
            with redirect_stdout(io.StringIO()):
                updated = candidate.run_check_and_refresh()

        self.assertEqual(updated, 0)
        self.assertEqual(probe.call_count, 1)
        self.assertIn(mock.call(1), sleep.call_args_list)
        self.assertTrue(candidate._continue_harvest)
        self.assertEqual(candidate._harvest_retry_delay, 0)
        self.assertLessEqual(candidate._retry_after.get(self._slot(), 0), time.time() + 1)

    def test_fast_normal_miss_has_only_the_short_round_floor_before_continuing(self) -> None:
        accounts = [self._account([self._model()])]
        candidate, _host, _root = self._manager(accounts, self._dynamic_proxies())

        with mock.patch.object(
            manager_module,
            "probe_turn_state",
            return_value=probe_result(200, valid_state(312)),
        ), mock.patch.object(manager_module.time, "sleep") as sleep:
            with redirect_stdout(io.StringIO()):
                updated = candidate.run_check_and_refresh()

        self.assertEqual(updated, 0)
        self.assertTrue(candidate._continue_harvest)
        delays = [call.args[0] for call in sleep.call_args_list if call.args]
        self.assertTrue(any(0 < delay <= 0.1 for delay in delays), delays)

    def test_static_invalid_or_short_lifetime_not_written(self) -> None:
        for state in (valid_state(292, issued_at=int(time.time())-3601),
                      valid_state(292, issued_at=int(time.time())-3500),
                      valid_state(292)[:12]+'\x01'+valid_state(292)[12:]):
            candidate, host, _ = self._manager(
                [self._account([self._model(target_len=len(state))])],
                {'static_proxies':['http://static.invalid:8080']})
            with mock.patch.object(manager_module,'probe_turn_state',return_value=probe_result(state)), redirect_stdout(io.StringIO()):
                self.assertEqual(candidate.run_check_and_refresh(),0)
            self.assertEqual(host.write_calls,[])

    def test_expired_or_illegal_header_states_are_not_written(self) -> None:
        cases = [
            ("expired", valid_state(292, issued_at=int(time.time()) - 3601), 292),
            (
                "illegal-header",
                valid_state(292)[:12] + "\x01" + valid_state(292)[12:],
                293,
            ),
        ]
        for label, state, target_len in cases:
            with self.subTest(label=label):
                accounts = [self._account([self._model(target_len=target_len)])]
                candidate, host, _root = self._manager(accounts, self._dynamic_proxies())

                with mock.patch.object(
                    manager_module,
                    "probe_turn_state",
                    return_value=probe_result(200, state),
                ):
                    with redirect_stdout(io.StringIO()):
                        updated = candidate.run_check_and_refresh()

                self.assertEqual(updated, 0)
                self.assertEqual(host.write_calls, [])
                self.assertTrue(candidate._continue_harvest)
                self.assertEqual(candidate._harvest_retry_delay, 0)
                self.assertLessEqual(candidate._retry_after.get(self._slot(), 0), time.time() + 1)

    def test_write_failure_does_not_report_or_track_a_false_success(self) -> None:
        accounts = [self._account([self._model()])]
        candidate, host, _root = self._manager(
            accounts, self._dynamic_proxies(), write_results=[False]
        )
        with mock.patch.object(
            manager_module,
            "probe_turn_state",
            return_value=probe_result(200, valid_state(292)),
        ):
            with redirect_stdout(io.StringIO()):
                updated = candidate.run_check_and_refresh()

        self.assertEqual(updated, 0)
        self.assertEqual(len(host.write_calls), 1)
        self.assertNotIn("model-a", host.read_pinned_states(7))
        self.assertFalse(candidate._continue_harvest)
        self.assertGreater(candidate._retry_after[self._slot()], time.time())

    def test_force_tracks_a_fresh_pin_until_it_is_written_then_stops_forcing_successes(self) -> None:
        models = [self._model("model-a"), self._model("model-b")]
        fresh = valid_state(292)
        initial_pins = {
            7: {
                "model-a": {"state": fresh, "state_len": 292},
                "model-b": {"state": fresh, "state_len": 292},
            }
        }
        accounts = [self._account(models)]
        candidate, host, _root = self._manager(
            accounts, self._dynamic_proxies(), pins=initial_pins
        )
        responses = iter(
            [
                probe_result(200, valid_state(312)),  # forced model-a misses
                probe_result(200, valid_state(292)),  # forced model-b writes
                probe_result(200, valid_state(292)),  # pending model-a writes next round
            ]
        )
        calls: List[str] = []

        def rotating_probe(_proxy: str, _creds: Dict[str, Any], model: str, **_kwargs: Any) -> Dict[str, Any]:
            calls.append(model)
            return next(responses)

        with mock.patch.object(manager_module, "probe_turn_state", side_effect=rotating_probe):
            with redirect_stdout(io.StringIO()):
                first_updated = candidate.run_check_and_refresh(force=True)
            self.assertEqual(first_updated, 1)
            self.assertTrue(candidate._continue_harvest)
            self.assertEqual(candidate._forced_pending, {self._slot("model-a")})
            with redirect_stdout(io.StringIO()):
                second_updated = candidate.run_check_and_refresh(force=False)

        self.assertEqual(second_updated, 1)
        self.assertEqual(calls, ["model-a", "model-b", "model-a"])
        self.assertEqual([call["model"] for call in host.write_calls], ["model-b", "model-a"])
        self.assertEqual(candidate._forced_pending, set())
        self.assertFalse(candidate._continue_harvest)


    def test_http_overrides_do_not_change_auth_or_database_error_defaults(self):
        for status, expected in ((403, 30), (429, 30), (401, 300), (407, 300)):
            with self.subTest(status=status):
                candidate, _host, _root = self._manager(
                    [self._account([self._model()])], self._dynamic_proxies())
                candidate.config["http_error_backoff_seconds"] = {"403": 30, "429": 30}
                with mock.patch.object(manager_module, "probe_turn_state", return_value=probe_result(status)):
                    with redirect_stdout(io.StringIO()):
                        candidate.run_check_and_refresh()
                self.assertEqual(candidate._harvest_retry_delay, expected)
                self.assertFalse(candidate._continue_harvest)

    def test_http_override_preserves_longer_server_wait(self):
        candidate, _host, _root = self._manager(
            [self._account([self._model()])], self._dynamic_proxies())
        candidate.config["http_error_backoff_seconds"] = {"403": 30, "429": 30}
        response = probe_result(429)
        response["retry_after"] = "900"
        with mock.patch.object(manager_module, "probe_turn_state", return_value=response):
            with redirect_stdout(io.StringIO()):
                candidate.run_check_and_refresh()
        self.assertEqual(candidate._harvest_retry_delay, 900)

    def test_nonfinite_retry_after_cannot_pause_forever(self):
        candidate, _host, _root = self._manager(
            [self._account([self._model()])], self._dynamic_proxies())
        candidate.config["http_error_backoff_seconds"] = {"429": 30}
        response = probe_result(429)
        response["retry_after"] = "Infinity"
        with mock.patch.object(manager_module, "probe_turn_state", return_value=response):
            with redirect_stdout(io.StringIO()):
                candidate.run_check_and_refresh()
        self.assertEqual(candidate._harvest_retry_delay, 30)


if __name__ == "__main__":
    unittest.main(verbosity=2)
