import io
import json
import time
from contextlib import redirect_stdout
from datetime import datetime, timezone, timedelta
from pathlib import Path
import base64
import struct
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

    def read_pinned_states(self, account_id):
        return self.pinned_map.get(int(account_id), {})

    def write_pinned_state(self, account_id, model, state, expires_at_iso, state_len, cookie="", cookie_expires_at_iso=""):
        self.writes.append({
            "account_id": account_id,
            "model": model,
            "state": state,
            "cookie": cookie,
            "cookie_expires_at_iso": cookie_expires_at_iso,
        })
        if account_id not in self.pinned_map:
            self.pinned_map[account_id] = {}
        self.pinned_map[account_id][model.lower()] = {
            "state": state,
            "state_len": state_len,
            "cookie": cookie,
            "cookie_expires_at": cookie_expires_at_iso,
            "expires_at": expires_at_iso,
            "updated_at": datetime.now(timezone.utc).isoformat(),
        }
        return True


class TestRollingSession(TestCase):
    def setUp(self):
        self.state_dir = Path("/tmp/test_rolling_state_dir")
        self.state_dir.mkdir(parents=True, exist_ok=True)
        (self.state_dir / "probe-diagnostics.json").write_text("{}")
        (self.state_dir / "proxies.txt").write_text("http://1.1.1.1:8080\n")

    def tearDown(self):
        import shutil
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
            "max_session_age_seconds": 1200,
            "cookie_advance_seconds": 90,
            "failure_backoff_seconds": 30,
        }
        if config_updates:
            cfg.update(config_updates)
        mgr = StateManager.__new__(StateManager)
        mgr.config = cfg
        mgr.config_path = self.state_dir / "config.json"
        mgr._accounts_cache = cfg["accounts"]
        mgr._dynamic_proxies = []
        mgr._dynamic_indices = {}
        mgr._rotating = False
        mgr._proxy_sources = {"http://1.1.1.1:8080": "static"}
        mgr.proxies = ["http://1.1.1.1:8080"]
        mgr.failure_backoff_seconds = 30
        mgr.max_probes_per_pass = 50
        mgr.proxy_cooldown_seconds = 0
        mgr._proxy_usage = {}
        mgr._usage_lock = mock.MagicMock()
        mgr._retry_lock = mock.MagicMock()
        mgr._accounts_overlay_file = self.state_dir / "accounts.json"
        mgr._accounts_overlay_file.write_text("[]")
        mgr._overlay_lock = mock.MagicMock()
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
        mgr._session_start = {}
        mgr._session_proxy = {}
        mgr.stats = mock.MagicMock()
        mgr.notifier = mock.MagicMock()
        mgr._creds_cache = {60: {"token": "tok", "account": "acc", "version": "0.155.0"}}
        mgr._credentials = lambda acct: mgr._creds_cache[int(acct["id"])]
        mgr.account_advance_minutes = lambda acct: 2
        mgr.record_probe = mock.MagicMock()
        mgr._record_renewal_success = mock.MagicMock()
        mgr._stats_attempt = mock.MagicMock()
        return mgr

    def test_roll_session_success(self):
        mgr = self._make_manager()
        mgr.host = FakeHost()
        mgr._session_start[60] = time.time() - 300  # 5 minutes old (< 20m)

        state292 = valid_state(292)
        cookie = "__cflb=active123; __oailb=jwt123"

        with mock.patch.object(manager_module, "probe_turn_state", return_value=probe_result(200, state292, cookie=cookie)) as mock_probe:
            res = mgr.roll_session(
                account={"id": 60, "name": "test_account"},
                model_cfg={"name": "gpt-6-astra", "target_state_len": 292},
                current_state=state292,
                current_cookie=cookie,
            )
            self.assertIsNotNone(res)
            st, info, p = res
            self.assertEqual(len(st), 292)
            self.assertEqual(info["cookie"], cookie)
            # Verify probe was called with existing cookie and state
            mock_probe.assert_called_once()
            _, kwargs = mock_probe.call_args
            self.assertEqual(kwargs.get("cookie"), cookie)
            self.assertEqual(kwargs.get("turn_state"), state292)

    def test_roll_session_retires_when_exceeding_max_age(self):
        mgr = self._make_manager({"max_session_age_seconds": 1200})
        mgr.host = FakeHost()
        mgr._session_start[60] = time.time() - 1300  # Exceeds 1200s (20m)

        state292 = valid_state(292)
        with mock.patch.object(manager_module, "probe_turn_state") as mock_probe:
            res = mgr.roll_session(
                account={"id": 60, "name": "test_account"},
                model_cfg={"name": "gpt-6-astra", "target_state_len": 292},
                current_state=state292,
                current_cookie="__cflb=test",
            )
            self.assertIsNone(res)
            mock_probe.assert_not_called()
            self.assertNotIn(60, mgr._session_start)

    def test_roll_session_miss_falls_back(self):
        mgr = self._make_manager()
        mgr.host = FakeHost()
        mgr._session_start[60] = time.time() - 100

        state312 = valid_state(312)  # Returns wrong length / degraded
        with mock.patch.object(manager_module, "probe_turn_state", return_value=probe_result(200, state312)):
            res = mgr.roll_session(
                account={"id": 60, "name": "test_account"},
                model_cfg={"name": "gpt-6-astra", "target_state_len": 292},
                current_state=valid_state(292),
                current_cookie="__cflb=test",
            )
            self.assertIsNone(res)

    def test_run_check_and_refresh_dispatches_cookie_roll(self):
        # Cookie expires in 70s (< cookie_advance_seconds: 90s)
        now_utc = datetime.now(timezone.utc).replace(microsecond=0)
        cookie_exp = (now_utc + timedelta(seconds=70)).isoformat()
        ticket_exp = (now_utc + timedelta(hours=1)).isoformat()
        state292 = valid_state(292)

        pinned = {
            60: {
                "gpt-6-astra": {
                    "state": state292,
                    "state_len": 292,
                    "cookie": "__cflb=old_cookie",
                    "cookie_expires_at": cookie_exp,
                    "expires_at": ticket_exp,
                    "updated_at": now_utc.isoformat(),
                },
                "gpt-5.6-sol": {
                    "state": state292,
                    "state_len": 292,
                    "cookie": "__cflb=old_cookie",
                    "cookie_expires_at": cookie_exp,
                    "expires_at": ticket_exp,
                    "updated_at": now_utc.isoformat(),
                },
            }
        }
        mgr = self._make_manager()
        mgr.host = FakeHost(pinned)
        mgr._reload_settings = mock.MagicMock()

        new_cookie = "__cflb=fresh_cookie; __oailb=fresh_jwt"
        with mock.patch.object(mgr, "roll_session", return_value=(state292, {"valid": True, "is_expired": False, "length": 292, "expires_at_iso": ticket_exp, "expires_at": ticket_exp, "cookie": new_cookie, "cookie_expires_at": (now_utc + timedelta(seconds=210)).isoformat()}, "http://1.1.1.1:8080")) as mock_roll:
            with mock.patch.object(mgr, "harvest") as mock_harvest:
                with redirect_stdout(io.StringIO()):
                    mgr.run_check_and_refresh()

                # roll_session was called exactly ONCE to refresh the account cookie
                mock_roll.assert_called_once()
                # harvest was NOT called because roll succeeded!
                mock_harvest.assert_not_called()
                # Verify DB write occurred
                self.assertEqual(len(mgr.host.writes), 1)
                self.assertEqual(mgr.host.writes[0]["cookie"], new_cookie)
