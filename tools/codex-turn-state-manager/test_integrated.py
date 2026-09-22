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
            self.assertIn(created["id"], [row["id"] for row in json.loads(listed)["sources"]])

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





    def test_base_inventory_is_readonly_and_get_does_not_resolve_or_probe(self):
        candidate, root, _state, _config = self._manager(proxies={
            "static_proxies": [f"http://static-{n}.example:8080" for n in range(10)],
            "sources": [{"type":"rotating_residential", "name":"missing", "endpoint_file":"absent.json"},
                        {"type":"rotating_residential", "enabled":False, "name":"disabled"}]})
        source = candidate.create_proxy_source(self._source("managed", "rotating", "http://user:private-canary@gateway.example:8080"))
        with mock.patch.object(integrated.socket, "getaddrinfo", side_effect=AssertionError("GET did DNS")), mock.patch.object(integrated.manager, "probe_turn_state", side_effect=AssertionError("GET did probe")):
            rows = candidate.list_proxy_sources()
        self.assertEqual(rows[0]["endpoint_count"], 10)
        self.assertTrue(rows[0]["read_only"])
        self.assertEqual(next(r for r in rows if r["name"]=="missing")["status"], "unavailable")
        self.assertEqual(next(r for r in rows if r["name"]=="disabled")["status"], "disabled")
        self.assertEqual(next(r for r in rows if r["id"]==source["id"])["status"], "pending")
        self.assertNotIn("private-canary", json.dumps(rows))
        self.assertNotIn("gateway.example", json.dumps(rows))
        self.assertNotIn(str(root), json.dumps(rows))

    def test_enable_overlay_waits_for_worker_and_keeps_baseline_and_history(self):
        candidate, _root, _state, _config = self._manager()
        baseline = list(candidate.proxies)
        source = candidate.create_proxy_source(self._source("managed", "rotating", "gateway.example:8080"))
        candidate._apply_proxy_source_overlay()
        self.assertEqual(len(candidate.proxies), 2)
        candidate.stats.record_attempt(9, "a", "managed", 200, 312, 1)
        candidate.set_proxy_source_enabled(source["id"], False)
        self.assertEqual(len(candidate.proxies), 2)  # HTTP thread never changes routing.
        self.assertEqual(candidate.list_proxy_sources()[-1]["status"], "pending")
        candidate._apply_proxy_source_overlay()
        self.assertEqual(candidate.proxies, baseline)
        self.assertEqual(candidate.list_proxy_sources()[-1]["status"], "disabled")
        self.assertEqual(candidate.source_stats()["by_source"]["managed"]["attempts"], 1)
        candidate.set_proxy_source_enabled(source["id"], True)
        candidate._apply_proxy_source_overlay()
        self.assertEqual(candidate.list_proxy_sources()[-1]["status"], "loaded")
        with self.assertRaises(integrated.ProxySourceError): candidate.set_proxy_source_enabled("base-static", False)
        with self.assertRaises(integrated.ProxySourceError): candidate.set_proxy_source_enabled(source["id"], "false")

    def test_invalid_overlay_is_visible_but_worker_keeps_last_valid_pool(self):
        candidate, _root, state, _config = self._manager()
        source = candidate.create_proxy_source(self._source("managed", "static", "added.example:8080"))
        candidate._apply_proxy_source_overlay()
        before = list(candidate.proxies)
        (state / "proxy-sources.json").write_text('{broken')
        with self.assertRaises(integrated.ProxySourceError): candidate.list_proxy_sources()
        candidate._apply_proxy_source_overlay()
        self.assertEqual(candidate.proxies, before)
        with self.assertRaises(integrated.ProxySourceError): candidate.set_proxy_source_enabled(source["id"], False)

    def test_own_per_model_static_first_and_backoff_are_preserved(self):
        account = {"id":9,"name":"test","models":[{"name":"a"},{"name":"b"}]}
        candidate, _root, _state, _config = self._manager(accounts=[account], proxies={"static_proxies":["http://s1.example:80","http://s2.example:80"],"sources":[]})
        candidate.create_proxy_source(self._source("dynamic", "rotating", "dyn.example:8080"))
        candidate._apply_proxy_source_overlay()
        calls = []
        def probe(proxy, creds, model, **kwargs):
            calls.append((model, proxy))
            return probe_result(valid_state(312))
        with mock.patch.object(integrated.manager, "probe_turn_state", side_effect=probe):
            for _ in range(3):
                for model in account["models"]: candidate.harvest(account, model)
        self.assertEqual([proxy for model, proxy in calls if model=="a"], candidate.proxies)
        self.assertEqual([proxy for model, proxy in calls if model=="b"], candidate.proxies)
        candidate._retry_after["9:a"] = time.time()+300
        with mock.patch.object(integrated.manager, "probe_turn_state") as probe:
            candidate.run_check_and_refresh(force=True, only_account=9, only_model="a")
        probe.assert_not_called()

    def test_extract_waits_for_per_model_static_exhaustion(self):
        account={"id":9,"name":"test","models":[{"name":"a"}]}
        candidate, _root, _state, _config = self._manager(accounts=[account])
        candidate.create_proxy_source(self._source("extract", "extract", "https://provider.example/ips"))
        candidate._apply_proxy_source_overlay()
        with mock.patch.object(candidate, "_fetch_extract_proxy_source", return_value="extracted.example:8080") as fetch, mock.patch.object(integrated.manager, "probe_turn_state", return_value=probe_result(valid_state(312))) as probe:
            candidate.harvest(account, account["models"][0])
            fetch.assert_not_called()
            candidate._apply_proxy_source_overlay()  # Continuation cannot reset the consumed static queue.
            candidate.harvest(account, account["models"][0])
            fetch.assert_called_once()
            self.assertIn("extracted.example", probe.call_args.args[0])

    def test_source_toggle_http_requires_csrf_and_writable_mode(self):
        candidate, _root, _state, _config = self._manager()
        source = candidate.create_proxy_source(self._source("managed", "rotating", "gateway.example:8080"))
        server, thread = self._server(candidate)
        try:
            connection = http.client.HTTPConnection(*server.server_address, timeout=3)
            path = "/api/proxy-sources/"+source["id"]+"/enabled"
            self.assertEqual(self._request(connection,"POST",path,{"enabled":False})[0],403)
            candidate.config["panel"]={"read_only":True}
            self.assertEqual(self._request(connection,"POST",path,{"enabled":False},{"X-CTSM-Panel":"1"})[0],403)
            candidate.config["panel"]={"read_only":False}
            self.assertEqual(self._request(connection,"POST",path,{"enabled":False},{"X-CTSM-Panel":"1","Origin":"https://untrusted.example"})[0],403)
            self.assertEqual(self._request(connection,"POST",path,{"enabled":False},{"X-CTSM-Panel":"1"})[0],200)
            connection.close()
        finally:
            server.shutdown(); server.server_close(); thread.join(timeout=3)


    def test_authenticated_admin_listener_keeps_tailnet_view_readonly(self):
        candidate, root, _state, _config = self._manager()
        candidate.config["panel"] = {"enabled": False, "read_only": True}
        token = root / "admin-token"
        token.write_text("synthetic-service-token-not-a-secret\n")
        token.chmod(0o600)
        handler = type("AuthenticatedTestHandler", (integrated.AuthenticatedPanelHandler,), {"manager": candidate, "token_file": token})
        server = integrated.manager.ThreadingHTTPServer(("127.0.0.1", 0), handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True); thread.start()
        headers = {"Authorization": "Bearer synthetic-service-token-not-a-secret", "X-CTSM-Panel": "1"}
        try:
            connection = http.client.HTTPConnection(*server.server_address, timeout=3)
            self.assertEqual(self._request(connection,"GET","/api/state")[0],401)
            self.assertEqual(self._request(connection,"GET","/api/state",headers={"Authorization":"Bearer wrong"})[0],401)
            status, body = self._request(connection,"GET","/api/state",headers=headers)
            self.assertEqual(status,200)
            self.assertFalse(json.loads(body)["read_only"])
            self.assertTrue(candidate.snapshot()["read_only"])
            self.assertNotIn("synthetic-service-token",body)
            self.assertEqual(self._request(connection,"GET","/",headers=headers)[0],404)
            source=candidate.create_proxy_source(self._source("managed", "rotating", "gateway.example:8080"))
            path="/api/proxy-sources/"+source["id"]+"/enabled"
            self.assertEqual(self._request(connection,"POST",path,{"enabled":False},headers)[0],200)
            self.assertEqual(self._request(connection,"POST",path,{"enabled":True},{"Authorization":headers["Authorization"]})[0],403)
            self.assertEqual(self._request(connection,"POST",path,{"enabled":True},{**headers,"Origin":"http://untrusted.example"})[0],403)
            token.write_text("rotated-service-token-not-a-secret")
            self.assertEqual(self._request(connection,"GET","/api/state",headers=headers)[0],401)
            self.assertEqual(self._request(connection,"GET","/api/state",headers={"Authorization":"Bearer rotated-service-token-not-a-secret"})[0],200)
            token.unlink()
            self.assertEqual(self._request(connection,"GET","/api/state",headers=headers)[0],401)
            connection.close()
        finally:
            server.shutdown(); server.server_close(); thread.join(timeout=3)

    def test_admin_token_rejects_unsafe_files_and_values(self):
        _candidate, root, _state, _config = self._manager()
        token=root/'token'
        for content in ('', 'short', 'x'*4097, 'first-line\nsecond-line-secret', 'unicode-中文-value'):
            token.write_text(content);token.chmod(0o600)
            with self.assertRaises((ValueError, UnicodeError)):integrated._admin_token(token)
        token.write_text('synthetic-service-token-not-a-secret');token.chmod(0o644)
        with self.assertRaises(ValueError):integrated._admin_token(token)
        token.chmod(0o600)
        link=root/'link';link.symlink_to(token)
        with self.assertRaises(OSError):integrated._admin_token(link)

    def test_admin_listener_refuses_public_wildcard_and_missing_token(self):
        candidate, root, _state, _config = self._manager()
        token=root/'token';token.write_text('synthetic-service-token-not-a-secret');token.chmod(0o600)
        for bind in ('0.0.0.0','8.8.8.8','169.254.169.254','localhost','::'):
            candidate.config['admin_panel']={'enabled':True,'bind':bind,'token_file':str(token)}
            with mock.patch.object(integrated.manager,'ThreadingHTTPServer') as server:
                with self.assertRaises(ValueError):integrated.start_integrated_panels(candidate)
                server.assert_not_called()
        candidate.config['admin_panel']={'enabled':True,'bind':'127.0.0.1','token_file':str(root/'absent')}
        with self.assertRaises(OSError):integrated.start_integrated_panels(candidate)


    def test_random_static_pool_is_consumed_once_before_dynamic(self):
        account={"id":9,"name":"test","models":[{"name":"a"}]}
        statics=[f"http://static-{i}.example:80" for i in range(10)]
        candidate, _root, _state, _config = self._manager(accounts=[account],proxies={"static_proxies":statics,"sources":[]})
        candidate.config["static_proxy_order"]="random"
        candidate.create_proxy_source(self._source("dynamic", "rotating", "gateway.example:8080"))
        candidate._apply_proxy_source_overlay()
        with mock.patch.object(integrated.manager.random.SystemRandom, "shuffle", side_effect=lambda values: values.reverse()) as shuffle, mock.patch.object(integrated.manager, "probe_turn_state", return_value=probe_result(valid_state(312))) as probe:
            for _ in range(11):candidate.harvest(account,account["models"][0])
        self.assertEqual([call.args[0] for call in probe.call_args_list[:10]],list(reversed(statics)))
        self.assertIn("gateway.example",probe.call_args_list[10].args[0])
        shuffle.assert_called_once()


    def test_settings_validate_persist_override_accounts_and_preserve_backoff(self):
        account={"id":9,"name":"test","refresh_advance_minutes":20,"models":[{"name":"a"}]}
        candidate, root, state_dir, config = self._manager(accounts=[account])
        self.assertEqual(candidate.settings_snapshot()['refresh_advance_minutes'],15)
        for invalid in (0,31,True,1.5,'5',None,float('nan')):
            with self.assertRaises(ValueError):candidate.update_settings({'refresh_advance_minutes':invalid})
        candidate._retry_after['9:a']=time.time()+200
        candidate.update_settings({'refresh_advance_minutes':5})
        self.assertEqual(candidate.account_advance_minutes(account),5)
        self.assertEqual((state_dir/'settings.json').stat().st_mode & 0o777,0o600)
        with mock.patch.object(integrated.manager,'probe_turn_state') as probe:
            candidate.run_check_and_refresh(force=True)
        probe.assert_not_called()
        again=integrated.IntegratedStateManager(config)
        self.assertEqual(again.settings_snapshot()['refresh_advance_minutes'],5)
        (state_dir/'settings.json').write_text('{broken')
        self.assertIsNotNone(candidate.settings_snapshot()['error'])
        self.assertEqual(candidate.account_advance_minutes(account),5)
        with self.assertRaises(ValueError):candidate.update_settings({'refresh_advance_minutes':3})

    def test_average_measures_complete_cycle_across_retries_and_persists(self):
        account={"id":9,"name":"test","models":[{"name":"a"}]}
        candidate, _root, _state, config = self._manager(accounts=[account])
        candidate._rotating=True
        candidate._integrated_base_rotating=True
        candidate._harvest_retry_delay=0
        state=valid_state(292)
        result=(state,integrated.manager.inspect_turn_state(state),candidate.proxies[0])
        clock=[100.0]
        def harvest(*args, **kwargs):
            candidate._harvest_retry_delay=0
            return None if clock[0]==100 else result
        self.assertIsNone(candidate.renewal_timing_snapshot()['average_seconds'])
        with mock.patch.object(integrated.manager.time,'monotonic',side_effect=lambda:clock[0]),mock.patch.object(candidate,'harvest',side_effect=harvest):
            candidate.run_check_and_refresh()
            self.assertEqual(candidate.renewal_timing_snapshot()['samples'],0)
            clock[0]=130.0
            candidate.run_check_and_refresh()
        self.assertEqual(candidate.renewal_timing_snapshot()['samples'],1)
        self.assertEqual(candidate.renewal_timing_snapshot()['average_seconds'],30.0)
        again=integrated.IntegratedStateManager(config)
        self.assertEqual(again.renewal_timing_snapshot()['last_seconds'],30.0)
        self.assertEqual(again._renewal_started,{})

    def test_failed_write_and_removed_slot_do_not_count_as_success(self):
        account={"id":9,"name":"test","models":[{"name":"a"}]}
        candidate, _root, _state, _config = self._manager(accounts=[account])
        state=valid_state(292);result=(state,integrated.manager.inspect_turn_state(state),candidate.proxies[0])
        with mock.patch.object(candidate,'harvest',return_value=result),mock.patch.object(candidate.host,'write_pinned_state',return_value=False):
            candidate.run_check_and_refresh()
        self.assertEqual(candidate.renewal_timing_snapshot()['samples'],0)
        self.assertIn('9:a',candidate._renewal_started)
        candidate.config['accounts']=[]
        candidate.run_check_and_refresh()
        self.assertEqual(candidate._renewal_started,{})

    def test_settings_http_enforces_readonly_and_csrf(self):
        candidate, _root, _state, _config = self._manager()
        server,thread=self._server(candidate)
        try:
            connection=http.client.HTTPConnection(*server.server_address,timeout=3)
            self.assertEqual(self._request(connection,'GET','/api/settings')[0],200)
            self.assertEqual(self._request(connection,'POST','/api/settings',{'refresh_advance_minutes':5})[0],403)
            headers={'X-CTSM-Panel':'1'}
            candidate.config['panel']={'read_only':True}
            self.assertEqual(self._request(connection,'POST','/api/settings',{'refresh_advance_minutes':5},headers)[0],403)
            candidate.config['panel']={'read_only':False}
            self.assertEqual(self._request(connection,'POST','/api/settings',{'refresh_advance_minutes':5},headers)[0],200)
            self.assertEqual(self._request(connection,'POST','/api/settings',{'refresh_advance_minutes':True},headers)[0],400)
            self.assertEqual(candidate.settings_snapshot()['refresh_advance_minutes'],5)
            connection.close()
        finally:server.shutdown();server.server_close();thread.join(timeout=3)

    def test_target_hit_uses_the_same_saved_advance_window(self):
        account={"id":9,"name":"test","refresh_advance_minutes":20,"models":[{"name":"a"}]}
        candidate, _root, _state, _config = self._manager(accounts=[account])
        candidate.update_settings({'refresh_advance_minutes':5})
        info={'valid':True,'is_expired':False,'remaining_minutes':10}
        with mock.patch.object(integrated.manager,'inspect_turn_state',return_value=info):
            candidate._stats_attempt(account,account['models'][0],'static',probe_result(valid_state(292)))
        self.assertEqual(candidate.source_stats()['totals']['target_hits'],1)


if __name__ == "__main__":
    unittest.main()
