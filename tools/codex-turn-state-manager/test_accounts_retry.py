#!/usr/bin/env python3
"""Mock-only coverage for account overlays and diagnostic retry handling.

This imports the local rotating test fixtures so no test can contact PostgreSQL,
the proxy gateway, or an upstream model endpoint.
"""

import copy
import io
import json
from pathlib import Path
import tempfile
import time
import unittest
from contextlib import redirect_stdout
from typing import Any, Dict, Iterable, List, Tuple
from unittest import mock

from test_rotating import FakeHost, FakeNotifier, manager_module, probe_result, valid_state


def diagnostic(status: int, retryable: bool) -> Dict[str, Any]:
    """A complete sanitized diagnostic payload a probe is allowed to return."""
    return {
        "category": "mock_retryable" if retryable else "mock_terminal",
        "retryable": retryable,
        "source": "unit-test",
        "http_status": status,
        "request_id": f"req-{status}",
        "error_code": "mock_error",
        "body_kind": "json",
    }


class PerAccountHost(FakeHost):
    """Expose a harmless account marker to the mock probe and record DB reads."""

    def __init__(self, *args: Any, **kwargs: Any) -> None:
        super().__init__(*args, **kwargs)
        self.read_calls: List[int] = []

    def fetch_account(self, account_id: int, account_name: str) -> Dict[str, Any]:
        credentials = super().fetch_account(account_id, account_name)
        credentials["account"] = f"test-account-{account_id}"
        return credentials

    def read_pinned_states(self, account_id: int) -> Dict[str, Dict[str, Any]]:
        self.read_calls.append(int(account_id))
        return super().read_pinned_states(account_id)


class AccountsAndRetryTests(unittest.TestCase):
    def setUp(self) -> None:
        self._temporary_dirs: List[tempfile.TemporaryDirectory] = []

    def tearDown(self) -> None:
        for directory in reversed(self._temporary_dirs):
            directory.cleanup()

    @staticmethod
    def _model(name: str, target_len: int = 292, **extra: Any) -> Dict[str, Any]:
        model = {
            "name": name,
            "target_state_len": target_len,
            "require_exact_len": True,
        }
        model.update(extra)
        return model

    @staticmethod
    def _account(account_id: int, name: str, models: List[Dict[str, Any]], **extra: Any) -> Dict[str, Any]:
        account = {"id": account_id, "name": name, "models": models}
        account.update(extra)
        return account

    @staticmethod
    def _dynamic_proxies() -> Dict[str, Any]:
        return {
            "sources": [
                {
                    "type": "rotating_residential",
                    "enabled": True,
                    "gateway": "rotating.invalid:1234",
                    "user": "rotation-user",
                    "password": "test-password",
                    "country": "us",
                }
            ]
        }

    def _manager(
        self,
        accounts: List[Dict[str, Any]],
        pins: Dict[int, Dict[str, Dict[str, Any]]] = None,
        write_results: Iterable[Any] = (),
    ) -> Tuple[Any, PerAccountHost, Path, Path]:
        directory = tempfile.TemporaryDirectory()
        self._temporary_dirs.append(directory)
        root = Path(directory.name)
        state_dir = root / "state"
        config_path = root / "config.json"
        config_path.write_text(
            json.dumps(
                {
                    "state_dir": str(state_dir),
                    "failure_backoff_seconds": 300,
                    "refresh_advance_minutes": 15,
                    "max_probes_per_pass": 8,
                    "proxy_cooldown_seconds": 900,
                    "request_timeout_seconds": 1,
                    "accounts": copy.deepcopy(accounts),
                    "proxies": self._dynamic_proxies(),
                }
            ),
            encoding="utf-8",
        )
        candidate = manager_module.StateManager(config_path)
        host = PerAccountHost(pins, write_results)
        candidate.host = host
        candidate.notifier = FakeNotifier()
        return candidate, host, state_dir, config_path

    @staticmethod
    def _slot(account_id: int, model: str) -> str:
        return f"{account_id}:{model}"

    @staticmethod
    def _account_models(accounts: List[Dict[str, Any]]) -> Dict[int, List[Dict[str, Any]]]:
        return {entry["id"]: entry.get("models", []) for entry in accounts}

    @staticmethod
    def _run(candidate: Any, **kwargs: Any) -> int:
        with redirect_stdout(io.StringIO()):
            return candidate.run_check_and_refresh(**kwargs)

    def test_accounts_overlay_hot_add_remove_disable_and_invalid_edit_keeps_last_good_view(self) -> None:
        baseline = [
            self._account(1, "alpha", [self._model("baseline-model")]),
            self._account(2, "bravo", [self._model("second-model")]),
        ]
        candidate, _host, state_dir, config_path = self._manager(baseline)
        baseline_file = config_path.read_text(encoding="utf-8")
        overlay_path = state_dir / "accounts.json"

        self.assertEqual([entry["id"] for entry in candidate.accounts()], [1, 2])
        first_valid = {
            "1": {"models": [self._model("custom.model:beta", 296)]},
            "3": {"name": "charlie", "models": [self._model("added-model", 292)]},
        }
        state_dir.mkdir(parents=True, exist_ok=True)
        overlay_path.write_text(json.dumps(first_valid), encoding="utf-8")
        merged = candidate.accounts()
        by_id = self._account_models(merged)
        self.assertEqual([model["name"] for model in by_id[1]], ["custom.model:beta"])
        self.assertEqual(by_id[1][0]["target_state_len"], 296)
        self.assertIn(3, by_id)
        self.assertEqual(config_path.read_text(encoding="utf-8"), baseline_file)
        self.assertEqual(candidate.config["accounts"], baseline)

        # Removing an overlay entry removes only that overlay account.
        overlay_path.write_text(
            json.dumps({"1": first_valid["1"]}), encoding="utf-8"
        )
        self.assertNotIn(3, self._account_models(candidate.accounts()))

        # Disabled models are removed from the active view; disabled accounts too.
        overlay_path.write_text(
            json.dumps({"1": {"models": [self._model("custom.model:beta", 296, enabled=False)]}}),
            encoding="utf-8",
        )
        self.assertEqual(self._account_models(candidate.accounts())[1], [])
        overlay_path.write_text(json.dumps({"1": {"enabled": False}}), encoding="utf-8")
        self.assertNotIn(1, self._account_models(candidate.accounts()))

        # Accept a known-good replacement, then prove an invalid edit cannot apply
        # even the otherwise valid account-1 part of that same document.
        overlay_path.write_text(json.dumps(first_valid), encoding="utf-8")
        expected = candidate.accounts()
        invalid = {
            "1": {"models": [self._model("replacement-model", 292)]},
            "3": {"name": "charlie", "models": [self._model("invalid model name", 292)]},
        }
        overlay_path.write_text(json.dumps(invalid), encoding="utf-8")
        self.assertEqual(candidate.accounts(), expected)

    def test_two_accounts_with_same_model_keep_errors_isolated_and_honor_target_length(self) -> None:
        model = "same-model"
        accounts = [
            self._account(1, "alpha", [self._model(model, 292)]),
            self._account(2, "bravo", [self._model(model, 296)]),
        ]
        candidate, host, _state_dir, _config = self._manager(accounts)
        calls: List[Tuple[str, str]] = []

        def mixed_probe(_proxy: str, credentials: Dict[str, Any], name: str, **_kwargs: Any) -> Dict[str, Any]:
            calls.append((credentials["account"], name))
            if credentials["account"] == "test-account-1":
                response = probe_result(403)
                response["diagnostic"] = diagnostic(403, retryable=True)
                return response
            response = probe_result(200, valid_state(296))
            response["diagnostic"] = diagnostic(200, retryable=False)
            return response

        with mock.patch.object(manager_module, "probe_turn_state", side_effect=mixed_probe), mock.patch.object(
            manager_module.time, "sleep"
        ):
            self.assertEqual(self._run(candidate), 1)

        first_slot, second_slot = self._slot(1, model), self._slot(2, model)
        self.assertEqual(candidate._error_streaks[first_slot], 1)
        self.assertNotIn(second_slot, candidate._error_streaks)
        self.assertEqual(calls, [("test-account-1", model), ("test-account-2", model)])
        self.assertEqual([(call["account_id"], call["state_len"]) for call in host.write_calls], [(2, 296)])

        # The first account cannot issue a second request before its own 2s slot
        # expires, and the successful sibling remains skipped as fresh.
        with mock.patch.object(manager_module, "probe_turn_state", side_effect=mixed_probe), mock.patch.object(
            manager_module.time, "sleep"
        ):
            self.assertEqual(self._run(candidate), 0)
        self.assertEqual(len(calls), 2)

    def test_retryable_403_escalates_2s_4s_300s_and_a_200_312_clears_streak(self) -> None:
        model = "retry-model"
        candidate, _host, _state_dir, _config = self._manager(
            [self._account(1, "alpha", [self._model(model)])]
        )
        slot = self._slot(1, model)
        responses: List[Dict[str, Any]] = []
        for _ in range(3):
            response = probe_result(403)
            response["diagnostic"] = diagnostic(403, retryable=True)
            responses.append(response)
        cleared = probe_result(200, valid_state(312))
        cleared["diagnostic"] = diagnostic(200, retryable=False)
        responses.append(cleared)
        calls: List[str] = []

        def staged_probe(_proxy: str, _credentials: Dict[str, Any], name: str, **_kwargs: Any) -> Dict[str, Any]:
            calls.append(name)
            return responses.pop(0)

        with mock.patch.object(manager_module, "probe_turn_state", side_effect=staged_probe), mock.patch.object(
            manager_module.time, "sleep"
        ):
            self.assertEqual(self._run(candidate), 0)
            self.assertEqual(candidate._error_streaks[slot], 1)
            self.assertIn(slot, candidate._short_retries)
            self.assertGreaterEqual(candidate._retry_after[slot] - time.time(), 1.5)
            self.assertTrue(candidate._continue_harvest)

            # A pending short retry must not be probed before its timestamp.
            self.assertEqual(self._run(candidate), 0)
            self.assertEqual(calls, [model])

            candidate._retry_after[slot] = 0
            self.assertEqual(self._run(candidate), 0)
            self.assertEqual(candidate._error_streaks[slot], 2)
            self.assertIn(slot, candidate._short_retries)
            self.assertGreaterEqual(candidate._retry_after[slot] - time.time(), 3.5)

            candidate._retry_after[slot] = 0
            self.assertEqual(self._run(candidate), 0)
            self.assertEqual(candidate._error_streaks[slot], 3)
            self.assertNotIn(slot, candidate._short_retries)
            self.assertGreaterEqual(candidate._retry_after[slot] - time.time(), 299)
            self.assertFalse(candidate._continue_harvest)

            candidate._retry_after[slot] = 0
            self.assertEqual(self._run(candidate), 0)

        self.assertEqual(calls, [model, model, model, model])
        self.assertNotIn(slot, candidate._error_streaks)
        self.assertNotIn(slot, candidate._short_retries)
        self.assertNotIn(slot, candidate._retry_after)
        self.assertTrue(candidate._continue_harvest)

    def test_unknown_403_and_retry_after_never_become_short_retries_or_force_bypassable(self) -> None:
        model = "retry-model"
        candidate, _host, _state_dir, _config = self._manager(
            [self._account(1, "alpha", [self._model(model)])]
        )
        slot = self._slot(1, model)
        with mock.patch.object(manager_module, "probe_turn_state", return_value=probe_result(403)) as probe, mock.patch.object(
            manager_module.time, "sleep"
        ):
            self.assertEqual(self._run(candidate), 0)
        self.assertEqual(probe.call_count, 1)
        self.assertNotIn(slot, candidate._error_streaks)
        self.assertNotIn(slot, candidate._short_retries)
        self.assertGreaterEqual(candidate._retry_after[slot] - time.time(), 299)
        self.assertFalse(candidate._continue_harvest)

        # A Retry-After response uses its longer explicit delay, never the
        # transient 2s/4s staircase; --force cannot evade an existing wait.
        candidate, _host, _state_dir, _config = self._manager(
            [self._account(1, "alpha", [self._model(model)])]
        )
        response = probe_result(429)
        response.update({"retry_after": "900", "diagnostic": diagnostic(429, retryable=True)})
        with mock.patch.object(manager_module, "probe_turn_state", return_value=response) as probe, mock.patch.object(
            manager_module.time, "sleep"
        ):
            before = time.time()
            self.assertEqual(self._run(candidate), 0)
            self.assertEqual(probe.call_count, 1)
            self.assertNotIn(slot, candidate._error_streaks)
            self.assertNotIn(slot, candidate._short_retries)
            self.assertGreaterEqual(candidate._retry_after[slot], before + 899)
            self.assertEqual(self._run(candidate, force=True), 0)
            self.assertEqual(probe.call_count, 1)

    def test_filters_only_probe_the_requested_account_and_model(self) -> None:
        accounts = [
            self._account(1, "alpha", [self._model("model-a"), self._model("model-b")]),
            self._account(2, "bravo", [self._model("model-a"), self._model("custom.model:beta", 296)]),
        ]
        candidate, host, _state_dir, _config = self._manager(accounts)
        calls: List[Tuple[str, str]] = []

        def target_probe(_proxy: str, credentials: Dict[str, Any], name: str, **_kwargs: Any) -> Dict[str, Any]:
            calls.append((credentials["account"], name))
            response = probe_result(200, valid_state(296))
            response["diagnostic"] = diagnostic(200, retryable=False)
            return response

        with mock.patch.object(manager_module, "probe_turn_state", side_effect=target_probe), mock.patch.object(
            manager_module.time, "sleep"
        ):
            self.assertEqual(
                self._run(candidate, only_account=2, only_model="custom.model:beta"), 1
            )

        self.assertEqual(calls, [("test-account-2", "custom.model:beta")])
        self.assertEqual(host.read_calls, [2])
        self.assertEqual(
            [(call["account_id"], call["model"], call["state_len"]) for call in host.write_calls],
            [(2, "custom.model:beta", 296)],
        )

    def test_credentials_cache_refreshes_at_600_seconds(self) -> None:
        account = self._account(1, "alpha", [self._model("model-a")])
        candidate, host, _state_dir, _config = self._manager([account])
        with mock.patch.object(manager_module.time, "time", side_effect=[1000, 1599, 1600, 1600]):
            first = candidate._credentials(account)
            second = candidate._credentials(account)
            third = candidate._credentials(account)

        self.assertEqual(first, second)
        self.assertEqual(second, third)
        self.assertEqual(host.fetch_calls, [(1, "alpha"), (1, "alpha")])


if __name__ == "__main__":
    unittest.main(verbosity=2)
