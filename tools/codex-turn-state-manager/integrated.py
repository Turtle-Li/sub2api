#!/usr/bin/env python3
"""Optional proxy-source adapter for the stable turn-state manager.

This module deliberately leaves ``manager.py`` unchanged.  It adds a private
operator overlay that is applied by the manager's worker thread, then installs
the subclasses only when this module is used as the entry point.
"""

from __future__ import annotations

import hmac
import http.client
import ipaddress
import json
import os
from pathlib import Path
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
    """Require an explicit private directory; preserve the own-host deployment."""
    try:
        config = json.loads(config_path.read_text(encoding="utf-8"))
        raw = config["state_dir"]
        if not isinstance(raw, str) or _contains_control(raw):
            raise ValueError()
        state_dir = Path(raw)
        if not state_dir.is_absolute() or state_dir == Path("/"):
            raise ValueError()
        return state_dir
    except (OSError, UnicodeError, ValueError, KeyError, TypeError) as exc:
        raise IntegratedRuntimeError() from exc


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


def _normalized_proxy_endpoint(raw: str, resolve: bool = True) -> str:
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
    if resolve:
        _validate_proxy_host(parsed.hostname, port)
    else:
        _reject_non_public_host(parsed.hostname)
    for credential in (parsed.username, parsed.password):
        if credential is not None and _contains_control(urllib.parse.unquote(credential)):
            raise ProxySourceError()
    return normalized


def _proxy_lines(content: str, maximum: int, resolve: bool = True) -> List[str]:
    lines = [line.strip() for line in content.splitlines() if line.strip()]
    if not lines or len(lines) > maximum:
        raise ProxySourceError()
    result: List[str] = []
    for line in lines:
        endpoint = _normalized_proxy_endpoint(line, resolve=resolve)
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


def _source_record(payload: Dict[str, Any], source_id: str, resolve: bool = True) -> Dict[str, Any]:
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
        content = "\n".join(_proxy_lines(content, _MAX_STATIC_LINES, resolve=resolve))
        return {"id": source_id, "name": name, "type": source_type, "content": content, "enabled": True}

    if source_type == "rotating":
        if payload.get("username") not in (None, "") or payload.get("password") not in (None, ""):
            raise ProxySourceError()
        lines = _proxy_lines(content, 1, resolve=resolve)
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
    record = _source_record(value, source_id, resolve=False)
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
        self._accepted_source_records = []
        self._applied_source_records = {}
        self._extract_errors = set()
        self._source_load_errors = set()

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

    def _base_source_metadata(self):
        dynamic = set(self._integrated_base_dynamic)
        static_count = sum(proxy not in dynamic for proxy in self._integrated_base_proxies)
        rows = [{"id": "base-static", "name": "static", "type": "static",
                 "enabled": True, "origin": "base", "read_only": True,
                 "endpoint_count": static_count,
                 "status": "loaded" if static_count else "unavailable"}]
        for index, source in enumerate(self.config.get("proxies", {}).get("sources", [])):
            if source.get("type") != "rotating_residential":
                if source.get("enabled", True):
                    continue
                rows.append({"id": f"base-static-disabled-{index}", "name": "static", "type": "static",
                             "enabled": False, "origin": "base", "read_only": True,
                             "endpoint_count": 0, "status": "disabled"})
                continue
            name = source.get("name", "rainproxy")
            if not isinstance(name, str) or not _SOURCE_NAME_RE.fullmatch(name):
                name = "unknown-source"
            count = sum(self._integrated_base_sources.get(proxy) == name for proxy in dynamic)
            enabled = bool(source.get("enabled", True))
            rows.append({"id": f"base-dynamic-{index}", "name": name, "type": "rotating",
                         "enabled": enabled, "origin": "base", "read_only": True,
                         "endpoint_count": count, "status": "disabled" if not enabled else ("loaded" if count else "unavailable")})
        return rows

    def list_proxy_sources(self):
        self._ensure_proxy_source_state()
        # Same lock order as the worker; no network or probe on this path.
        with self._proxy_source_refresh_lock, self._proxy_source_file_lock:
            records = self._load_proxy_source_records()  # Invalid file is an explicit HTTP error.
            rows = self._base_source_metadata()
            for record in records:
                row = _source_metadata(record)
                count = sum(self._proxy_sources.get(proxy) == record["name"] for proxy in self.proxies)
                if self._applied_source_records.get(record["id"]) != record:
                    status = "pending"
                elif not record["enabled"]:
                    status = "disabled"
                elif record["id"] in self._extract_errors or record["id"] in self._source_load_errors:
                    status = "error"
                elif count:
                    status = "loaded"
                elif record["type"] == "extract":
                    status = "deferred"
                else:
                    status = "duplicate"
                row.update(origin="managed", read_only=False, endpoint_count=count, status=status)
                rows.append(row)
            return rows

    def set_proxy_source_enabled(self, source_id, enabled):
        self._ensure_proxy_source_state()
        if not isinstance(source_id, str) or not _SOURCE_ID_RE.fullmatch(source_id) or not isinstance(enabled, bool):
            raise ProxySourceError()
        with self._proxy_source_file_lock:
            records = self._load_proxy_source_records()
            for record in records:
                if record["id"] == source_id:
                    record["enabled"] = enabled
                    _write_private_json(self._proxy_sources_overlay_file, {"version": _PROXY_SOURCE_VERSION, "sources": records})
                    self._wake.set()
                    return _source_metadata(record)
            raise ProxySourceError()

    def create_proxy_source(self, payload: Dict[str, Any]) -> Dict[str, Any]:
        self._ensure_proxy_source_state()
        record = _source_record(payload, uuid.uuid4().hex)
        with self._proxy_source_file_lock:
            records = self._load_proxy_source_records()
            base_names = {x.get("name", "rainproxy") for x in self.config.get("proxies", {}).get("sources", []) if x.get("type") == "rotating_residential"}
            if record["name"] in base_names:
                raise ProxySourceError()
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
                self._extract_errors.add(source_id)
                print(f"[!] Proxy source refresh failed ({type(exc).__name__}).")
                return []
            self._extract_errors.discard(source_id)
            self._extract_cache[source_id] = (signature, endpoints)
            cached = self._extract_cache[source_id]
        return list(cached[1]) if cached is not None else []

    def _prune_removed_proxy_runtime_state(self, old_static, new_static):
        additions = [proxy for proxy in new_static if proxy not in old_static]
        for slot, pending in list(self._static_pending.items()):
            self._static_pending[slot] = [proxy for proxy in pending if proxy in new_static]
            self._static_pending[slot].extend(proxy for proxy in additions if proxy not in self._static_pending[slot])
        # Usage keys are already hashes in the own manager; keep its persisted
        # cooldown history instead of applying the upstream raw-URL cleanup.

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
                    base_names = {row["name"] for row in self._base_source_metadata()}
                    if any(record["name"] in base_names for record in records):
                        raise ProxySourceError()
                    self._accepted_source_records = records
            except (OSError, ProxySourceError) as exc:
                records = self._accepted_source_records
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

            self._source_load_errors = set()
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
                    self._source_load_errors.add(record["id"])
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
            self._applied_source_records = {record["id"]: dict(record) for record in records}
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
            self._rotating = self._integrated_base_rotating or bool(self._dynamic_proxies) or bool(self._active_extract_source_records)

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

    def run_check_and_refresh(self, *args, **kwargs):
        self._apply_proxy_source_overlay()
        return super().run_check_and_refresh(*args, **kwargs)

    def harvest(self, account, model_cfg):
        slot = f"{account['id']}:{model_cfg['name']}"
        static = [proxy for proxy in self.proxies if proxy not in self._dynamic_proxies]
        if self._active_extract_source_records and not self._static_pending.get(slot, static):
            self._activate_extract_sources_for_dynamic()
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
        match = re.fullmatch(r"/api/proxy-sources/([0-9a-f]{32})/enabled", path)
        if match:
            if not self._mutation_allowed():
                self._send(403, {"error": "forbidden"})
                return
            try:
                body = self._body()
                result = self.manager.set_proxy_source_enabled(match[1], body.get("enabled"))
                self._send(200, {"source": result})
            except (ValueError, TypeError, AttributeError):
                self._send(400, {"error": "invalid source setting"})
            except Exception:
                self._send(500, {"error": "panel operation failed"})
            return
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


# An optional same-host admin listener is separate from the legacy read-only
# Tailnet viewer. The existing app's internal token stays in its protected file.
def _admin_token(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077 or info.st_size > 4096:
            raise ValueError("invalid admin token file")
        raw = os.read(fd, 4097).strip()
        if not 16 <= len(raw) <= 1024 or any(byte < 0x21 or byte > 0x7e for byte in raw):
            raise ValueError("invalid admin token file")
        return raw.decode("ascii")
    finally:
        os.close(fd)


class AuthenticatedPanelHandler(IntegratedPanelHandler):
    token_file = None

    def _authorized(self):
        try:
            expected = _admin_token(self.token_file)
            supplied = self.headers.get("Authorization", "")
            return hmac.compare_digest(supplied.encode("utf-8"), ("Bearer " + expected).encode("ascii"))
        except (OSError, ValueError, TypeError, UnicodeError):
            return False

    def _mutation_allowed(self):
        if not self._authorized() or self.headers.get(manager.PANEL_CSRF_HEADER) != "1":
            return False
        origin = self.headers.get("Origin")
        if not origin:
            return True
        try:
            return urllib.parse.urlsplit(origin).netloc == self.headers.get("Host", "")
        except ValueError:
            return False

    def _send(self, code, payload, content_type="application/json"):
        if code == 200 and isinstance(payload, dict) and "read_only" in payload:
            payload = dict(payload, read_only=False)
        return super()._send(code, payload, content_type)

    def do_GET(self):
        if not self._authorized():
            return self._send(401, {"error": "unauthorized"})
        if not self.path.startswith("/api/"):
            return self._send(404, {"error": "not found"})
        return super().do_GET()

    def do_POST(self):
        if not self._authorized():
            return self._send(401, {"error": "unauthorized"})
        return super().do_POST()

    def do_DELETE(self):
        if not self._authorized():
            return self._send(401, {"error": "unauthorized"})
        return super().do_DELETE()


_start_readonly_panel = manager.start_panel


def start_integrated_panels(state):
    cfg = state.config.get("admin_panel", {})
    admin_server = None
    if cfg.get("enabled", False):
        bind = ipaddress.ip_address(cfg.get("bind", ""))
        if bind.version != 4 or bind.is_unspecified or bind.is_link_local or not (bind.is_private or bind.is_loopback):
            raise ValueError("admin panel requires a literal private IPv4 address")
        token_file = Path(cfg.get("token_file", ""))
        if not token_file.is_absolute():
            raise ValueError("admin token file must be absolute")
        _admin_token(token_file)  # Fail before opening either listener.
        port = cfg.get("port", 8788)
        if isinstance(port, bool) or not isinstance(port, int) or not 1 <= port <= 65535:
            raise ValueError("invalid admin panel port")
        handler = type("BoundAdminPanelHandler", (AuthenticatedPanelHandler,), {"manager": state, "token_file": token_file})
        admin_server = manager.ThreadingHTTPServer((str(bind), port), handler)
        admin_server.daemon_threads = True
    try:
        readonly = _start_readonly_panel(state)
    except Exception:
        if admin_server:
            admin_server.server_close()
        raise
    if admin_server:
        threading.Thread(target=admin_server.serve_forever, name="admin-panel", daemon=True).start()
        print("[*] Authenticated private admin panel started.")
    return readonly, admin_server


def main() -> int:
    """Preserve own-host discovery and probe behavior; add source management."""
    try:
        state_dir = _explicit_runtime_state_dir(_config_path_from_argv(sys.argv[1:]))
    except IntegratedRuntimeError:
        print("[!] Integrated startup requires an explicit absolute project state_dir.")
        return 1
    manager.StateManager = IntegratedStateManager
    manager.PanelHandler = IntegratedPanelHandler
    manager.start_panel = start_integrated_panels
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
