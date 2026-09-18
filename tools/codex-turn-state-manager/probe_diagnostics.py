"""Safe, local-only classification for HTTP probe responses.

This module deliberately reports only a small, JSON-serializable diagnostic
record.  It does not preserve response text, headers, cookies, credentials, or
turn-state values, so callers can safely attach the result to operational logs.
"""

import json
import re
from typing import Any, Optional, Tuple


MAX_BODY_BYTES = 8192

# Values returned from an untrusted JSON body must come from this fixed set.
# "rate_limit" is included because it is an explicit, non-sensitive canonical
# condition used by the scheduler; arbitrary provider error names are dropped.
ERROR_CODE_WHITELIST = frozenset(
    {
        "access_denied",
        "permission_denied",
        "invalid_api_key",
        "insufficient_permissions",
        "unsupported_country_region_territory",
        "account_deactivated",
        "temporarily_unavailable",
        "service_unavailable",
        "upstream_timeout",
        "gateway_timeout",
        "proxy_connection_error",
        "rate_limit",
    }
)

TEMPORARY_ERROR_CODES = frozenset(
    {
        "temporarily_unavailable",
        "service_unavailable",
        "upstream_timeout",
        "gateway_timeout",
        "proxy_connection_error",
    }
)

FORBIDDEN_ERROR_CODES = frozenset(
    {
        "access_denied",
        "permission_denied",
        "invalid_api_key",
        "insufficient_permissions",
        "unsupported_country_region_territory",
        "account_deactivated",
    }
)

REQUEST_ID_HEADERS = (
    "x-request-id",
    "request-id",
    "x-correlation-id",
    "x-amzn-requestid",
    "x-amz-request-id",
    "x-openai-request-id",
)

UUID_RE = re.compile(
    r"^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-"
    r"[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$"
)
HEX_REQUEST_ID_RE = re.compile(r"^[0-9a-fA-F]{16,64}$")
CF_RAY_RE = re.compile(r"^[0-9a-fA-F]{16,32}(?:-[A-Z]{3})?$")


def _limited_body(body: str) -> str:
    """Return at most ``MAX_BODY_BYTES`` UTF-8 bytes before any JSON parsing."""
    if not isinstance(body, str):
        return ""
    return body.encode("utf-8", errors="replace")[:MAX_BODY_BYTES].decode(
        "utf-8", errors="ignore"
    )


def _normalised_headers(headers: dict[str, list[str]]) -> dict[str, tuple[str, ...]]:
    """Keep only string header names and values; never retain them for output."""
    if not isinstance(headers, dict):
        return {}

    result: dict[str, tuple[str, ...]] = {}
    for raw_name, raw_values in headers.items():
        if not isinstance(raw_name, str):
            continue
        name = raw_name.strip().casefold()
        if not name:
            continue

        if isinstance(raw_values, str):
            values = (raw_values,)
        elif isinstance(raw_values, (list, tuple)):
            values = tuple(value for value in raw_values if isinstance(value, str))
        else:
            values = ()
        if values:
            result[name] = result.get(name, ()) + values
    return result


def _safe_request_id(headers: dict[str, tuple[str, ...]]) -> Optional[str]:
    """Extract only canonical UUIDs, pure hex IDs, or strict CF-Ray IDs."""
    for header_name in REQUEST_ID_HEADERS:
        for raw_value in headers.get(header_name, ()):
            candidate = raw_value.strip()
            if UUID_RE.fullmatch(candidate) or HEX_REQUEST_ID_RE.fullmatch(candidate):
                return candidate.lower()
    for raw_value in headers.get("cf-ray", ()):
        candidate = raw_value.strip()
        if CF_RAY_RE.fullmatch(candidate):
            return candidate
    return None


def _source(stage: str, headers: dict[str, tuple[str, ...]]) -> str:
    """Describe response-path evidence without claiming a causal source."""
    if stage == "proxy_connect":
        return "proxy"
    if "cf-ray" in headers:
        return "edge_hint"
    if any("cloudflare" in value.casefold() for value in headers.get("server", ())):
        return "edge_hint"
    if stage == "upstream":
        return "upstream"
    return "unknown"


def _safe_error_code(payload: dict[str, Any]) -> Optional[str]:
    """Return an allowlisted code, prioritising explicit permission evidence."""
    candidates = []
    error = payload.get("error")
    if isinstance(error, dict):
        candidates.extend((error.get("code"), error.get("type")))
    candidates.append(payload.get("code"))

    safe_codes = []
    for candidate in candidates:
        if not isinstance(candidate, str):
            continue
        code = candidate.strip().casefold()
        if code in ERROR_CODE_WHITELIST:
            safe_codes.append(code)
    for code in safe_codes:
        if code in FORBIDDEN_ERROR_CODES:
            return code
    return safe_codes[0] if safe_codes else None


def _body_metadata(body: str) -> Tuple[str, Optional[str]]:
    """Classify a bounded body without returning any of its text or structure."""
    limited = _limited_body(body)
    stripped = limited.lstrip()
    if not stripped:
        return "empty", None

    try:
        decoded = json.loads(limited)
    except (json.JSONDecodeError, RecursionError, TypeError, ValueError):
        # This is deliberately a shape classification only; HTML must not make
        # a 403 retriable or trigger a path around a security challenge.
        return ("html" if stripped.startswith("<") else "text"), None

    if not isinstance(decoded, dict):
        return "json_non_object", None
    return "json_object", _safe_error_code(decoded)


def _safe_status(status: int) -> Optional[int]:
    """Keep an integer status only; avoid serialising unexpected input objects."""
    if isinstance(status, int) and not isinstance(status, bool):
        return status
    return None


def _classification_for(status: Optional[int], error_code: Optional[str]) -> Tuple[str, bool]:
    """Choose retry policy from explicit status/code evidence, never text clues."""
    if status == 200:
        return "ok", False
    if status == 401:
        return "authentication_failed", False
    if status == 407:
        return "proxy_auth", False
    if status == 429:
        # A scheduler may honor Retry-After separately; this helper never asks
        # callers to immediately retry it.
        return "rate_limit", False
    if status in (502, 503, 504):
        return "temporary_gateway", True
    if status == 403:
        if error_code in TEMPORARY_ERROR_CODES:
            return "temporary_error", True
        if error_code in FORBIDDEN_ERROR_CODES:
            return "forbidden", False
        return "unknown_forbidden", False

    if error_code in TEMPORARY_ERROR_CODES:
        return "temporary_error", True
    if error_code in FORBIDDEN_ERROR_CODES:
        return "forbidden", False
    if status is None:
        return "unknown", False
    if 500 <= status <= 599:
        return "server_error", False
    if 400 <= status <= 499:
        return "client_error", False
    return "unexpected_status", False


def classify_response(
    status: int,
    headers: dict[str, list[str]],
    body: str,
    stage: str,
) -> dict:
    """Return a fixed, JSON-safe diagnostic record for one HTTP response.

    ``source`` records only the probe stage or edge-header indication.  It does
    not establish that a proxy or edge caused the response.  In particular, an
    edge indication and an HTML response never make a 403 retryable.
    """
    safe_headers = _normalised_headers(headers)
    body_kind, error_code = _body_metadata(body)
    http_status = _safe_status(status)
    category, retryable = _classification_for(http_status, error_code)
    return {
        "category": category,
        "retryable": retryable,
        "source": _source(stage, safe_headers),
        "http_status": http_status,
        "request_id": _safe_request_id(safe_headers),
        "error_code": error_code,
        "body_kind": body_kind,
    }
