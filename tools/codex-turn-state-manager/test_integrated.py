#!/usr/bin/env python3
"""Mock-only coverage for the integrated proxy-source adapter.

The fixtures use temporary state directories and harmless example hostnames.
They never load a production config, invoke curl, or contact a proxy/provider.
"""

import base64
from contextlib import redirect_stdout
import http.client
import io
import json
from pathlib import Path
import struct
import tempfile
import threading
import time
import unittest
from unittest import mock

import integrated


def valid_state(length=292):
    raw_length = length * 3 // 4
    raw = b"\x80" + struct.pack(">Q", int(time.time())) + b"x" * (raw_length - 9)
    result = base64.urlsafe_b64encode(raw).decode("ascii")
    if len(result) != length:
        raise AssertionError("invalid test state length")
    return result


def probe_result(state):
    return {
        "http_status": 200,
        "state": state,
        "state_len": len(state),
        "served_model": "",
        "header_ms": 1,
        "diagnostic": {"retryable": False, "source": "mock"},
    }


class FakeHost:
    def __init__(self):
        self.pins = {}
        self.write_calls = []

    def fetch_account(self, account_id, account_name):
        return {
            "token": "mock-token",
            "account": f"mock-account-{account_id}",
            "device": "mock-device",
            "version": "0.154.0",
        }

    def read_pinned_states(self, account_id):
        return dict(self.pins.get(int(account_id), {}))

    def write_pinned_state(self, account_id, model, state, expires_at_iso, state_len):
        self.write_calls.append((int(account_id), model, state_len))
        self.pins.setdefault(int(account_id), {})[model.lower()] = {
            "state": state,
            "state_len": state_len,
            "expires_at": expires_at_iso,
        }
        return True


class FakeResponse:
    def __init__(self, status, body):
        self.status = status
        self._body = body
        self.read_size = None

    def read(self, size):
        self.read_size = size
        return self._body


class FakeConnection:
    def __init__(self, response):
        self.response = response
        self.requests = []
        self.closed = False

    def request(self, method, target, headers):
        self.requests.append((method, target, headers))

    def getresponse(self):
        return self.response

    def close(self):
        self.closed = True


class IntegratedProxySourceTests(unittest.TestCase):
    def setUp(self):
        self._temporary_dirs = []
        self._proxy_host_validation_patch = mock.patch.object(
            integrated,
            "_validate_proxy_host",
            side_effect=lambda host, _port: host,
        )
        self._proxy_host_validation_patch.start()

    def tearDown(self):
        self._proxy_host_validation_patch.stop()
        for directory in reversed(self._temporary_dirs):
            directory.cleanup()

    def _manager(self, accounts=None, proxies=None):
        directory = tempfile.TemporaryDirectory()
        self._temporary_dirs.append(directory)
        root = Path(directory.name)
        state_dir = root / "project-state"
        config = {
            "state_dir": str(state_dir),
            "sub2api": {"container": "sub2api-test", "use_sudo": False},
            "alerts": {"enabled": False},
            "degraded": {"enabled": False},
            "accounts": accounts or [],
            "proxies": proxies or {"static_proxies": ["http://base-proxy.example:8080"], "sources": []},
            "failure_backoff_seconds": 1,
            "refresh_advance_minutes": 15,
            "max_probes_per_pass": 8,
            "proxy_cooldown_seconds": 1,
            "request_timeout_seconds": 1,
        }
        config_path = root / "integrated-config.json"
        config_path.write_text(json.dumps(config), encoding="utf-8")
        candidate = integrated.IntegratedStateManager(config_path)
        candidate.host = FakeHost()
        return candidate, root, state_dir, config_path

    @staticmethod
    def _source(name, source_type, content, **extra):
        payload = {"name": name, "type": source_type, "content": content}
        payload.update(extra)
        return payload

    def _server(self, candidate):
        handler = type("IntegratedTestHandler", (integrated.IntegratedPanelHandler,), {"manager": candidate})
        server = integrated.manager.ThreadingHTTPServer(("127.0.0.1", 0), handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        return server, thread

    def _request(self, connection, method, path, payload=None, headers=None):
        body = None if payload is None else json.dumps(payload)
        merged = dict(headers or {})
        if body is not None:
            merged.setdefault("Content-Type", "application/json")
        connection.request(method, path, body=body, headers=merged)
        response = connection.getresponse()
        return response.status, response.read().decode("utf-8")

    def test_panel_persists_private_overlay_without_leaking_or_mutating_pool(self):
        candidate, _root, state_dir, _config = self._manager()
        baseline = list(candidate.proxies)
        server, thread = self._server(candidate)
        mock_password = "mock-password-only"
        try:
            connection = http.client.HTTPConnection(*server.server_address, timeout=3)
            status, body = self._request(
                connection,
                "POST",
                "/api/proxy-sources",
                self._source(
                    "imported-static",
                    "static",
                    "proxy-one.example:8101:mock-user:" + mock_password + "\n"
                    "socks5h://proxy-two.example:8102",
                ),
                {"X-CTSM-Panel": "1"},
            )
            self.assertEqual(status, 201, body)
            self.assertNotIn(mock_password, body)
            self.assertNotIn("proxy-one.example", body)
            created = json.loads(body)["source"]
            self.assertEqual(created["count"], 2)
            self.assertEqual(candidate.proxies, baseline)

            status, listed = self._request(connection, "GET", "/api/proxy-sources")
            self.assertEqual(status, 200, listed)
            self.assertNotIn(mock_password, listed)
            self.assertNotIn("proxy-one.example", listed)
            self.assertEqual(json.loads(listed)["sources"][0]["id"], created["id"])

            overlay = state_dir / "proxy-sources.json"
            self.assertEqual(overlay.stat().st_mode & 0o777, 0o600)
            candidate._refresh_proxies()  # The maintenance thread's reload point.
            self.assertNotEqual(candidate.proxies, baseline)

            status, deleted = self._request(
                connection,
                "DELETE",
                "/api/proxy-sources/" + created["id"],
                headers={"X-CTSM-Panel": "1"},
            )
            self.assertEqual(status, 200, deleted)
            candidate._refresh_proxies()
            self.assertEqual(candidate.proxies, baseline)
            connection.close()
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=3)

    def test_static_rotating_and_disabled_sources_refresh_without_residue(self):
        candidate, _root, state_dir, _config = self._manager()
        static = candidate.create_proxy_source(self._source(
            "static-import", "static", "same-host.example:8201:mock-user:mock-pass\nother-host.example:8202"
        ))
        rotating = candidate.create_proxy_source(self._source(
            "rotating-import", "rotating", "gateway.example:9200:mock-user:mock-pass"
        ))
        with self.assertRaises(integrated.ProxySourceError):
            candidate.create_proxy_source(self._source(
                "bad-rotating", "rotating", "one.example:80\ntwo.example:80"
            ))

        candidate._refresh_proxies()
        static_proxy = next(proxy for proxy in candidate.proxies if "same-host.example" in proxy)
        self.assertIn(static_proxy, candidate.proxies)
        self.assertTrue(candidate._rotating)
        self.assertTrue(any("gateway.example" in proxy for proxy in candidate._dynamic_proxies))
        self.assertEqual(candidate._proxy_sources[static_proxy], "static-import")

        candidate._static_pending["1:model"] = [static_proxy]
        candidate._proxy_usage[static_proxy] = {"last_used": 1}
        self.assertTrue(candidate.delete_proxy_source(static["id"]))
        candidate._refresh_proxies()
        self.assertNotIn(static_proxy, candidate.proxies)
        self.assertNotIn(static_proxy, candidate._static_pending["1:model"])
        self.assertNotIn(static_proxy, candidate._proxy_usage)
        self.assertNotIn("mock-pass", (state_dir / "proxy-usage.json").read_text(encoding="utf-8"))

        envelope = integrated._read_private_json(state_dir / "proxy-sources.json")
        for source in envelope["sources"]:
            if source["id"] == rotating["id"]:
                source["enabled"] = False
        integrated._write_private_json(state_dir / "proxy-sources.json", envelope)
        candidate._refresh_proxies()
        self.assertFalse(any("gateway.example" in proxy for proxy in candidate.proxies))

    def test_extract_sources_are_bounded_cached_and_dropped_after_a_failed_refresh(self):
        candidate, _root, _state, _config = self._manager()
        source = candidate.create_proxy_source(self._source(
            "extract-import",
            "extract",
            "https://source.example/proxies",
            username="mock-user",
            password="mock-password",
        ))
        response = "\n".join(f"extract-{index}.example:{9000 + index}" for index in range(10))
        with mock.patch.object(candidate, "_fetch_extract_proxy_source", return_value=response) as fetch:
            candidate._refresh_proxies()
            # Reloading source metadata must not consume an extraction API
            # response while static candidates could still be selected.
            extracted = [proxy for proxy in candidate._dynamic_proxies if "extract-" in proxy]
            self.assertEqual(extracted, [])
            fetch.assert_not_called()

            candidate._activate_extract_sources_for_dynamic()
            fetch.assert_called_once()
            self.assertEqual(len([proxy for proxy in candidate._dynamic_proxies if "extract-" in proxy]), 8)

            # Cached results may be reused during this extraction phase, but
            # the provider is not called again until its bounded refresh is due.
            candidate._activate_extract_sources_for_dynamic()
            fetch.assert_called_once()
            self.assertEqual(len([proxy for proxy in candidate._dynamic_proxies if "extract-" in proxy]), 8)

        candidate._extract_next_fetch[source["id"]] = 0
        output = io.StringIO()
        with mock.patch.object(candidate, "_fetch_extract_proxy_source", side_effect=RuntimeError("mock-password")):
            with redirect_stdout(output):
                candidate._activate_extract_sources_for_dynamic()
        self.assertFalse(any("extract-" in proxy for proxy in candidate._dynamic_proxies))
        self.assertNotIn("mock-password", output.getvalue())
        self.assertIn("RuntimeError", output.getvalue())

    def test_empty_core_pool_never_promotes_or_retains_deleted_overlay_entries(self):
        candidate, _root, _state, _config = self._manager(
            proxies={"static_proxies": [], "sources": []}
        )
        self.assertEqual(candidate.proxies, [])
        created = candidate.create_proxy_source(self._source(
            "overlay-only", "static", "overlay-only.example:9111"
        ))
        candidate._refresh_proxies()
        self.assertEqual(len(candidate.proxies), 1)
        self.assertTrue(candidate._integrated_base_initialized)

        self.assertTrue(candidate.delete_proxy_source(created["id"]))
        candidate._refresh_proxies()
        self.assertEqual(candidate.proxies, [])

    def test_project_manager_lock_blocks_another_probe_owner(self):
        _candidate, _root, state_dir, _config = self._manager()
        with integrated._ManagerInstanceLock(state_dir):
            with self.assertRaises(integrated.IntegratedRuntimeError):
                with integrated._ManagerInstanceLock(state_dir):
                    pass

    def test_new_static_source_joins_an_active_round_before_dynamic_fallback(self):
        candidate, _root, _state, _config = self._manager(
            proxies={"static_proxies": ["http://initial-static.example:8111"], "sources": []}
        )
        candidate._begin_static_round()
        self.assertEqual(candidate._take_static_round_proxy(), "http://initial-static.example:8111")

        candidate.create_proxy_source(self._source(
            "mid-round-static", "static", "mid-round-static.example:8112"
        ))
        candidate._apply_proxy_source_overlay()
        self.assertEqual(candidate._take_static_round_proxy(), "http://mid-round-static.example:8112")

    def test_extract_validation_dns_guard_and_direct_transport_do_not_forward_defaults(self):
        for url in (
            "http://source.example/list",
            "https://localhost/list",
            "https://127.0.0.1/list",
            "https://169.254.169.254/list",
            "https://mock-user:mock-password@source.example/list",
        ):
            with self.assertRaises(integrated.ProxySourceError):
                integrated._validated_extract_url(url)

        with mock.patch.object(
            integrated.socket,
            "getaddrinfo",
            return_value=[(2, 1, 6, "", ("10.0.0.1", 443))],
        ):
            with self.assertRaises(integrated.ProxySourceFetchError):
                integrated._resolve_public_ips("source.example", 443)

        self._proxy_host_validation_patch.stop()
        try:
            with mock.patch.object(
                integrated.socket,
                "getaddrinfo",
                return_value=[(2, 1, 6, "", ("10.0.0.2", 8080))],
            ):
                with self.assertRaises(integrated.ProxySourceError):
                    integrated._normalized_proxy_endpoint("http://rebound.example:8080")
        finally:
            self._proxy_host_validation_patch.start()

        candidate, _root, _state, _config = self._manager()
        connection = FakeConnection(FakeResponse(200, b'{"data":[{"host":"returned.example","port":9400}]}'))
        record = {
            "content": "https://source.example/v1/list",
            "username": "mock-user",
            "password": "mock-password",
        }
        with mock.patch.object(integrated, "_resolve_public_ips", return_value=["8.8.8.8"]), mock.patch.object(
            integrated, "_VerifiedHTTPSConnection", return_value=connection
        ) as constructor:
            payload = candidate._fetch_extract_proxy_source(record)
        self.assertIn("returned.example", payload)
        constructor.assert_called_once_with("source.example", 443, ["8.8.8.8"], 10)
        headers = connection.requests[0][2]
        self.assertNotIn("mock-user", json.dumps(headers))
        self.assertNotIn("mock-password", json.dumps(headers))
        self.assertTrue(connection.closed)

        redirect = FakeConnection(FakeResponse(302, b""))
        with mock.patch.object(integrated, "_resolve_public_ips", return_value=["8.8.8.8"]), mock.patch.object(
            integrated, "_VerifiedHTTPSConnection", return_value=redirect
        ):
            with self.assertRaises(integrated.ProxySourceFetchError):
                candidate._fetch_extract_proxy_source(record)
        self.assertEqual(len(redirect.requests), 1)

    def test_static_hosts_are_unique_per_round_then_dynamic_without_continuation_reset(self):
        accounts = [
            {"id": account_id, "name": f"account-{account_id}", "models": [
                {"name": "model-a", "target_state_len": 292, "require_exact_len": True}
            ]}
            for account_id in (1, 2, 3)
        ]
        proxies = {
            "static_proxies": [
                "http://same-static.example:8001",
                "http://same-static.example:8002",
                "http://other-static.example:8001",
            ],
            "sources": [{
                "type": "rotating_residential",
                "name": "mock-gateway",
                "gateway": "dynamic-gateway.example:9000",
                "user": "mock-user",
                "password": "mock-password",
                "country": "us",
            }],
        }
        candidate, _root, _state, _config = self._manager(accounts, proxies)
        candidate._static_round_rng = __import__("random").Random(7)
        calls = []

        def wrong_state(proxy, _credentials, model, **_kwargs):
            calls.append((proxy, model, threading.get_ident()))
            return probe_result("x" * 312)

        with mock.patch.object(integrated.manager, "probe_turn_state", side_effect=wrong_state):
            with redirect_stdout(io.StringIO()):
                candidate.run_check_and_refresh()
        first_round = list(calls)
        static_calls = [entry for entry in first_round if "dynamic-gateway.example" not in entry[0]]
        dynamic_calls = [entry for entry in first_round if "dynamic-gateway.example" in entry[0]]
        self.assertEqual(len(static_calls), 2)
        self.assertEqual({integrated._proxy_host_identity(entry[0]) for entry in static_calls}, {
            "same-static.example", "other-static.example"
        })
        self.assertEqual(len(dynamic_calls), 1)
        self.assertEqual({entry[2] for entry in first_round}, {threading.get_ident()})
        self.assertTrue(candidate._static_round_active)

        calls.clear()
        with mock.patch.object(integrated.manager, "probe_turn_state", side_effect=wrong_state):
            with redirect_stdout(io.StringIO()):
                candidate.run_check_and_refresh()
        self.assertEqual(len(calls), 3)
        self.assertTrue(all("dynamic-gateway.example" in entry[0] for entry in calls))
        self.assertTrue(candidate._static_round_active)

        with mock.patch.object(integrated.manager, "probe_turn_state", return_value=probe_result(valid_state())):
            with redirect_stdout(io.StringIO()):
                candidate.run_check_and_refresh()
        self.assertFalse(candidate._static_round_active)

        calls.clear()
        with mock.patch.object(integrated.manager, "probe_turn_state", side_effect=wrong_state):
            with redirect_stdout(io.StringIO()):
                candidate.run_check_and_refresh(force=True)
        self.assertEqual(len([entry for entry in calls if "dynamic-gateway.example" not in entry[0]]), 2)

    def test_extract_only_dynamic_fallback_runs_after_the_last_static_host(self):
        accounts = [{
            "id": 1,
            "name": "account-1",
            "models": [{"name": "model-a", "target_state_len": 292, "require_exact_len": True}],
        }]
        candidate, _root, _state, _config = self._manager(
            accounts,
            {"static_proxies": ["http://static-first.example:8011"], "sources": []},
        )
        candidate.create_proxy_source(self._source(
            "extract-fallback", "extract", "https://source.example/proxies"
        ))
        calls = []

        def probe(proxy, _credentials, _model, **_kwargs):
            calls.append(proxy)
            if "static-first.example" in proxy:
                return probe_result("x" * 312)
            return probe_result(valid_state())

        with mock.patch.object(
            candidate,
            "_fetch_extract_proxy_source",
            return_value="extract-fallback.example:9011",
        ) as fetch, mock.patch.object(integrated.manager, "probe_turn_state", side_effect=probe):
            with redirect_stdout(io.StringIO()):
                candidate.run_check_and_refresh()
            self.assertEqual(calls, ["http://static-first.example:8011"])
            fetch.assert_not_called()
            self.assertTrue(candidate._continue_harvest)
            self.assertTrue(candidate._static_round_active)

            with redirect_stdout(io.StringIO()):
                candidate.run_check_and_refresh()

        self.assertEqual(calls, [
            "http://static-first.example:8011",
            "http://extract-fallback.example:9011",
        ])
        self.assertEqual(fetch.call_count, 1)
        self.assertFalse(candidate._continue_harvest)
        self.assertFalse(candidate._static_round_active)
        self.assertEqual(candidate.host.write_calls, [(1, "model-a", 292)])

    def test_explicit_runtime_target_is_required(self):
        candidate, _root, _state, config_path = self._manager()
        self.assertIsInstance(candidate, integrated.IntegratedStateManager)
        valid = json.loads(config_path.read_text(encoding="utf-8"))
        invalid_configurations = []

        missing_container = dict(valid)
        missing_container.pop("sub2api")
        invalid_configurations.append(missing_container)

        alerts_enabled = json.loads(json.dumps(valid))
        alerts_enabled["alerts"]["enabled"] = True
        invalid_configurations.append(alerts_enabled)

        degraded_enabled = json.loads(json.dumps(valid))
        degraded_enabled["degraded"]["enabled"] = True
        invalid_configurations.append(degraded_enabled)

        legacy_state_dir = dict(valid)
        legacy_state_dir["state_dir"] = str(integrated._LEGACY_STATE_DIR)
        invalid_configurations.append(legacy_state_dir)

        for invalid in invalid_configurations:
            config_path.write_text(json.dumps(invalid), encoding="utf-8")
            with self.assertRaises(integrated.IntegratedRuntimeError):
                integrated.IntegratedStateManager(config_path)


if __name__ == "__main__":
    unittest.main()
