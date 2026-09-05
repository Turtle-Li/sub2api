#!/usr/bin/env python3
"""Verify the effective Caddy route contract for the Sub2API application.

The check runs against ``caddy adapt`` output (or the Admin API document), not
the source Caddyfile. Matchers, ``handle`` groups, and terminal routes are only
unambiguous after Caddy has adapted them. The verifier is deliberately
fail-closed: a matcher or handler it cannot model is reported as unknown
instead of being treated as a non-match.
"""

from __future__ import annotations

import argparse
import fnmatch
import json
import re
import sys
from typing import Any, Union


DEFAULT_API_HOST = "api.turtleligpt.com"
# Keep the upstream grammar in one place. Azure applies the stricter
# ``allow_localhost=False`` policy because its candidate must never proxy to an
# arbitrary process on the host; the generic node-local template may use
# localhost during a standalone deployment.
APP_UPSTREAM_RE = re.compile(r"^(?:sub2api(?:-(?:blue|green))?|localhost):8080$")

# These are the paths used by the product and by Codex's image client. The task
# path is represented by a stable placeholder because the real value is a
# generated task id. Batch Image list is included separately from submit so a
# missing GET route cannot hide behind a passing POST route.
PROBE_PATHS: tuple[tuple[str, str], ...] = (
    ("POST", "/images/generations"),
    ("POST", "/images/generations/async"),
    ("POST", "/images/edits"),
    ("POST", "/images/edits/async"),
    ("GET", "/images/tasks/route-contract-probe"),
    ("POST", "/v1/images/generations"),
    ("POST", "/v1/images/edits"),
    ("POST", "/v1/images/generations/async"),
    ("POST", "/v1/images/edits/async"),
    ("GET", "/v1/images/tasks/route-contract-probe"),
    ("POST", "/v1/images/batches"),
    ("GET", "/v1/images/batches"),
)

PROXY = "proxy"
BLOCK = "block"
PASS = "pass"
UNKNOWN = "unknown"
# Use typing.Union instead of the PEP 604 ``|`` expression so the installed
# verifier can still be imported on Python 3.9 hosts.
MatchResult = Union[bool, str]


def _path_matches(pattern: str, path: str) -> bool:
    """Match the path forms emitted by Caddy's path matcher."""

    if pattern == path:
        return True
    if pattern.endswith("/*"):
        prefix = pattern[:-1]
        return path.startswith(prefix) and len(path) > len(prefix)
    # Caddy accepts glob characters in path matchers (including /foo*).
    if any(char in pattern for char in "*?["):
        return fnmatch.fnmatchcase(path, pattern)
    return False


_CONTENT_LENGTH_GUARD = re.compile(
    r"^\(?\{http\.request\.header\.content-length\}!=''"
    r"&&int\(\{http\.request\.header\.content-length\}\)>[0-9]+\)?$"
)


def _expression_matches_probe(expression: str) -> MatchResult:
    """Evaluate only the request-size expression used by this deployment.

    Contract probes intentionally have no Content-Length header, so the known
    non-empty-and-over-limit guard cannot match. Every other expression is
    unknown and therefore cannot prove that a later proxy is reachable.
    """

    normalized = re.sub(r"\s+", "", expression).lower()
    if _CONTENT_LENGTH_GUARD.fullmatch(normalized):
        return False
    return UNKNOWN


def _pattern_matches(patterns: Any, value: str) -> MatchResult:
    if not isinstance(patterns, list) or not patterns:
        return UNKNOWN
    saw_unknown = False
    for pattern in patterns:
        if not isinstance(pattern, str):
            saw_unknown = True
            continue
        if fnmatch.fnmatchcase(value.lower(), pattern.lower()):
            return True
    return UNKNOWN if saw_unknown else False


def _matcher_matches(matcher: dict[str, Any], host: str, method: str, path: str) -> MatchResult:
    if not isinstance(matcher, dict) or not matcher:
        return UNKNOWN
    result: MatchResult = True
    for key, value in matcher.items():
        current: MatchResult
        if key == "host":
            current = _pattern_matches(value, host)
        elif key == "path":
            if not isinstance(value, list) or not value:
                current = UNKNOWN
            else:
                saw_unknown = False
                current = False
                for pattern in value:
                    if not isinstance(pattern, str):
                        saw_unknown = True
                        continue
                    if _path_matches(pattern, path):
                        current = True
                        break
                if current is not True and saw_unknown:
                    current = UNKNOWN
        elif key == "method":
            if not isinstance(value, list) or not all(isinstance(item, str) for item in value):
                current = UNKNOWN
            else:
                current = method in value
        elif key == "not":
            if not isinstance(value, list) or not value:
                current = UNKNOWN
            else:
                child_results: list[MatchResult] = []
                for child in value:
                    if not isinstance(child, dict):
                        child_results.append(UNKNOWN)
                    else:
                        child_results.append(_matcher_matches(child, host, method, path))
                if any(item is True for item in child_results):
                    current = False
                elif any(item == UNKNOWN for item in child_results):
                    current = UNKNOWN
                else:
                    current = True
        elif key == "expression":
            expression = value.get("expr") if isinstance(value, dict) else value
            current = _expression_matches_probe(expression) if isinstance(expression, str) else UNKNOWN
        else:
            # Header, regexp, remote-IP, protocol, vars, and future matchers
            # can all alter route reachability. Do not guess their result.
            current = UNKNOWN

        if current is False:
            return False
        if current == UNKNOWN:
            result = UNKNOWN
    return result


def _route_matches(route: dict[str, Any], host: str, method: str, path: str) -> MatchResult:
    matchers = route.get("match")
    if matchers is None:
        return True
    if not isinstance(matchers, list) or not matchers:
        return UNKNOWN
    # Caddy represents OR-ed matcher sets as a list of objects. Each object is
    # an AND set for the common adapted JSON emitted by this deployment.
    results: list[MatchResult] = []
    for matcher_set in matchers:
        if not isinstance(matcher_set, dict):
            results.append(UNKNOWN)
        else:
            results.append(_matcher_matches(matcher_set, host, method, path))
    if any(item is True for item in results):
        return True
    if any(item == UNKNOWN for item in results):
        return UNKNOWN
    return False


def application_upstream_matches(dial: str, *, allow_localhost: bool = True) -> bool:
    """Return whether a dial is one of the reviewed application upstreams."""

    if not isinstance(dial, str) or not APP_UPSTREAM_RE.fullmatch(dial):
        return False
    return allow_localhost or not dial.startswith("localhost:")


def _proxy_is_application(handler: dict[str, Any], *, allow_localhost: bool = True) -> bool:
    upstreams = handler.get("upstreams")
    if not isinstance(upstreams, list) or not upstreams:
        return False
    for upstream in upstreams:
        if not isinstance(upstream, dict) or not isinstance(upstream.get("dial"), str):
            return False
        if not application_upstream_matches(upstream["dial"], allow_localhost=allow_localhost):
            return False
    return True


def _resolve_handlers(
    handlers: Any, host: str, method: str, path: str, *, allow_localhost: bool = True
) -> str:
    if not isinstance(handlers, list):
        return BLOCK
    for handler in handlers:
        if not isinstance(handler, dict):
            return BLOCK
        handler_name = handler.get("handler")
        if handler_name == "reverse_proxy":
            return PROXY if _proxy_is_application(handler, allow_localhost=allow_localhost) else BLOCK
        if handler_name == "subroute":
            result = _resolve_routes(
                handler.get("routes"), host, method, path, allow_localhost=allow_localhost
            )
            if result in (PROXY, BLOCK, UNKNOWN):
                return result
            continue
        if handler_name == "static_response":
            return BLOCK
        if handler_name in {"encode", "headers", "log_append", "request_body", "tracing", "metrics"}:
            # These handlers do not select a different route or change the
            # request path for the reviewed Caddy versions.
            continue
        # Rewrites, vars, authentication, and arbitrary future handlers can
        # change matching or terminate the request. Refuse to prove reachability.
        return BLOCK
    return PASS


def _resolve_routes(
    routes: Any, host: str, method: str, path: str, *, allow_localhost: bool = True
) -> str:
    if not isinstance(routes, list):
        return BLOCK
    selected_groups: set[str] = set()
    for route in routes:
        if not isinstance(route, dict):
            return UNKNOWN
        match = _route_matches(route, host, method, path)
        if match is False:
            continue
        if match == UNKNOWN:
            return UNKNOWN
        group = route.get("group")
        if group is not None:
            if not isinstance(group, str) or not group:
                return UNKNOWN
            if group in selected_groups:
                # ``handle`` groups are mutually exclusive after their first
                # matching route, even when that route's handlers fall through.
                continue
            selected_groups.add(group)
        result = _resolve_handlers(
            route.get("handle", []), host, method, path, allow_localhost=allow_localhost
        )
        if result in (PROXY, BLOCK, UNKNOWN):
            return result
        if route.get("terminal") is True:
            return BLOCK
    return PASS


def _route_hosts(route: dict[str, Any]) -> set[str]:
    hosts: set[str] = set()
    matchers = route.get("match", [])
    if not isinstance(matchers, list):
        return hosts
    for matcher in matchers:
        if isinstance(matcher, dict) and isinstance(matcher.get("host"), list):
            hosts.update(value.lower() for value in matcher["host"] if isinstance(value, str))
    return hosts


def _route_excludes_host(route: dict[str, Any], host: str) -> bool:
    """Prove that a route before the API route cannot match its host."""

    matchers = route.get("match")
    if not isinstance(matchers, list) or not matchers:
        return False
    for matcher_set in matchers:
        if not isinstance(matcher_set, dict):
            return False
        hosts = matcher_set.get("host")
        if not isinstance(hosts, list) or not hosts:
            return False
        for pattern in hosts:
            if not isinstance(pattern, str):
                return False
            if fnmatch.fnmatchcase(host.lower(), pattern.lower()):
                return False
    return True


def _is_https_listener(value: Any) -> bool:
    if not isinstance(value, str):
        return False
    # Accept Caddy's explicit IPv4/IPv6 listener forms while matching the
    # exact HTTPS port, not a substring such as :1443.
    return value == ":443" or value.endswith(":443")


def _server(document: dict[str, Any]) -> dict[str, Any]:
    servers = document.get("apps", {}).get("http", {}).get("servers", {})
    if not isinstance(servers, dict) or not servers:
        raise ValueError("apps.http.servers is missing")
    candidates = [
        server
        for server in servers.values()
        if isinstance(server, dict)
        and isinstance(server.get("listen"), list)
        and any(_is_https_listener(item) for item in server["listen"])
    ]
    if len(candidates) != 1:
        raise ValueError("expected exactly one HTTP server listening on :443")
    return candidates[0]


def _resolve_api_route(
    route: dict[str, Any], host: str, method: str, path: str, *, allow_localhost: bool
) -> str:
    match = _route_matches(route, host, method, path)
    if match is not True:
        return match if match == UNKNOWN else BLOCK
    return _resolve_handlers(
        route.get("handle", []), host, method, path, allow_localhost=allow_localhost
    )


def verify_document(
    document: dict[str, Any], api_host: str = DEFAULT_API_HOST, *, allow_localhost: bool = True
) -> dict[str, Any]:
    server = _server(document)
    routes = server.get("routes")
    if not isinstance(routes, list):
        raise ValueError(":443 server routes are missing")

    api_indexes = [
        index
        for index, route in enumerate(routes)
        if isinstance(route, dict)
        and any(
            isinstance(pattern, str) and fnmatch.fnmatchcase(api_host.lower(), pattern.lower())
            for pattern in _route_hosts(route)
        )
    ]
    if len(api_indexes) != 1:
        raise ValueError(f"expected exactly one route for {api_host}")
    api_index = api_indexes[0]
    for route in routes[:api_index]:
        if not isinstance(route, dict) or not _route_excludes_host(route, api_host):
            raise ValueError("an earlier Caddy route can intercept the production API hostname")

    api_route = routes[api_index]
    if api_route.get("terminal") is not True:
        raise ValueError("production API route must be terminal")
    for method, path in PROBE_PATHS:
        result = _resolve_api_route(
            api_route, api_host, method, path, allow_localhost=allow_localhost
        )
        if result != PROXY:
            detail = "unknown route matcher/handler" if result == UNKNOWN else "does not resolve"
            raise ValueError(f"{method} {path} {detail} to the Sub2API application")

    unsupported_result = _resolve_api_route(
        api_route,
        api_host,
        "GET",
        "/__route_contract_unsupported__",
        allow_localhost=allow_localhost,
    )
    if unsupported_result not in (PROXY, BLOCK):
        raise ValueError("production API route has no deterministic fallback")
    policy = "catch-all" if unsupported_result == PROXY else "explicit-allowlist"
    return {
        "api_host": api_host,
        "image_probe_count": len(PROBE_PATHS),
        "unsupported_policy": policy,
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--host", default=DEFAULT_API_HOST)
    parser.add_argument(
        "--disallow-localhost",
        action="store_true",
        help="reject localhost:8080 upstreams (used by the Azure candidate verifier)",
    )
    args = parser.parse_args()
    try:
        document = json.load(sys.stdin)
        if not isinstance(document, dict):
            raise ValueError("Caddy JSON root must be an object")
        contract = verify_document(
            document, args.host, allow_localhost=not args.disallow_localhost
        )
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        print(f"verify-image-route-contract: {exc}", file=sys.stderr)
        return 1
    print(
        "IMAGE_ROUTE_CONTRACT_PASS "
        f"host={contract['api_host']} paths={contract['image_probe_count']} "
        f"policy={contract['unsupported_policy']}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
