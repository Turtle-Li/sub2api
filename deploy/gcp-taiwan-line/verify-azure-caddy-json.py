#!/usr/bin/env python3
"""Verify the security-relevant Azure Caddy JSON contract.

The input is either `caddy adapt` output or the active admin-API config.  The
script deliberately fingerprints only the listener and API reverse-proxy
properties which must remain identical across those two views; certificate
generation paths may legitimately differ after certificate activation.
"""

from __future__ import annotations

import fnmatch
import hashlib
import json
import re
import sys
from typing import Any


API_HOST = "api.turtleligpt.com"
EXPECTED_ALLOW = ["130.211.243.139/32"]
UPSTREAM_RE = re.compile(r"^sub2api(?:-(?:blue|green))?:8080$")
EXPECTED_REQUEST_HEADERS = {
    "delete": ["X-Real-IP", "CF-Connecting-IP"],
    "set": {"X-Forwarded-For": ["{http.request.remote.host}"]},
}
PROTECTED_HEADERS = {"x-forwarded-for", "x-real-ip", "cf-connecting-ip"}


def fail(message: str) -> "NoReturn":
    raise SystemExit(f"verify-azure-caddy-json: {message}")


def walk_nodes(value: Any):
    if isinstance(value, dict):
        yield value
        for child in value.values():
            yield from walk_nodes(child)
    elif isinstance(value, list):
        for child in value:
            yield from walk_nodes(child)


def route_hosts(route: dict[str, Any]) -> set[str]:
    hosts: set[str] = set()
    for matcher in route.get("match", []):
        if isinstance(matcher, dict):
            values = matcher.get("host", [])
            if isinstance(values, list):
                hosts.update(value for value in values if isinstance(value, str))
    return hosts


def route_is_proven_exclusive_of_api(route: dict[str, Any]) -> bool:
    """Return true only when every OR matcher excludes the production host."""

    matchers = route.get("match")
    if not isinstance(matchers, list) or not matchers:
        return False
    for matcher in matchers:
        if not isinstance(matcher, dict):
            return False
        hosts = matcher.get("host")
        if not isinstance(hosts, list) or not hosts:
            return False
        for pattern in hosts:
            if not isinstance(pattern, str):
                return False
            if fnmatch.fnmatchcase(API_HOST, pattern.lower()):
                return False
    return True


def verify(document: dict[str, Any]) -> dict[str, Any]:
    servers = (
        document.get("apps", {})
        .get("http", {})
        .get("servers", {})
    )
    if not isinstance(servers, dict):
        fail("apps.http.servers is missing")
    if len(servers) != 1:
        fail("expected exactly one explicitly configured HTTP server")

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
    if proxy_wrapper.get("timeout") != 2_000_000_000:
        fail("proxy_protocol timeout must remain exactly 2 seconds")
    if str(proxy_wrapper.get("fallback_policy", "")).upper() != "SKIP":
        fail("proxy_protocol fallback policy must be skip")
    if not isinstance(tls_wrapper, dict) or tls_wrapper != {"wrapper": "tls"}:
        fail("tls must be the second and final :443 listener wrapper")

    routes = server.get("routes", [])
    if not isinstance(routes, list):
        fail(":443 server routes must be a list")
    api_route_indexes = [
        index
        for index, route in enumerate(routes)
        if isinstance(route, dict) and API_HOST in route_hosts(route)
    ]
    if len(api_route_indexes) != 1:
        fail("expected exactly one route for the production API hostname")
    api_route_index = api_route_indexes[0]
    api_route = routes[api_route_index]
    if api_route.get("terminal") is not True:
        fail("production API route must be terminal")
    for route in routes[:api_route_index]:
        if not isinstance(route, dict) or not route_is_proven_exclusive_of_api(route):
            fail("an earlier route can intercept the production API hostname")

    api_nodes = list(walk_nodes(api_route.get("handle", [])))
    reverse_proxies = [node for node in api_nodes if node.get("handler") == "reverse_proxy"]
    if len(reverse_proxies) != 2:
        fail("production API route must contain exactly two reverse_proxy handlers")

    for node in api_nodes:
        if node.get("handler") != "headers":
            continue
        request = node.get("request")
        if request is None:
            continue
        serialized = json.dumps(request, sort_keys=True).lower()
        if any(name in serialized for name in PROTECTED_HEADERS):
            fail("standalone headers handler mutates a protected client-IP header")

    upstreams: set[str] = set()
    for handler in reverse_proxies:
        headers = handler.get("headers")
        request_headers = headers.get("request") if isinstance(headers, dict) else None
        if request_headers != EXPECTED_REQUEST_HEADERS:
            fail("reverse_proxy client-IP header policy does not match the frozen contract")
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
        "client_ip_header_policy": "overwrite-xff-from-connection-peer-drop-xreal-cf",
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
