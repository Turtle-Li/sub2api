#!/usr/bin/env python3
"""Keep the external API edge allowlist aligned with Go gateway routes."""

from __future__ import annotations

import re
import unittest
from pathlib import Path
from typing import Iterator, List, Tuple


REPO_ROOT = Path(__file__).resolve().parents[2]
GATEWAY_SOURCE = REPO_ROOT / "backend/internal/server/routes/gateway.go"
EXTERNAL_CADDYFILE = REPO_ROOT / "deploy/Caddyfile.external-cert.example"

ROUTE_RE = re.compile(
    r'^\s*(gateway|gemini|codexDirect|antigravityV1|antigravityV1Beta|r)'
    r'\.(?:GET|POST|PATCH|DELETE|PUT|Any)\("([^"]+)"'
)
ROUTE_PREFIXES = {
    "gateway": "/v1",
    "gemini": "/v1beta",
    "codexDirect": "/backend-api/codex",
    "antigravityV1": "/antigravity/v1",
    "antigravityV1Beta": "/antigravity/v1beta",
    "r": "",
}


def registered_routes() -> Iterator[Tuple[int, str]]:
    lines = GATEWAY_SOURCE.read_text(encoding="utf-8").splitlines()
    start = next(index for index, line in enumerate(lines) if "func RegisterGatewayRoutes(" in line)
    end = next(
        index
        for index, line in enumerate(lines[start + 1 :], start + 1)
        if line.startswith("func dispatchCodexModelsGateway(")
    )
    for index in range(start, end):
        match = ROUTE_RE.match(lines[index])
        if match is None:
            continue
        receiver, path = match.groups()
        yield index + 1, ROUTE_PREFIXES[receiver] + path


def external_allowlist() -> List[str]:
    for line in EXTERNAL_CADDYFILE.read_text(encoding="utf-8").splitlines():
        code = line.split("#", 1)[0].strip()
        fields = code.split()
        if len(fields) >= 3 and fields[0] == "@openai_api" and fields[1] == "path":
            return fields[2:]
    raise AssertionError("external Caddy template has no @openai_api path allowlist")


def path_is_covered(path: str, pattern: str) -> bool:
    if pattern == path:
        return True
    if pattern.endswith("/*"):
        prefix = pattern[:-1]
        return path.startswith(prefix) and len(path) > len(prefix)
    return False


class GatewayCaddyRouteDriftTest(unittest.TestCase):
    def test_payment_webhook_is_exact_post_only_edge_route(self) -> None:
        text = EXTERNAL_CADDYFILE.read_text(encoding="utf-8")
        match = re.search(r"@unified_payment_webhook\s*\{([^}]+)\}", text)
        self.assertIsNotNone(match, "payment webhook has no dedicated API edge route")
        directives = [line.strip() for line in match.group(1).splitlines() if line.strip()]
        self.assertEqual(["method POST", "path /api/v1/payment/webhook/unified"], directives)
        self.assertRegex(text, r"handle @unified_payment_webhook\s*\{\s*import sub2api_proxy_no_store\s*\}")
        self.assertFalse(any(path_is_covered("/api/v1/admin/settings", pattern) for pattern in external_allowlist()))
        self.assertFalse(any(path_is_covered("/api/v1/payment/orders", pattern) for pattern in external_allowlist()))

    def test_every_registered_gateway_route_is_edge_reachable(self) -> None:
        allowlist = external_allowlist()
        missing = [
            f"{path} (gateway.go:{line})"
            for line, path in registered_routes()
            if not any(path_is_covered(path, pattern) for pattern in allowlist)
        ]
        self.assertEqual([], missing, "external Caddy allowlist is missing gateway routes")


if __name__ == "__main__":
    unittest.main()
