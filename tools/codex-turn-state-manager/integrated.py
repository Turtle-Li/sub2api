#!/usr/bin/env python3
"""Optional proxy-source adapter for the stable turn-state manager.

This module deliberately leaves ``manager.py`` unchanged.  It adds a private
operator overlay that is applied by the manager's worker thread, then installs
the subclasses only when this module is used as the entry point.
"""

from __future__ import annotations

import http.client
import ipaddress
import json
import os
from pathlib import Path
import random
import re
import socket
import ssl
import stat
import sys
import tempfile
import threading
import time
from typing import Any, Dict, List, Optional, Sequence, Tuple
import urllib.parse
import uuid

import legacy_host_adapter
import manager


_PROXY_SOURCES_FILE = "proxy-sources.json"
_PROXY_SOURCE_VERSION = 1
_MAX_PROXY_SOURCES = 50
_MAX_SOURCE_BYTES = 64 * 1024
_MAX_STATIC_LINES = 100
_MAX_EXTRACT_ENDPOINTS = 8
_MAX_EXTRACT_CANDIDATES = 100
_EXTRACT_TIMEOUT_SECONDS = 10
# The daemon normally polls every 60 seconds.  This prevents rapid manual
# retry loops from repeatedly calling a provider, while each due worker pass
# still obtains a fresh result.
_EXTRACT_MIN_REFRESH_SECONDS = 30

_SOURCE_NAME_RE = re.compile(r"^[a-z][a-z0-9_-]{0,63}$")
_SOURCE_ID_RE = re.compile(r"^[0-9a-f]{32}$")
_SOURCE_TYPES = frozenset(("static", "rotating", "extract"))
_PROXY_SCHEMES = frozenset(("http", "https", "socks5", "socks5h"))
_BLOCKED_HOSTS = frozenset((
    "localhost",
    "metadata",
    "metadata.google",
    "metadata.google.internal",
    "instance-data",
))
_CONTAINER_NAME_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$")
_LEGACY_STATE_DIR = Path("/var/lib/codex-turn-state-manager")


class ProxySourceError(ValueError):
    """A source error whose text is never returned to the panel."""


class ProxySourceFetchError(ProxySourceError):
    """A generic failure while fetching an extraction source."""


class IntegratedRuntimeError(ValueError):
    """A startup error that does not disclose deployment paths or settings."""


def _contains_control(value: str) -> bool:
    return any(ord(char) < 0x20 or ord(char) == 0x7F for char in value)


def _private_overlay_path(state_dir: Path) -> Path:
    return state_dir / _PROXY_SOURCES_FILE


def _explicit_runtime_state_dir(config_path: Path) -> Path:
    """Require an adapter-specific target instead of legacy production defaults."""
    try:
        config = json.loads(config_path.read_text(encoding="utf-8"))
    except (OSError, UnicodeDecodeError, ValueError) as exc:
        raise IntegratedRuntimeError() from exc
    if not isinstance(config, dict):
        raise IntegratedRuntimeError()
    raw_state_dir = config.get("state_dir")
    sub2api = config.get("sub2api")
    alerts = config.get("alerts")
    degraded = config.get("degraded")
    if (
        not isinstance(raw_state_dir, str)
        or not isinstance(sub2api, dict)
        or not isinstance(alerts, dict)
        or not isinstance(degraded, dict)
    ):
        raise IntegratedRuntimeError()
    state_dir = Path(raw_state_dir)
    container = sub2api.get("container")
    if (
        not state_dir.is_absolute()
        or state_dir == _LEGACY_STATE_DIR
        or _contains_control(raw_state_dir)
        or not isinstance(container, str)
        or not _CONTAINER_NAME_RE.fullmatch(container)
        or alerts.get("enabled") is not False
        or degraded.get("enabled") is not False
    ):
        raise IntegratedRuntimeError()
    return state_dir


def _config_path_from_argv(argv: Sequence[str]) -> Path:
    default = Path(__file__).with_name("config.json")
    for index, argument in enumerate(argv):
        if argument == "--config":
            if index + 1 >= len(argv):
                raise IntegratedRuntimeError()
            return Path(argv[index + 1])
        if argument.startswith("--config="):
            return Path(argument.partition("=")[2])
    return default


class _ManagerInstanceLock:
    """An advisory, process-lifetime lock for probe and pin-write modes."""

    def __init__(self, state_dir: Path):
        self._path = state_dir / "integrated-manager.lock"
        self._fd: Optional[int] = None

    def __enter__(self) -> "_ManagerInstanceLock":
        import fcntl

        self._path.parent.mkdir(parents=True, exist_ok=True)
        self._fd = os.open(str(self._path), os.O_RDWR | os.O_CREAT, 0o600)
        os.fchmod(self._fd, 0o600)
        try:
            fcntl.flock(self._fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except OSError as exc:
            os.close(self._fd)
            self._fd = None
            raise IntegratedRuntimeError() from exc
        return self

    def __exit__(self, _exc_type: Any, _exc: Any, _traceback: Any) -> None:
        if self._fd is None:
            return
        import fcntl

        try:
            fcntl.flock(self._fd, fcntl.LOCK_UN)
        finally:
            os.close(self._fd)
            self._fd = None


def _write_private_json(path: Path, payload: Any) -> None:
    """Atomically replace a secret-bearing overlay with mode 0600."""
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp_name = tempfile.mkstemp(prefix=".proxy-sources-", dir=str(path.parent))
    tmp_path = Path(tmp_name)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            json.dump(payload, handle, ensure_ascii=False, indent=2)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(tmp_path, path)
        # os.replace keeps the temporary file's mode.  Keep this explicit so
        # an unusual platform/filesystem cannot silently widen access.
        os.chmod(path, 0o600)
    except Exception:
        try:
            os.close(fd)
        except OSError:
            pass
        try:
            tmp_path.unlink()
        except OSError:
            pass
        raise


def _read_private_json(path: Path) -> Dict[str, Any]:
    try:
        metadata = os.lstat(path)
    except FileNotFoundError:
        return {"version": _PROXY_SOURCE_VERSION, "sources": []}
    if not stat.S_ISREG(metadata.st_mode) or metadata.st_mode & 0o077:
        raise ProxySourceError()
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeDecodeError, ValueError) as exc:
        raise ProxySourceError() from exc
    if not isinstance(value, dict):
        raise ProxySourceError()
    return value


def _clean_text(value: Any, limit: int) -> str:
    if not isinstance(value, str) or _contains_control(value) or len(value.encode("utf-8")) > limit:
        raise ProxySourceError()
    return value.strip()


def _clean_multiline_content(value: Any, limit: int) -> str:
    if not isinstance(value, str) or len(value.encode("utf-8")) > limit:
        raise ProxySourceError()
    if any(char != "\n" and (ord(char) < 0x20 or ord(char) == 0x7F) for char in value):
        raise ProxySourceError()
    return value.strip()


def _canonical_host(host: str) -> str:
    if not isinstance(host, str):
        raise ProxySourceError()
    candidate = host.strip().rstrip(".").lower()
    if not candidate or _contains_control(candidate) or "%" in candidate:
        raise ProxySourceError()
    try:
        return candidate.encode("idna").decode("ascii")
    except UnicodeError as exc:
        raise ProxySourceError() from exc


def _reject_non_public_host(host: str) -> str:
    candidate = _canonical_host(host)
    if candidate in _BLOCKED_HOSTS or candidate.endswith((".localhost", ".local", ".internal")):
        raise ProxySourceError()
    try:
        address = ipaddress.ip_address(candidate)
    except ValueError:
        address = None
    if address is not None and not address.is_global:
        raise ProxySourceError()
    return candidate


def _validate_proxy_host(host: str, port: int) -> str:
    """Reject source endpoints whose current DNS answers include private IPs."""
    candidate = _reject_non_public_host(host)
    try:
        _resolve_public_ips(candidate, port)
    except ProxySourceError as exc:
        raise ProxySourceError() from exc
    return candidate


def _normalized_proxy_endpoint(raw: str) -> str:
    """Use the stable parser, then add boundary validation around its output."""
    if not isinstance(raw, str) or _contains_control(raw):
        raise ProxySourceError()
    normalized = manager.normalize_proxy_url(raw)
    if not normalized:
        raise ProxySourceError()
    try:
        parsed = urllib.parse.urlsplit(normalized)
        port = parsed.port
    except ValueError as exc:
        raise ProxySourceError() from exc
    if (
        parsed.scheme not in _PROXY_SCHEMES
        or not parsed.hostname
        or port is None
        or not 1 <= port <= 65535
        or parsed.query
        or parsed.fragment
        or parsed.path not in ("", "/")
    ):
        raise ProxySourceError()
    _validate_proxy_host(parsed.hostname, port)
    for credential in (parsed.username, parsed.password):
        if credential is not None and _contains_control(urllib.parse.unquote(credential)):
            raise ProxySourceError()
    return normalized


def _proxy_lines(content: str, maximum: int) -> List[str]:
    lines = [line.strip() for line in content.splitlines() if line.strip()]
    if not lines or len(lines) > maximum:
        raise ProxySourceError()
    result: List[str] = []
    for line in lines:
        endpoint = _normalized_proxy_endpoint(line)
        if endpoint not in result:
            result.append(endpoint)
    if not result:
        raise ProxySourceError()
    return result


def _validated_extract_url(content: str) -> str:
    url = _clean_text(content, _MAX_SOURCE_BYTES)
    if not url:
        raise ProxySourceError()
    try:
        parsed = urllib.parse.urlsplit(url)
        port = parsed.port or 443
    except ValueError as exc:
        raise ProxySourceError() from exc
    if (
        parsed.scheme != "https"
        or not parsed.hostname
        or parsed.username is not None
        or parsed.password is not None
        or not 1 <= port <= 65535
    ):
        raise ProxySourceError()
    _reject_non_public_host(parsed.hostname)
    return url


def _extract_defaults(value: Any) -> str:
    if value is None:
        return ""
    return _clean_text(value, 512)


def _source_record(payload: Dict[str, Any], source_id: str) -> Dict[str, Any]:
    if not isinstance(payload, dict) or not _SOURCE_ID_RE.fullmatch(source_id):
        raise ProxySourceError()
    name = _clean_text(payload.get("name"), 64)
    source_type = payload.get("type")
    if not _SOURCE_NAME_RE.fullmatch(name) or name == "static" or source_type not in _SOURCE_TYPES:
        raise ProxySourceError()
    content = (
        _clean_multiline_content(payload.get("content"), _MAX_SOURCE_BYTES)
        if source_type == "static"
        else _clean_text(payload.get("content"), _MAX_SOURCE_BYTES)
    )

    if source_type == "static":
        if payload.get("username") not in (None, "") or payload.get("password") not in (None, ""):
            raise ProxySourceError()
        content = "\n".join(_proxy_lines(content, _MAX_STATIC_LINES))
        return {"id": source_id, "name": name, "type": source_type, "content": content, "enabled": True}

    if source_type == "rotating":
        if payload.get("username") not in (None, "") or payload.get("password") not in (None, ""):
            raise ProxySourceError()
        lines = _proxy_lines(content, 1)
        if len(lines) != 1:
            raise ProxySourceError()
        return {"id": source_id, "name": name, "type": source_type, "content": lines[0], "enabled": True}

    username = _extract_defaults(payload.get("username"))
    password = _extract_defaults(payload.get("password"))
    if bool(username) != bool(password):
        raise ProxySourceError()
    record = {
        "id": source_id,
        "name": name,
        "type": source_type,
        "content": _validated_extract_url(content),
        "enabled": True,
    }
    if username:
        record["username"] = username
        record["password"] = password
    return record


def _stored_source_record(value: Any) -> Dict[str, Any]:
    if not isinstance(value, dict):
        raise ProxySourceError()
    source_id = value.get("id")
    if not isinstance(source_id, str) or not _SOURCE_ID_RE.fullmatch(source_id):
        raise ProxySourceError()
    record = _source_record(value, source_id)
    enabled = value.get("enabled", True)
    if not isinstance(enabled, bool):
        raise ProxySourceError()
    record["enabled"] = enabled
    return record


def _source_metadata(record: Dict[str, Any]) -> Dict[str, Any]:
    source_type = record["type"]
    count = len(record["content"].splitlines()) if source_type == "static" else (1 if source_type == "rotating" else 0)
    return {
        "id": record["id"],
        "name": record["name"],
        "type": source_type,
        "enabled": bool(record.get("enabled", True)),
        "count": count,
        "configured": True,
    }


def _resolve_public_ips(host: str, port: int) -> List[str]:
    canonical_host = _reject_non_public_host(host)
    try:
        literal = ipaddress.ip_address(canonical_host)
    except ValueError:
        literal = None
    if literal is not None:
        return [str(literal)]
    try:
        answers = socket.getaddrinfo(canonical_host, port, type=socket.SOCK_STREAM)
    except OSError as exc:
        raise ProxySourceFetchError() from exc
    addresses: List[str] = []
    for _family, _kind, _protocol, _canonname, sockaddr in answers:
        try:
            address = ipaddress.ip_address(sockaddr[0])
        except ValueError as exc:
            raise ProxySourceFetchError() from exc
        # Reject a mixed DNS answer as well: selecting the public answer while
        # accepting a private one leaves a rebinding edge open for later code.
        if not address.is_global:
            raise ProxySourceFetchError()
        rendered = str(address)
        if rendered not in addresses:
            addresses.append(rendered)
    if not addresses:
        raise ProxySourceFetchError()
    return addresses


class _VerifiedHTTPSConnection(http.client.HTTPSConnection):
    """Connect to the validated DNS answer while preserving TLS hostname checks."""

    def __init__(self, host: str, port: int, approved_ips: Sequence[str], timeout: int):
        super().__init__(host=host, port=port, timeout=timeout, context=ssl.create_default_context())
        self._approved_ips = tuple(approved_ips)

    def connect(self) -> None:
        failure: Optional[OSError] = None
        for address in self._approved_ips:
            sock: Optional[socket.socket] = None
            try:
                sock = socket.create_connection((address, self.port), self.timeout)
                self.sock = self._context.wrap_socket(sock, server_hostname=self.host)
                return
            except OSError as exc:
                failure = exc
                if sock is not None:
                    try:
                        sock.close()
                    except OSError:
                        pass
        raise OSError("verified HTTPS connection failed") from failure


def _format_proxy_host(host: str) -> str:
    try:
        ipaddress.ip_address(host)
    except ValueError:
        return host
    return f"[{host}]" if ":" in host else host


def _proxy_host_identity(proxy: str) -> str:
    """Collapse proxy URLs to host identity, excluding credentials and port."""
    try:
        host = urllib.parse.urlsplit(proxy).hostname
    except ValueError as exc:
        raise ProxySourceError() from exc
    if not host:
        raise ProxySourceError()
    canonical = _canonical_host(host)
    try:
        return str(ipaddress.ip_address(canonical))
    except ValueError:
        return canonical


def _proxy_with_defaults(endpoint: str, username: str, password: str) -> str:
    normalized = _normalized_proxy_endpoint(endpoint)
    parsed = urllib.parse.urlsplit(normalized)
    if parsed.username is not None or not username:
        return normalized
    host = _format_proxy_host(parsed.hostname or "")
    return _normalized_proxy_endpoint(
        f"{parsed.scheme}://{urllib.parse.quote(username, safe='')}:"
        f"{urllib.parse.quote(password, safe='')}@{host}:{parsed.port}"
    )


def _endpoint_from_object(value: Dict[str, Any], username: str, password: str) -> str:
    direct = value.get("proxy") or value.get("url") or value.get("endpoint")
    if isinstance(direct, str):
        return _proxy_with_defaults(direct, username, password)
    host = value.get("host") or value.get("ip") or value.get("proxy_address")
    port = value.get("port")
    scheme = value.get("protocol") or value.get("scheme") or "http"
    item_username = value.get("username", username)
    item_password = value.get("password", password)
    if (
        not isinstance(host, str)
        or isinstance(port, bool)
        or not isinstance(scheme, str)
        or not isinstance(item_username, str)
        or not isinstance(item_password, str)
    ):
        raise ProxySourceError()
    try:
        port_number = int(port)
    except (TypeError, ValueError) as exc:
        raise ProxySourceError() from exc
    if not 1 <= port_number <= 65535 or scheme.lower() not in _PROXY_SCHEMES:
        raise ProxySourceError()
    if bool(item_username) != bool(item_password) or _contains_control(item_username) or _contains_control(item_password):
        raise ProxySourceError()
    host = _format_proxy_host(host.strip())
    if item_username:
        return _normalized_proxy_endpoint(
            f"{scheme.lower()}://{urllib.parse.quote(item_username, safe='')}:"
            f"{urllib.parse.quote(item_password, safe='')}@{host}:{port_number}"
        )
    return _normalized_proxy_endpoint(f"{scheme.lower()}://{host}:{port_number}")


def _json_endpoint_values(payload: Any) -> List[Any]:
    if isinstance(payload, list):
        return payload
    if not isinstance(payload, dict):
        raise ProxySourceError()
    data = payload.get("data", payload.get("results", payload.get("proxies")))
    if isinstance(data, list):
        return data
    if isinstance(data, dict):
        if all(key in data for key in ("ip", "port")) or all(key in data for key in ("host", "port")):
            return [data]
        for key in ("proxy_list", "proxies", "results", "items"):
            if isinstance(data.get(key), list):
                return data[key]
    raise ProxySourceError()


def _extract_proxy_endpoints(response: str, username: str, password: str) -> List[str]:
    if not isinstance(response, str) or len(response.encode("utf-8")) > _MAX_SOURCE_BYTES:
        raise ProxySourceError()
    stripped = response.strip()
    if not stripped:
        raise ProxySourceError()
    if stripped.startswith(("{", "[")):
        try:
            values = _json_endpoint_values(json.loads(stripped))
        except (ValueError, TypeError) as exc:
            raise ProxySourceError() from exc
    else:
        values = [line.strip() for line in response.splitlines() if line.strip()]

    endpoints: List[str] = []
    for value in values[:_MAX_EXTRACT_CANDIDATES]:
        try:
            if isinstance(value, str):
                endpoint = _proxy_with_defaults(value, username, password)
            elif isinstance(value, dict):
                endpoint = _endpoint_from_object(value, username, password)
            else:
                continue
        except ProxySourceError:
            continue
        if endpoint not in endpoints:
            endpoints.append(endpoint)
        if len(endpoints) >= _MAX_EXTRACT_ENDPOINTS:
            break
    if not endpoints:
        raise ProxySourceError()
    return endpoints


class IntegratedStateManager(manager.StateManager):
    """Stable manager plus a worker-reloaded, private proxy source overlay."""

    def __init__(self, config_path: Path):
        _explicit_runtime_state_dir(Path(config_path))
        legacy_host_adapter.install()
        super().__init__(config_path)

    def _ensure_proxy_source_state(self) -> None:
        if hasattr(self, "_proxy_source_file_lock"):
            return
        self._proxy_source_file_lock = threading.RLock()
        self._proxy_source_refresh_lock = threading.RLock()
        self._integrated_base_initialized = False
        self._integrated_base_proxies: List[str] = []
        self._integrated_base_dynamic: List[str] = []
        self._integrated_base_sources: Dict[str, str] = {}
        self._integrated_base_rotating = False
        self._extract_cache: Dict[str, Tuple[Tuple[str, str, str], List[str]]] = {}
        self._extract_next_fetch: Dict[str, float] = {}
        self._extract_fetch_signature: Dict[str, Tuple[str, str, str]] = {}
        self._active_extract_source_records: List[Dict[str, Any]] = []
        self._active_extract_proxy_urls: set[str] = set()
        self._static_round_active = False
        self._static_round_by_host: Dict[str, List[str]] = {}
        self._static_round_used_hosts: set[str] = set()
        self._static_round_rng = random.SystemRandom()

    @property
    def _proxy_sources_overlay_file(self) -> Path:
        return _private_overlay_path(self.state_dir)

    def _load_proxy_source_records(self) -> List[Dict[str, Any]]:
        envelope = _read_private_json(self._proxy_sources_overlay_file)
        if envelope.get("version") != _PROXY_SOURCE_VERSION or not isinstance(envelope.get("sources"), list):
            raise ProxySourceError()
        records = [_stored_source_record(item) for item in envelope["sources"]]
        if len(records) > _MAX_PROXY_SOURCES or len({record["id"] for record in records}) != len(records):
            raise ProxySourceError()
        if len({record["name"] for record in records}) != len(records):
            raise ProxySourceError()
        return records

    def list_proxy_sources(self) -> List[Dict[str, Any]]:
        self._ensure_proxy_source_state()
        with self._proxy_source_file_lock:
            try:
                return [_source_metadata(record) for record in self._load_proxy_source_records()]
            except ProxySourceError:
                return []

    def create_proxy_source(self, payload: Dict[str, Any]) -> Dict[str, Any]:
        self._ensure_proxy_source_state()
        record = _source_record(payload, uuid.uuid4().hex)
        with self._proxy_source_file_lock:
            records = self._load_proxy_source_records()
            if len(records) >= _MAX_PROXY_SOURCES or any(row["name"] == record["name"] for row in records):
                raise ProxySourceError()
            records.append(record)
            _write_private_json(
                self._proxy_sources_overlay_file,
                {"version": _PROXY_SOURCE_VERSION, "sources": records},
            )
        # The panel thread only persists and wakes the daemon.  It never
        # mutates self.proxies, which remains owned by the maintenance thread.
        self._wake.set()
        return _source_metadata(record)

    def delete_proxy_source(self, source_id: str) -> bool:
        self._ensure_proxy_source_state()
        if not isinstance(source_id, str) or not _SOURCE_ID_RE.fullmatch(source_id):
            raise ProxySourceError()
        with self._proxy_source_file_lock:
            records = self._load_proxy_source_records()
            remaining = [record for record in records if record["id"] != source_id]
            if len(remaining) == len(records):
                return False
            _write_private_json(
                self._proxy_sources_overlay_file,
                {"version": _PROXY_SOURCE_VERSION, "sources": remaining},
            )
        self._wake.set()
        return True

    def _fetch_extract_proxy_source(self, record: Dict[str, Any]) -> str:
        """Fetch directly through a verified public DNS answer, with no redirects."""
        url = _validated_extract_url(record["content"])
        try:
            parsed = urllib.parse.urlsplit(url)
            host = _canonical_host(parsed.hostname or "")
            port = parsed.port or 443
        except ValueError as exc:
            raise ProxySourceFetchError() from exc
        addresses = _resolve_public_ips(host, port)
        request_target = parsed.path or "/"
        if parsed.query:
            request_target += "?" + parsed.query
        host_header = _format_proxy_host(host)
        if port != 443:
            host_header += f":{port}"
        connection = _VerifiedHTTPSConnection(host, port, addresses, _EXTRACT_TIMEOUT_SECONDS)
        try:
            connection.request(
                "GET",
                request_target,
                headers={
                    "Host": host_header,
                    "User-Agent": "codex-state-manager-proxy-source/1.0",
                    "Accept": "application/json, text/plain;q=0.9",
                    "Connection": "close",
                },
            )
            response = connection.getresponse()
            # A redirect is intentionally a failure; credentials and source
            # defaults are never forwarded to a different URL.
            if response.status < 200 or response.status >= 300:
                raise ProxySourceFetchError()
            payload = response.read(_MAX_SOURCE_BYTES + 1)
            if len(payload) > _MAX_SOURCE_BYTES:
                raise ProxySourceFetchError()
            return payload.decode("utf-8")
        except ProxySourceFetchError:
            raise
        except (OSError, UnicodeError, http.client.HTTPException) as exc:
            raise ProxySourceFetchError() from exc
        finally:
            connection.close()

    def _extract_urls_for_source(self, record: Dict[str, Any], force: bool) -> List[str]:
        source_id = record["id"]
        signature = (record["content"], record.get("username", ""), record.get("password", ""))
        now = time.monotonic()
        cached = self._extract_cache.get(source_id)
        due = (
            force
            or self._extract_fetch_signature.get(source_id) != signature
            or now >= self._extract_next_fetch.get(source_id, 0.0)
        )
        if due:
            self._extract_next_fetch[source_id] = now + _EXTRACT_MIN_REFRESH_SECONDS
            self._extract_fetch_signature[source_id] = signature
            try:
                endpoints = _extract_proxy_endpoints(
                    self._fetch_extract_proxy_source(record),
                    record.get("username", ""),
                    record.get("password", ""),
                )
            except Exception as exc:  # noqa: BLE001 - never log source text or credentials
                self._extract_cache.pop(source_id, None)
                print(f"[!] Proxy source refresh failed ({type(exc).__name__}).")
                return []
            self._extract_cache[source_id] = (signature, endpoints)
            cached = self._extract_cache[source_id]
        return list(cached[1]) if cached is not None else []

    def _begin_static_round(self) -> None:
        """Build a temporary cross-account pool keyed by egress host identity."""
        static_proxies = [proxy for proxy in self.proxies if proxy not in self._dynamic_proxies]
        by_host: Dict[str, List[str]] = {}
        for proxy in static_proxies:
            try:
                identity = _proxy_host_identity(proxy)
            except ProxySourceError:
                continue
            by_host.setdefault(identity, []).append(proxy)
        self._static_round_by_host = by_host
        self._static_round_used_hosts = set()
        self._static_round_active = True

    def _take_static_round_proxy(self) -> Optional[str]:
        if not self._static_round_by_host:
            return None
        identity = self._static_round_rng.choice(list(self._static_round_by_host))
        candidates = self._static_round_by_host.pop(identity)
        self._static_round_used_hosts.add(identity)
        return self._static_round_rng.choice(candidates)

    def _static_round_has_remaining(self) -> bool:
        return bool(self._static_round_by_host)

    def _finish_static_round(self) -> None:
        self._static_round_active = False
        self._static_round_by_host = {}
        self._static_round_used_hosts = set()

    def _harvest_with_static_round(self, account: Dict[str, Any], model_cfg: Dict[str, Any]) -> Any:
        """Give each slot one unique static host before using dynamic gateways."""
        slot = f"{account['id']}:{model_cfg['name']}"
        selected = self._take_static_round_proxy()
        if selected is not None:
            if self._rotating:
                # The stable rotating routine consumes exactly one pending
                # static endpoint, then it will use dynamic gateways only once
                # the temporary cross-account pool has been exhausted.
                self._static_pending[slot] = [selected]
                return super().harvest(account, model_cfg)

            # The stable non-rotating routine normally walks every static URL
            # itself.  Restrict one call to the allocated host and request a
            # continuation while the round still has unused host identities.
            original_proxies = self.proxies
            self.proxies = [selected]
            try:
                result = super().harvest(account, model_cfg)
            finally:
                self.proxies = original_proxies
            if result is None and self._static_round_has_remaining():
                self._harvest_retry_delay = 0
                self._continue_harvest = True
            return result

        self._activate_extract_sources_for_dynamic()
        if self._dynamic_proxies:
            # An empty pending list is the established core signal to move
            # from static attempts to the existing rotating-gateway branch.
            self._static_pending[slot] = []
            return super().harvest(account, model_cfg)
        return None

    def _prune_removed_proxy_runtime_state(self, old_static: List[str], new_static: List[str]) -> None:
        allowed = set(self.proxies)
        old_set = set(old_static)
        additions = [proxy for proxy in new_static if proxy not in old_set]
        for slot, pending in list(self._static_pending.items()):
            updated = [proxy for proxy in pending if proxy in new_static]
            updated.extend(proxy for proxy in additions if proxy not in updated)
            self._static_pending[slot] = updated

        if self._static_round_active:
            allowed_static = set(new_static)
            self._static_round_by_host = {
                identity: [proxy for proxy in choices if proxy in allowed_static]
                for identity, choices in self._static_round_by_host.items()
                if any(proxy in allowed_static for proxy in choices)
            }
            # A panel import can land between continuation passes.  Add it to
            # the live temporary pool unless that host identity has already
            # been selected for this round.
            for proxy in additions:
                try:
                    identity = _proxy_host_identity(proxy)
                except ProxySourceError:
                    continue
                if identity in self._static_round_used_hosts:
                    continue
                choices = self._static_round_by_host.setdefault(identity, [])
                if proxy not in choices:
                    choices.append(proxy)

        if not hasattr(self, "_proxy_usage"):
            return
        changed = False
        with self._usage_lock:
            for proxy in list(self._proxy_usage):
                if proxy not in allowed:
                    del self._proxy_usage[proxy]
                    changed = True
            snapshot = dict(self._proxy_usage)
        if changed:
            try:
                # This removes deleted source credentials from the core usage
                # cache as soon as the worker observes the overlay update.
                _write_private_json(self._proxy_state_file, snapshot)
            except OSError as exc:
                print(f"[!] Proxy usage cleanup failed ({type(exc).__name__}).")

    def _apply_proxy_source_overlay(self) -> None:
        self._ensure_proxy_source_state()
        with self._proxy_source_refresh_lock:
            if not self._integrated_base_initialized:
                self._integrated_base_proxies = list(self.proxies)
                self._integrated_base_dynamic = list(self._dynamic_proxies)
                self._integrated_base_sources = dict(self._proxy_sources)
                self._integrated_base_rotating = bool(self._rotating)
                self._integrated_base_initialized = True
            try:
                with self._proxy_source_file_lock:
                    records = self._load_proxy_source_records()
            except ProxySourceError as exc:
                records = []
                print(f"[!] Proxy source overlay ignored ({type(exc).__name__}).")

            active_ids = {
                record["id"]
                for record in records
                if record.get("enabled", True) and record["type"] == "extract"
            }
            tracked_extract_ids = (
                set(self._extract_cache)
                | set(self._extract_next_fetch)
                | set(self._extract_fetch_signature)
            )
            for source_id in tracked_extract_ids:
                if source_id not in active_ids:
                    self._extract_cache.pop(source_id, None)
                    self._extract_next_fetch.pop(source_id, None)
                    self._extract_fetch_signature.pop(source_id, None)

            base_dynamic = list(self._integrated_base_dynamic)
            old_static = [proxy for proxy in self.proxies if proxy not in self._dynamic_proxies]
            base_static = [proxy for proxy in self._integrated_base_proxies if proxy not in base_dynamic]
            static_overlay: List[str] = []
            dynamic_overlay: List[str] = []
            extract_records: List[Dict[str, Any]] = []
            source_names = dict(self._integrated_base_sources)

            def add_static(endpoint: str, source_name: str) -> None:
                if endpoint not in base_static and endpoint not in static_overlay:
                    static_overlay.append(endpoint)
                    source_names[endpoint] = source_name

            def add_dynamic(endpoint: str, source_name: str) -> None:
                if endpoint not in base_static and endpoint not in static_overlay and endpoint not in base_dynamic and endpoint not in dynamic_overlay:
                    dynamic_overlay.append(endpoint)
                    source_names[endpoint] = source_name

            for record in records:
                if not record.get("enabled", True):
                    continue
                try:
                    if record["type"] == "static":
                        for endpoint in _proxy_lines(record["content"], _MAX_STATIC_LINES):
                            add_static(endpoint, record["name"])
                    elif record["type"] == "rotating":
                        add_dynamic(_proxy_lines(record["content"], 1)[0], record["name"])
                    else:
                        # Fetch only after the round's static identities have
                        # been consumed.  Reading the source file is safe here;
                        # calling the provider is intentionally deferred to
                        # the worker's dynamic phase.
                        extract_records.append(record)
                except Exception as exc:  # noqa: BLE001 - source content remains private
                    print(f"[!] Proxy source refresh failed ({type(exc).__name__}).")

            self._dynamic_proxies = base_dynamic + dynamic_overlay
            self.proxies = base_static + static_overlay + self._dynamic_proxies
            self._proxy_sources = source_names
            # The inherited rotating scheduler needs to keep a static miss in
            # its continuation loop when an extraction source is waiting.  It
            # still sees no dynamic endpoint until _activate... runs after the
            # temporary static host pool is empty.
            self._rotating = (
                self._integrated_base_rotating
                or bool(self._dynamic_proxies)
                or bool(extract_records)
            )
            self._active_extract_source_records = extract_records
            self._active_extract_proxy_urls = set()
            self._prune_removed_proxy_runtime_state(old_static, base_static + static_overlay)

    def _activate_extract_sources_for_dynamic(self) -> None:
        """Refresh extraction sources only when static hosts are exhausted."""
        self._ensure_proxy_source_state()
        with self._proxy_source_refresh_lock:
            # Results from an extraction API must never survive a failed due
            # refresh.  Remove the prior runtime-only endpoints before asking
            # the source again, while leaving core and imported-gateway pools.
            previous = set(self._active_extract_proxy_urls)
            if previous:
                self._dynamic_proxies = [proxy for proxy in self._dynamic_proxies if proxy not in previous]
                self.proxies = [proxy for proxy in self.proxies if proxy not in previous]
                for proxy in previous:
                    self._proxy_sources.pop(proxy, None)
            self._active_extract_proxy_urls = set()

            for record in self._active_extract_source_records:
                try:
                    endpoints = self._extract_urls_for_source(record, force=False)
                except Exception as exc:  # noqa: BLE001 - source contents stay private
                    print(f"[!] Proxy source refresh failed ({type(exc).__name__}).")
                    continue
                for endpoint in endpoints:
                    if endpoint in self.proxies:
                        continue
                    self._dynamic_proxies.append(endpoint)
                    self.proxies.append(endpoint)
                    self._proxy_sources[endpoint] = record["name"]
                    self._active_extract_proxy_urls.add(endpoint)
            self._rotating = self._integrated_base_rotating or bool(self._dynamic_proxies)

    def _refresh_proxies(self) -> None:
        """Keep all existing source and rotating-gateway behavior, then overlay."""
        super()._refresh_proxies()
        self._ensure_proxy_source_state()
        with self._proxy_source_refresh_lock:
            self._integrated_base_proxies = list(self.proxies)
            self._integrated_base_dynamic = list(self._dynamic_proxies)
            self._integrated_base_sources = dict(self._proxy_sources)
            self._integrated_base_rotating = bool(self._rotating)
            self._integrated_base_initialized = True
        self._apply_proxy_source_overlay()

    def run_check_and_refresh(self, *args: Any, **kwargs: Any) -> int:
        """Worker-thread hooks for source reload and one cross-account pool."""
        self._apply_proxy_source_overlay()
        if not self._static_round_active:
            self._begin_static_round()
        try:
            return super().run_check_and_refresh(*args, **kwargs)
        finally:
            # The core sets _continue_harvest only when it needs the next pass.
            # Retaining the pool across that boundary is what lets it reach a
            # dynamic gateway after static hosts have been consumed.
            if not self._continue_harvest:
                self._finish_static_round()

    def harvest(self, account: Dict[str, Any], model_cfg: Dict[str, Any]) -> Any:
        if self._static_round_active:
            return self._harvest_with_static_round(account, model_cfg)
        return super().harvest(account, model_cfg)


class IntegratedPanelHandler(manager.PanelHandler):
    """Panel routes that expose metadata only; other routes stay in the core."""

    def do_GET(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler API
        path = urllib.parse.urlparse(self.path).path.rstrip("/") or "/"
        if path != "/api/proxy-sources":
            return super().do_GET()
        try:
            self._send(200, {"sources": self.manager.list_proxy_sources()})
        except Exception:  # noqa: BLE001 - do not disclose overlay details
            self._send(500, {"error": "panel operation failed; inspect service diagnostics"})

    def do_POST(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler API
        path = urllib.parse.urlparse(self.path).path.rstrip("/") or "/"
        if path != "/api/proxy-sources":
            return super().do_POST()
        if not self._mutation_allowed():
            self._send(403, {"error": "forbidden"})
            return
        try:
            source = self.manager.create_proxy_source(self._body())
            self._send(201, {"source": source})
        except (ProxySourceError, ValueError, TypeError):
            self._send(400, {"error": "invalid proxy source"})
        except Exception:  # noqa: BLE001 - do not disclose source text or credentials
            self._send(500, {"error": "panel operation failed; inspect service diagnostics"})

    def do_DELETE(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler API
        path = urllib.parse.urlparse(self.path).path.rstrip("/")
        parts = [part for part in path.split("/") if part]
        if len(parts) != 3 or parts[:2] != ["api", "proxy-sources"]:
            return super().do_DELETE()
        if not self._mutation_allowed():
            self._send(403, {"error": "forbidden"})
            return
        try:
            removed = self.manager.delete_proxy_source(urllib.parse.unquote(parts[2]))
            self._send(200 if removed else 404, {"removed": removed})
        except (ProxySourceError, ValueError, TypeError):
            self._send(400, {"error": "invalid proxy source"})
        except Exception:  # noqa: BLE001 - do not disclose source text or credentials
            self._send(500, {"error": "panel operation failed; inspect service diagnostics"})


def main() -> int:
    """Run the unmodified CLI/daemon after replacing only its extension seams."""
    try:
        state_dir = _explicit_runtime_state_dir(_config_path_from_argv(sys.argv[1:]))
    except IntegratedRuntimeError:
        print("[!] Integrated startup requires explicit project state_dir, sub2api.container, and disabled legacy alerts/degraded services.")
        return 1
    legacy_host_adapter.install()
    manager.StateManager = IntegratedStateManager
    manager.PanelHandler = IntegratedPanelHandler
    probe_modes = {"--once", "--force", "--daemon"}
    if not any(argument in probe_modes for argument in sys.argv[1:]):
        return manager.main()
    try:
        with _ManagerInstanceLock(state_dir):
            return manager.main()
    except IntegratedRuntimeError:
        print("[!] Integrated probe manager is already running for this project state directory.")
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
