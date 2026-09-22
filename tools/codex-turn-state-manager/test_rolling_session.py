import base64
import copy
import io
import json
import shutil
import struct
import threading
import time
from contextlib import redirect_stdout
from datetime import datetime, timedelta, timezone
from pathlib import Path
from unittest import TestCase, mock

import manager as manager_module
from manager import StateManager, inspect_turn_state


def valid_state(length: int = 292, issued_at: int = None) -> str:
    if length % 4:
        raise ValueError("test state lengths must be a multiple of four")
    issued_at = int(time.time()) if issued_at is None else issued_at
    raw_len = length * 3 // 4
    raw = b"\x80" + struct.pack(">Q", issued_at) + b"x" * (raw_len - 9)
    return base64.urlsafe_b64encode(raw).decode("ascii")


def probe_result(status=200, state="", cookie="", expires_in=210):
    now_utc = datetime.now(timezone.utc).replace(microsecond=0)
    exp = (now_utc + timedelta(seconds=expires_in)).isoformat() if cookie else None
    return {
        "http_status": status,
        "state": state,
        "state_len": len(state),
        "cookie": cookie,
        "cookie_expires_at": exp,
        "served_model": "gpt-6-astra",
        "diagnostic": {"category": "ok", "retryable": False, "summary": "ok"},
        "header_ms": 100,
        "error": None,
    }


class FakeHost:
    def __init__(self, pinned_map=None):
        self.pinned_map = pinned_map or {}
        self.writes = []
        self.account_cookies = {}
        for account_id, models in self.pinned_map.items():
            for entry in models.values():
                if entry.get("cookie"):
                    self.account_cookies[int(account_id)] = {
                        "cookie": entry["cookie"],
                        "cookie_expires_at": entry.get("cookie_expires_at", ""),
                    }
                    break

    def read_pinned_states(self, account_id):
        result = copy.deepcopy(self.pinned_map.get(int(account_id), {}))
        account_cookie = self.account_cookies.get(int(account_id), {})
        cookie = account_cookie.get("cookie", "")
        expires_at = account_cookie.get("cookie_expires_at", "")
        if cookie and expires_at:
            for entry in result.values():
                if not entry.get("cookie_expires_at") or entry["cookie_expires_at"] < expires_at:
                    entry["cookie"] = cookie
                    entry["cookie_expires_at"] = expires_at
        return result

    def write_pinned_state(
        self,
        account_id,
        model,
        state,
        expires_at_iso,
        state_len,
        cookie="",
        cookie_expires_at_iso="",
    ):
        self.writes.append({
            "account_id": account_id,
            "model": model,
            "state": state,
            "cookie": cookie,
            "cookie_expires_at_iso": cookie_expires_at_iso,
        })
        entry = {
            "state": state,
            "state_len": state_len,
            "expires_at": expires_at_iso,
            "updated_at": datetime.now(timezone.utc).isoformat(),
        }
        if cookie:
            entry["cookie"] = cookie
            entry["cookie_expires_at"] = cookie_expires_at_iso
            self.account_cookies[int(account_id)] = {
                "cookie": cookie,
                "cookie_expires_at": cookie_expires_at_iso,
            }
            if account_id in self.pinned_map:
                for m_k, m_v in self.pinned_map[account_id].items():
                    if isinstance(m_v, dict) and m_k != model.lower():
                        m_v["cookie"] = cookie
                        if cookie_expires_at_iso:
                            m_v["cookie_expires_at"] = cookie_expires_at_iso
        self.pinned_map.setdefault(account_id, {})[model.lower()] = entry
        return True


class TestFreshCookieRefresh(TestCase):
    def setUp(self):
        self.state_dir = Path("/tmp/test_fresh_cookie_refresh")
        self.state_dir.mkdir(parents=True, exist_ok=True)
        (self.state_dir / "probe-diagnostics.json").write_text("{}")
        (self.state_dir / "proxies.txt").write_text(
            "http://1.1.1.1:8080\nhttp://2.2.2.2:8080\n"
        )

    def tearDown(self):
        shutil.rmtree(self.state_dir, ignore_errors=True)

    def _make_manager(self, config_updates=None):
        cfg = {
            "state_dir": str(self.state_dir),
            "proxies": {"sources": [{"type": "file", "path": str(self.state_dir / "proxies.txt")}]},
            "accounts": [
                {
                    "id": 60,
                    "name": "test_account",
                    "refresh_advance_minutes": 2,
                    "models": [
                        {"name": "gpt-6-astra", "target_state_len": 292, "require_exact_len": True},
                        {"name": "gpt-5.6-sol", "target_state_len": 292, "require_exact_len": True},
                    ],
                }
            ],
            "cookie_advance_seconds": 30,
            "failure_backoff_seconds": 30,
        }
        if config_updates:
            cfg.update(config_updates)

        mgr = StateManager.__new__(StateManager)
        mgr.config = cfg
        mgr.config_path = self.state_dir / "config.json"
        mgr.state_dir = self.state_dir
        mgr._accounts_cache = cfg["accounts"]
        mgr._dynamic_proxies = []
        mgr._dynamic_indices = {}
        mgr._rotating = False
        mgr._proxy_sources = {
            "http://1.1.1.1:8080": "static",
            "http://2.2.2.2:8080": "static",
        }
        mgr.proxies = list(mgr._proxy_sources)
        mgr.failure_backoff_seconds = 30
        mgr.max_probes_per_pass = 50
        mgr.proxy_cooldown_seconds = 0
        mgr._proxy_usage = {}
        mgr._proxy_state_file = self.state_dir / "proxy-usage.json"
        mgr._usage_lock = threading.Lock()
        mgr._retry_lock = threading.Lock()
        mgr._accounts_overlay_file = self.state_dir / "accounts.json"
        mgr._overlay_file = mgr._accounts_overlay_file
        mgr._accounts_overlay_file.write_text("{}")
        mgr._overlay_lock = threading.RLock()
        mgr._accepted_overlay = {}
        mgr._retry_after = {}
        mgr._short_retries = set()
        mgr._logged_skips = {}
        mgr._renewal_started = {}
        mgr._static_pending = {}
        mgr._forced_pending = set()
        mgr._probe_attempts = {}
        mgr._error_streaks = {}
        mgr._diagnostics = {}
        mgr._continue_harvest = False
        mgr._harvest_retry_delay = 0
        mgr.stats = mock.MagicMock()
        mgr.notifier = mock.MagicMock()
        mgr._creds_cache = {60: {"token": "tok", "account": "acc", "version": "0.155.0"}}
        mgr._credentials = lambda acct: mgr._creds_cache[int(acct["id"])]
        mgr.account_advance_minutes = lambda acct: 2
        mgr.record_probe = mock.MagicMock()
        mgr._record_renewal_success = mock.MagicMock()
        mgr._stats_attempt = mock.MagicMock()
        mgr.refresh_advance_minutes = 2
        mgr._settings_file = self.state_dir / "settings.json"
        mgr._settings_file.write_text(json.dumps({
            "refresh_advance_minutes": 2,
            "probing_enabled": True,
        }))
        mgr._settings_lock = threading.RLock()
        mgr._settings_override = None
        mgr._settings_error = False
        mgr._reload_settings()
        return mgr

    @staticmethod
    def _pinned(cookie="__cflb=old_cookie", cookie_expiry_seconds=20):
        now_utc = datetime.now(timezone.utc).replace(microsecond=0)
        ticket_exp = (now_utc + timedelta(hours=1)).isoformat()
        cookie_exp = (
            (now_utc + timedelta(seconds=cookie_expiry_seconds)).isoformat()
            if cookie_expiry_seconds is not None
            else ""
        )
        state = valid_state(292)
        return {
            60: {
                "gpt-6-astra": {
                    "state": state,
                    "state_len": 292,
                    "cookie": cookie,
                    "cookie_expires_at": cookie_exp,
                    "expires_at": ticket_exp,
                    "updated_at": now_utc.isoformat(),
                },
                "gpt-5.6-sol": {
                    "state": state,
                    "state_len": 292,
                    "cookie": cookie,
                    "cookie_expires_at": cookie_exp,
                    "expires_at": ticket_exp,
                    "updated_at": now_utc.isoformat(),
                },
            }
        }

    def test_cookie_due_uses_one_clean_harvest_and_requires_set_cookie(self):
        mgr = self._make_manager()
        mgr.host = FakeHost(self._pinned())
        state = valid_state(292)
        info = inspect_turn_state(state)
        info.update({
            "cookie": "__cflb=fresh; __oailb=fresh",
            "cookie_expires_at": (datetime.now(timezone.utc) + timedelta(seconds=210)).isoformat(),
        })

        with mock.patch.object(
            mgr,
            "harvest",
            return_value=(state, info, "http://1.1.1.1:8080"),
        ) as harvest:
            with redirect_stdout(io.StringIO()):
                updated = mgr.run_check_and_refresh()

        self.assertEqual(updated, 1)
        self.assertGreaterEqual(harvest.call_count, 1)
        self.assertTrue(harvest.call_args.kwargs["require_cookie"])
        self.assertEqual(len(mgr.host.writes), 1)
        self.assertEqual(mgr.host.writes[0]["cookie"], "__cflb=fresh; __oailb=fresh")
        self.assertFalse(hasattr(mgr, "roll_session"))

    def test_missing_or_invalid_cookie_metadata_triggers_clean_refresh(self):
        for cookie, expires in (("", None), ("__cflb=old", None)):
            with self.subTest(cookie=bool(cookie), expires=expires):
                mgr = self._make_manager()
                mgr.host = FakeHost(self._pinned(cookie=cookie, cookie_expiry_seconds=expires))
                value = "broken" if cookie else ""
                mgr.host.pinned_map[60]["gpt-6-astra"]["cookie_expires_at"] = value
                mgr.host.pinned_map[60]["gpt-5.6-sol"]["cookie_expires_at"] = value
                with mock.patch.object(mgr, "harvest", return_value=None) as harvest:
                    with redirect_stdout(io.StringIO()):
                        mgr.run_check_and_refresh()
                self.assertGreaterEqual(harvest.call_count, 1)
                self.assertTrue(harvest.call_args.kwargs["require_cookie"])
                self.assertEqual(mgr.host.writes, [])

    def test_cookie_refresh_rejects_292_without_set_cookie_and_never_sends_cookie(self):
        mgr = self._make_manager()
        mgr.host = FakeHost()
        state = valid_state(292)

        with mock.patch.object(
            manager_module,
            "probe_turn_state",
            side_effect=[
                probe_result(200, state),
                probe_result(200, state, cookie="__cflb=fresh; __oailb=fresh"),
            ],
        ) as probe:
            with redirect_stdout(io.StringIO()):
                result = mgr.harvest(
                    {"id": 60, "name": "test_account"},
                    {"name": "gpt-6-astra", "target_state_len": 292, "require_exact_len": True},
                    require_cookie=True,
                )

        self.assertIsNotNone(result)
        self.assertEqual(probe.call_count, 2)
        for call in probe.call_args_list:
            self.assertNotIn("cookie", call.kwargs)
            self.assertNotIn("turn_state", call.kwargs)
        self.assertEqual(result[1]["cookie"], "__cflb=fresh; __oailb=fresh")

    def test_cookie_refresh_rejects_non_target_even_when_model_length_is_not_exact(self):
        mgr = self._make_manager()
        mgr.host = FakeHost()
        state = valid_state(256)

        with mock.patch.object(
            manager_module,
            "probe_turn_state",
            side_effect=[
                probe_result(200, state, cookie="__cflb=wrong-node"),
                probe_result(200, valid_state(292), cookie="__cflb=fresh"),
            ],
        ) as probe:
            with redirect_stdout(io.StringIO()):
                result = mgr.harvest(
                    {"id": 60, "name": "test_account"},
                    {"name": "gpt-6-astra", "target_state_len": 292, "require_exact_len": False},
                    require_cookie=True,
                )

        self.assertEqual(probe.call_count, 2)
        self.assertEqual(result[1]["length"], 292)
        self.assertEqual(result[1]["cookie"], "__cflb=fresh")

    def test_ticket_only_non_target_does_not_replace_routing_cookie(self):
        mgr = self._make_manager()
        mgr.host = FakeHost()
        state = valid_state(256)
        with mock.patch.object(
            manager_module,
            "probe_turn_state",
            return_value=probe_result(200, state, cookie="__cflb=wrong-node"),
        ):
            with redirect_stdout(io.StringIO()):
                result = mgr.harvest(
                    {"id": 60, "name": "test_account"},
                    {"name": "gpt-6-astra", "target_state_len": 292, "require_exact_len": False},
                    require_cookie=False,
                )

        self.assertIsNotNone(result)
        self.assertEqual(result[1]["length"], 256)
        self.assertEqual(result[1]["cookie"], "")
        self.assertEqual(result[1]["cookie_expires_at"], "")

    def test_failed_cookie_refresh_preserves_existing_pin(self):
        pinned = self._pinned()
        mgr = self._make_manager()
        mgr.host = FakeHost(pinned)

        with mock.patch.object(mgr, "harvest", return_value=None) as harvest:
            with redirect_stdout(io.StringIO()):
                updated = mgr.run_check_and_refresh()

        self.assertEqual(updated, 0)
        self.assertTrue(harvest.call_args.kwargs["require_cookie"])
        self.assertEqual(mgr.host.writes, [])
        self.assertEqual(
            mgr.host.pinned_map[60]["gpt-6-astra"]["cookie"],
            "__cflb=old_cookie",
        )

    def test_ticket_only_refresh_can_keep_existing_account_cookie(self):
        pinned = self._pinned(cookie_expiry_seconds=180)
        mgr = self._make_manager({"cookie_advance_seconds": 30})
        mgr.host = FakeHost(pinned)
        state = valid_state(292)
        info = inspect_turn_state(state)
        info.update({"cookie": "", "cookie_expires_at": ""})

        with mock.patch.object(
            mgr,
            "harvest",
            return_value=(state, info, "http://1.1.1.1:8080"),
        ) as harvest:
            with redirect_stdout(io.StringIO()):
                updated = mgr.run_check_and_refresh(force=True, only_model="gpt-6-astra")

        self.assertEqual(updated, 1)
        self.assertFalse(harvest.call_args.kwargs["require_cookie"])
        self.assertEqual(mgr.host.writes[0]["cookie"], "")
        self.assertEqual(
            mgr.host.read_pinned_states(60)["gpt-6-astra"]["cookie"],
            "__cflb=old_cookie",
        )

    def test_cookie_due_model_runs_before_a_different_ticket_due_model(self):
        pinned = self._pinned(cookie_expiry_seconds=180)
        pinned[60]["gpt-6-astra"]["state"] = valid_state(
            292, issued_at=int(time.time()) - 3500
        )
        pinned[60]["gpt-5.6-sol"]["cookie_expires_at"] = (
            datetime.now(timezone.utc) + timedelta(seconds=20)
        ).isoformat()
        mgr = self._make_manager({"cookie_advance_seconds": 30})
        mgr.host = FakeHost(pinned)
        mgr.host.account_cookies = {}
        calls = []

        def harvest(_account, model_cfg, require_cookie=False):
            calls.append((model_cfg["name"], require_cookie))
            state = valid_state(292)
            info = inspect_turn_state(state)
            info.update({
                "cookie": "__cflb=fresh" if require_cookie else "",
                "cookie_expires_at": (
                    datetime.now(timezone.utc) + timedelta(seconds=210)
                ).isoformat() if require_cookie else "",
            })
            return state, info, "http://1.1.1.1:8080"

        with mock.patch.object(mgr, "harvest", side_effect=harvest):
            with redirect_stdout(io.StringIO()):
                first_updated = mgr.run_check_and_refresh()
                first_continues = mgr._continue_harvest
                second_updated = mgr.run_check_and_refresh()

        self.assertEqual(first_updated, 1)
        self.assertTrue(first_continues)
        self.assertEqual(second_updated, 1)
        self.assertEqual(calls, [
            ("gpt-5.6-sol", True),
            ("gpt-6-astra", False),
        ])

    def test_single_model_cookie_refresh_syncs_to_all_models_and_avoids_further_probes(self):
        pinned = self._pinned(cookie_expiry_seconds=10)
        mgr = self._make_manager({"cookie_advance_seconds": 60})
        mgr.host = FakeHost(pinned)

        probed_models = []

        def harvest_impl(_account, model_cfg, require_cookie=False, *args, **kwargs):
            m_name = model_cfg["name"]
            probed_models.append(m_name)
            state = valid_state(292)
            info = inspect_turn_state(state)
            info.update({
                "cookie": "__cflb=synced_cookie_123",
                "cookie_expires_at": (datetime.now(timezone.utc) + timedelta(seconds=150)).isoformat(),
            })
            return state, info, "http://1.1.1.1:8080"

        with mock.patch.object(mgr, "harvest", side_effect=harvest_impl):
            with redirect_stdout(io.StringIO()):
                updated = mgr.run_check_and_refresh()

        self.assertEqual(updated, 1)
        # Only the first candidate was probed (serial, no multi-IP concurrent spam)
        self.assertEqual(len(probed_models), 1)
        # Verify all models in FakeHost received the synced winner cookie
        states = mgr.host.read_pinned_states(60)
        self.assertEqual(states["gpt-6-astra"]["cookie"], "__cflb=synced_cookie_123")
        self.assertEqual(states["gpt-5.6-sol"]["cookie"], "__cflb=synced_cookie_123")

        # Second pass: since both models' cookies are now extended to 150s, no probes needed!
        with mock.patch.object(mgr, "harvest", side_effect=harvest_impl):
            with redirect_stdout(io.StringIO()):
                second_updated = mgr.run_check_and_refresh()
        self.assertEqual(second_updated, 0)
        self.assertEqual(len(probed_models), 1)


if __name__ == "__main__":
    import unittest

    unittest.main()
