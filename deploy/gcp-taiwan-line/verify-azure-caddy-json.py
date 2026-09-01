#!/usr/bin/env python3
"""Verify the security-relevant Azure Caddy JSON contract.

The input is either `caddy adapt` output or the active admin-API config.  The
script deliberately fingerprints only the listener and API reverse-proxy
properties which must remain identical across those two views; certificate
generation paths may legitimately differ after certificate activation.
"""

from __future__ import annotations

import hashlib
import json
import re
import sys
from typing import Any


API_HOST = "api.turtleligpt.com"
EXPECTED_ALLOW = ["130.211.243.139/32"]
UPSTREAM_RE = re.compile(r"^sub2api(?:-(?:blue|green))?:8080$")


def fail(message: str) -> "NoReturn":
    raise SystemExit(f"verify-azure-caddy-json: {message}")


def walk_handles(value: Any):
    if isinstance(value, dict):
        if value.get("handler") == "reverse_proxy":
            yield value
        for child in value.values():
            yield from walk_handles(child)
    elif isinstance(value, list):
        for child in value:
            yield from walk_handles(child)


def route_hosts(route: dict[str, Any]) -> set[str]:
    hosts: set[str] = set()
    for matcher in route.get("match", []):
        if isinstance(matcher, dict):
            values = matcher.get("host", [])
            if isinstance(values, list):
                hosts.update(value for value in values if isinstance(value, str))
    return hosts


def verify(document: dict[str, Any]) -> dict[str, Any]:
    servers = (
        document.get("apps", {})
        .get("http", {})
        .get("servers", {})
    )
    if not isinstance(servers, dict):
        fail("apps.http.servers is missing")

    candidates: list[tuple[str, dict[str, Any]]] = []
    for name, server in servers.items():
        if not isinstance(server, dict):
            continue
        listen = server.get("listen", [])
        if isinstance(listen, list) and ":443" in listen:
            candidates.append((name, server))
    if len(candidates) != 1:
        fail("expected exactly one HTTP server listening on :443")

    server_name, server = candidates[0]
    if server.get("listen") != [":443"]:
        fail(":443 server has an unexpected additional listener")
    if server.get("protocols") != ["h1", "h2"]:
        fail(":443 server must enable only h1 and h2")
    if "trusted_proxies" in server or "client_ip_headers" in server:
        fail("HTTP-header proxy trust must stay disabled; PROXY v2 owns client identity")

    wrappers = server.get("listener_wrappers")
    if not isinstance(wrappers, list) or len(wrappers) != 2:
        fail(":443 server must contain exactly the proxy_protocol and tls wrappers")
    proxy_wrapper, tls_wrapper = wrappers
    if not isinstance(proxy_wrapper, dict) or proxy_wrapper.get("wrapper") != "proxy_protocol":
        fail("proxy_protocol must be the first :443 listener wrapper")
    if proxy_wrapper.get("allow") != EXPECTED_ALLOW:
        fail("proxy_protocol allowlist is not the frozen GCP /32")
    if str(proxy_wrapper.get("fallback_policy", "")).upper() != "SKIP":
        fail("proxy_protocol fallback policy must be skip")
    if not isinstance(tls_wrapper, dict) or tls_wrapper != {"wrapper": "tls"}:
        fail("tls must be the second and final :443 listener wrapper")

    api_routes = [
        route
        for route in server.get("routes", [])
        if isinstance(route, dict) and API_HOST in route_hosts(route)
    ]
    if len(api_routes) != 1:
        fail("expected exactly one route for the production API hostname")

    reverse_proxies = list(walk_handles(api_routes[0].get("handle", [])))
    if not reverse_proxies:
        fail("production API route has no reverse_proxy handler")

    upstreams: set[str] = set()
    for handler in reverse_proxies:
        # With no trusted_proxies and no explicit X-Forwarded-For rewrite,
        # Caddy v2.11.4 ignores client-supplied X-Forwarded-* values and derives
        # X-Forwarded-For from the connection peer restored by PROXY v2.
        headers = handler.get("headers")
        if headers not in (None, {}):
            request_headers = headers.get("request", {}) if isinstance(headers, dict) else {}
            serialized = json.dumps(request_headers, sort_keys=True).lower()
            if "x-forwarded-for" in serialized:
                fail("explicit X-Forwarded-For mutation is outside the reviewed contract")
        for upstream in handler.get("upstreams", []):
            if isinstance(upstream, dict) and isinstance(upstream.get("dial"), str):
                dial = upstream["dial"]
                if not UPSTREAM_RE.fullmatch(dial):
                    fail(f"unexpected API upstream: {dial}")
                upstreams.add(dial)
    if len(upstreams) != 1:
        fail("production API route must resolve to one application generation")

    return {
        "server": server_name,
        "listen": server["listen"],
        "protocols": server["protocols"],
        "listener_wrappers": wrappers,
        "api_host": API_HOST,
        "upstreams": sorted(upstreams),
        "xff_policy": "caddy-default-ignore-incoming-use-connection-peer",
    }


def main() -> None:
    try:
        document = json.load(sys.stdin)
    except (json.JSONDecodeError, OSError) as exc:
        fail(f"could not read Caddy JSON: {exc}")
    if not isinstance(document, dict):
        fail("Caddy JSON root must be an object")
    contract = verify(document)
    payload = json.dumps(contract, sort_keys=True, separators=(",", ":")).encode()
    print(hashlib.sha256(payload).hexdigest())


if __name__ == "__main__":
    main()
