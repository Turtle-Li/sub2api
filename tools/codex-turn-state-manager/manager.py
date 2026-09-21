#!/usr/bin/env python3
"""
Codex Turn-State Proactive Manager & Monitor

Harvests un-downgraded x-codex-turn-state values through a commercial proxy pool
and pins them onto Sub2API accounts so the gateway can replay them per model.

Runs ON the production host. Account credentials and pinned states are read and
written directly through the active Sub2API container's psql session, mirroring
the semantics of accountRepository.UpdateExtra (a jsonb merge). No admin API
token is involved.

The probe reproduces the codex-tui client fingerprint exactly; upstream risk
control keys off these headers, so a request missing them tends to come back
with a degraded (312-byte) state even when the egress IP is clean.

Modes: --status, --once, --force, --daemon, --test-proxies, --alert-test
"""

import collections
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import argparse
import base64
from datetime import datetime, timezone, timedelta
from email.utils import parsedate_to_datetime
import json
import ipaddress
import hashlib
import math
import os
from pathlib import Path
import re
import random
import socket
import struct
import subprocess
import sys
import tempfile
import threading
import time
from typing import Any, Dict, List, Optional, Tuple
import unicodedata
import urllib.request
from probe_diagnostics import classify_response
from probe_stats import ProbeStats

DEFAULT_TTL_SECONDS = 3600
MAX_STATE_BYTES = 8192
# gpt-6-astra is rejected with HTTP 400 below this client version.
MIN_CODEX_CLIENT_VERSION = "0.154.0"
BASE64_RE = re.compile(r"^[A-Za-z0-9+/=]+$")
MODEL_NAME_RE = re.compile(r"^[A-Za-z0-9_.:-]{1,128}$")


DEFAULT_REFRESH_ADVANCE_MINUTES = 15
PROBE_HISTORY_PER_SLOT = 50

def atomic_write_json(path: Path, payload: Any) -> None:
    """Write JSON so a reader never observes a half-written file."""
    path.parent.mkdir(parents=True, exist_ok=True)
    handle = tempfile.NamedTemporaryFile(
        "w", encoding="utf-8", dir=str(path.parent), delete=False
    )
    try:
        json.dump(payload, handle, ensure_ascii=False, indent=2)
        handle.flush()
        os.fsync(handle.fileno())
        handle.close()
        os.replace(handle.name, path)
    except Exception:
        handle.close()
        try:
            os.unlink(handle.name)
        except OSError:
            pass
        raise

# --------------------------------------------------------------------------
# turn-state inspection
# --------------------------------------------------------------------------

def inspect_turn_state(state: str) -> Dict[str, Any]:
    """Decode the Fernet timestamp prefix to recover issuance and expiry."""
    state = (state or "").strip()
    if not state:
        return {"valid": False, "length": 0, "error": "empty"}
    try:
        padded = state + "=" * (-len(state) % 4)
        raw = base64.urlsafe_b64decode(padded)
        if len(raw) < 9 or raw[0] != 0x80:
            return {"valid": False, "length": len(state), "error": "not_fernet"}
        issued_at = datetime.fromtimestamp(struct.unpack(">Q", raw[1:9])[0], tz=timezone.utc)
        expires_at = issued_at + timedelta(seconds=DEFAULT_TTL_SECONDS)
        now = datetime.now(timezone.utc)
        remaining = (expires_at - now).total_seconds()
        age = (now - issued_at).total_seconds()
        return {
            "valid": True,
            "length": len(state),
            "issued_at": issued_at.strftime("%Y-%m-%d %H:%M:%S UTC"),
            "issued_at_iso": issued_at.isoformat(),
            "expires_at": expires_at.strftime("%Y-%m-%d %H:%M:%S UTC"),
            "expires_at_iso": expires_at.isoformat(),
            "remaining_seconds": max(0, int(remaining)),
            "remaining_minutes": round(max(0.0, remaining) / 60.0, 1),
            "age_seconds": max(0, int(age)),
            "age_minutes": round(max(0.0, age) / 60.0, 1),
            "is_expired": remaining <= 0,
        }
    except Exception as exc:  # noqa: BLE001 - diagnostic passthrough
        return {"valid": False, "length": len(state), "error": str(exc)}


def _version_tuple(value: str) -> Tuple[int, ...]:
    """Parse a dotted version for ordering; unparsable segments sort as 0."""
    parts = []
    for segment in str(value).strip().split("."):
        digits = re.match(r"\d+", segment)
        parts.append(int(digits.group()) if digits else 0)
    return tuple(parts)


def is_valid_header_value(value: str) -> bool:
    """Mirror golang.org/x/net/http/httpguts.ValidHeaderFieldValue."""
    for ch in value:
        code = ord(ch)
        if code == 0x09:
            continue
        if code < 0x20 or code == 0x7F or code > 0xFF:
            return False
    return True


def is_safe_account_name(value: Any) -> bool:
    """Accept ordinary UTF-8 account labels without letting control data into SQL."""
    if not isinstance(value, str) or not 1 <= len(value) <= 128:
        return False
    try:
        encoded = value.encode("utf-8")
    except UnicodeEncodeError:
        return False
    return len(encoded) <= 512 and all(
        not unicodedata.category(character).startswith("C")
        for character in value
    )


def normalize_proxy_url(raw: str) -> Optional[str]:
    raw = (raw or "").strip()
    if not raw or raw.startswith("#"):
        return None
    if raw.startswith(("http://", "https://", "socks5://", "socks5h://")):
        return raw
    parts = raw.split(":")
    if len(parts) == 4:  # Webshare export: host:port:user:pass
        host, port, user, password = parts
        return f"http://{user}:{password}@{host}:{port}"
    if len(parts) == 2:
        host, port = parts
        return f"http://{host}:{port}"
    return None


def mask_proxy(proxy: str) -> str:
    try:
        parsed = urllib.parse.urlsplit(proxy)
        return f"{parsed.scheme}://***@{parsed.hostname}:{parsed.port}"
    except (ValueError, TypeError):
        return "<proxy>"


# --------------------------------------------------------------------------
# Feishu alerting over the deployment's Vault agent socket
# --------------------------------------------------------------------------

class FeishuNotifier:
    """Posts plain-text operational messages to the ops Feishu bot.

    The webhook URL is never stored in this tool's config: it is read on demand
    from the same Vault agent socket the backend uses, so the secret stays in
    one place.
    """

    def __init__(self, cfg: Dict[str, Any], state_dir: Path):
        self.enabled = bool(cfg.get("enabled", True))
        self.socket_path = cfg.get(
            "vault_socket",
            "/var/lib/docker/volumes/sub2api_feishu_vault/_data/public.sock",
        )
        self.vault_request_path = cfg.get(
            "vault_request_path", "/v1/secret/data/ops/feishu/payment"
        )
        self.vault_field = cfg.get("vault_field", "webhook_url")
        self.webhook_url_override = (cfg.get("webhook_url") or "").strip()
        self.cooldown_seconds = int(cfg.get("cooldown_seconds", 1800))
        self.state_file = state_dir / "alert-state.json"

    # -- cooldown bookkeeping ---------------------------------------------

    def _load_state(self) -> Dict[str, Any]:
        try:
            return json.loads(self.state_file.read_text(encoding="utf-8"))
        except Exception:  # noqa: BLE001 - missing or corrupt state restarts clean
            return {}

    def _save_state(self, state: Dict[str, Any]) -> None:
        try:
            self.state_file.parent.mkdir(parents=True, exist_ok=True)
            self.state_file.write_text(json.dumps(state, indent=2), encoding="utf-8")
        except Exception as exc:  # noqa: BLE001
            print(f"[!] Could not persist alert state: {exc}")

    # -- webhook resolution ------------------------------------------------

    def _read_webhook_from_vault(self) -> str:
        request = (
            f"GET {self.vault_request_path} HTTP/1.1\r\n"
            "Host: vault\r\n"
            "Accept: application/json\r\n"
            "Connection: close\r\n\r\n"
        ).encode("ascii")
        with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as sock:
            sock.settimeout(3)
            sock.connect(self.socket_path)
            sock.sendall(request)
            chunks: List[bytes] = []
            total = 0
            while total <= 8192:
                chunk = sock.recv(4096)
                if not chunk:
                    break
                chunks.append(chunk)
                total += len(chunk)
        raw = b"".join(chunks)
        head, _, body = raw.partition(b"\r\n\r\n")
        if b" 200 " not in head.split(b"\r\n")[0]:
            raise RuntimeError("vault agent returned a non-200 status")
        text = body.decode("utf-8", errors="replace").strip()
        if text and text[0] not in "{[":  # strip chunked-transfer framing
            text = "".join(
                line for line in text.splitlines() if line.strip()[:1] in "{[}]\"" or ":" in line
            )
        envelope = json.loads(text)
        value = envelope.get("data", {}).get("data", {}).get(self.vault_field, "")
        if not value:
            raise RuntimeError(f"vault payload has no field {self.vault_field}")
        return value

    def _resolve_webhook(self) -> str:
        if self.webhook_url_override:
            return self.webhook_url_override
        return self._read_webhook_from_vault()

    # -- delivery ----------------------------------------------------------

    def send(self, key: str, message: str, force: bool = False) -> bool:
        if not self.enabled:
            print("[*] Feishu alerting disabled by config; skipping notification.")
            return False

        state = self._load_state()
        now = time.time()
        last_sent = float(state.get(key, {}).get("last_sent", 0))
        if not force and now - last_sent < self.cooldown_seconds:
            remaining = int(self.cooldown_seconds - (now - last_sent))
            print(f"[*] Alert '{key}' suppressed by cooldown ({remaining}s remaining).")
            return False

        try:
            webhook_url = self._resolve_webhook()
        except Exception as exc:  # noqa: BLE001
            print(f"[!] Could not resolve Feishu webhook: {exc}")
            return False

        payload = json.dumps(
            {"msg_type": "text", "content": {"text": message}}
        ).encode("utf-8")
        request = urllib.request.Request(
            webhook_url,
            data=payload,
            headers={"Content-Type": "application/json", "Accept": "application/json"},
            method="POST",
        )
        try:
            with urllib.request.urlopen(request, timeout=5) as response:
                body = json.loads(response.read().decode("utf-8", errors="replace"))
        except Exception as exc:  # noqa: BLE001
            print(f"[!] Feishu delivery failed: {exc}")
            return False

        if body.get("code") not in (0, None) or body.get("StatusCode") not in (0, None):
            print(f"[!] Feishu rejected the message: {body}")
            return False

        state.setdefault(key, {})["last_sent"] = now
        self._save_state(state)
        print(f"[+] Feishu alert delivered (key={key}).")
        return True

    def clear(self, key: str) -> None:
        """Drop a cooldown entry so a recurrence alerts immediately."""
        state = self._load_state()
        if key in state:
            state.pop(key, None)
            self._save_state(state)


# --------------------------------------------------------------------------
# Sub2API host access
# --------------------------------------------------------------------------

class Sub2APIHost:
    """Reads and writes account state through the active Sub2API container.

    Every write reproduces the jsonb merge that accountRepository.UpdateExtra
    performs, so a pinned state written here is indistinguishable from one set
    through the admin API.
    """

    def __init__(self, cfg: Dict[str, Any]):
        self.node_state_script = cfg.get(
            "node_state_script", "/opt/sub2api/scripts/sub2api-node-state.sh"
        )
        self.container_override = (cfg.get("container") or "").strip()
        self.use_sudo = bool(cfg.get("use_sudo", True))
        self._container: Optional[str] = None

    def _sudo(self, args: List[str]) -> List[str]:
        return (["sudo"] + args) if self.use_sudo else args

    def active_container(self, force_refresh: bool = False) -> str:
        if self.container_override:
            return self.container_override
        if self._container and not force_refresh:
            return self._container
        proc = subprocess.run(
            self._sudo([self.node_state_script, "status"]),
            text=True,
            capture_output=True,
            check=True,
        )
        match = re.search(r"active_container=([^\s]+)", proc.stdout)
        if not match:
            raise RuntimeError("could not determine the active Sub2API container")
        self._container = match.group(1).strip()
        return self._container

    def run_sql(self, sql: str, read_only: bool = True) -> str:
        for attempt in range(2):
            container = self.active_container(force_refresh=(attempt > 0))
            cmd = self._sudo([
                "docker", "exec", "-i", container, "sh", "-c",
                'PGPASSWORD="$DATABASE_PASSWORD" PGSSLMODE="$DATABASE_SSLMODE" '
                'exec psql -h "$DATABASE_HOST" -p "$DATABASE_PORT" -U "$DATABASE_USER" '
                '-d "$DATABASE_DBNAME" -qAt',
            ])
            prologue = "BEGIN READ ONLY;\n" if read_only else "BEGIN;\n"
            try:
                proc = subprocess.run(
                    cmd,
                    input=prologue + sql + "\nCOMMIT;\n",
                    text=True,
                    capture_output=True,
                    check=True,
                )
                return proc.stdout
            except subprocess.CalledProcessError:
                self._container = None
                if attempt == 1:
                    raise
        return ""

    @staticmethod
    def _rows(output: str) -> List[str]:
        return [
            line
            for line in output.splitlines()
            if line.strip() not in {"", "BEGIN", "COMMIT", "UPDATE 0", "UPDATE 1"}
        ]

    def fetch_account(self, account_id: int, account_name: str) -> Dict[str, Any]:
        """Load the OAuth credentials and codex client version for one account."""
        if not is_safe_account_name(account_name):
            raise ValueError(f"unsafe account name: {account_name!r}")
        encoded_name = base64.b64encode(account_name.encode("utf-8")).decode("ascii")
        if not BASE64_RE.fullmatch(encoded_name):
            raise RuntimeError("encoded account name failed its own alphabet check")
        sql = f"""
SELECT jsonb_build_object(
  'token', a.credentials->>'access_token',
  'account', a.credentials->>'chatgpt_account_id',
  'device', a.extra->>'openai_device_id',
  'version', COALESCE(
    NULLIF((SELECT value FROM settings WHERE key='openai_codex_client_version'),''),
    NULLIF((SELECT value FROM settings WHERE key='openai_codex_client_version_synced'),''))
)
FROM accounts a
WHERE a.id = {int(account_id)}
  AND a.name = convert_from(decode('{encoded_name}', 'base64'), 'UTF8')
  AND a.status = 'active'
  AND a.deleted_at IS NULL;
"""
        rows = self._rows(self.run_sql(sql))
        if len(rows) != 1:
            raise RuntimeError(f"account {account_id}/{account_name} not found or not active")
        data = json.loads(rows[0])
        for field in ("token", "account", "version"):
            value = data.get(field)
            if not value or any(ch in str(value) for ch in "\r\n"):
                raise RuntimeError(
                    f"account {account_id} has an unusable '{field}' field"
                    + (
                        " — no codex client version is configured; set the"
                        " openai_codex_client_version setting rather than letting the probe guess"
                        if field == "version"
                        else ""
                    )
                )
        device = data.get("device")
        if device is not None and (not isinstance(device, str) or any(ch in device for ch in "\r\n")):
            raise RuntimeError("account has an invalid device field")
        data["device"] = (device or "").strip()
        # Guessing a version is worse than failing: upstream rejects gpt-6-astra
        # outright below MIN_CODEX_CLIENT_VERSION, which would surface as every
        # model failing to harvest for no visible reason.
        if _version_tuple(data["version"]) < _version_tuple(MIN_CODEX_CLIENT_VERSION):
            raise RuntimeError(
                f"codex client version {data['version']} is below the "
                f"{MIN_CODEX_CLIENT_VERSION} required by current models; "
                "update the openai_codex_client_version setting"
            )
        return data

    def read_pinned_states(self, account_id: int) -> Dict[str, Any]:
        sql = (
            "SELECT jsonb_build_object("
            "'states', COALESCE(extra->'pinned_codex_turn_states','{}'::jsonb), "
            "'account_cookie', COALESCE(extra->'pinned_codex_routing_cookie','{}'::jsonb)"
            f") FROM accounts WHERE id = {int(account_id)} AND deleted_at IS NULL;"
        )
        rows = self._rows(self.run_sql(sql))
        if not rows:
            return {}
        try:
            parsed = json.loads(rows[0])
            if not isinstance(parsed, dict):
                return {}
            if "states" not in parsed and "account_cookie" not in parsed:
                return parsed
            states = parsed.get("states") or {}
            acct_cookie_obj = parsed.get("account_cookie") or {}
            acct_cookie = acct_cookie_obj.get("cookie", "")
            acct_cookie_exp = acct_cookie_obj.get("expires_at", "")
            if acct_cookie and acct_cookie_exp:
                try:
                    exp_dt = datetime.fromisoformat(str(acct_cookie_exp).replace("Z", "+00:00"))
                    if exp_dt > datetime.now(timezone.utc):
                        for m_entry in states.values():
                            if isinstance(m_entry, dict):
                                m_exp = m_entry.get("cookie_expires_at", "")
                                if not m_exp or m_exp < acct_cookie_exp:
                                    m_entry["cookie"] = acct_cookie
                                    m_entry["cookie_expires_at"] = acct_cookie_exp
                except Exception:
                    pass
            return states
        except Exception:
            return {}

    def write_pinned_state(
        self,
        account_id: int,
        model: str,
        state: str,
        expires_at_iso: str,
        state_len: int,
        cookie: str = "",
        cookie_expires_at_iso: str = "",
    ) -> bool:
        """Merge one model's pinned state into accounts.extra.

        The merge happens entirely inside one UPDATE statement, at two levels:
        the outer `||` preserves every other key in `extra`, and the inner `||`
        preserves every other model already pinned. There is no read-modify-write
        window, so this cannot clobber a concurrent write from another worker,
        another process, or the admin UI updating the same JSONB column.

        Note that jsonb_set() cannot be used here: it leaves the target unchanged
        unless every path element above the last already exists, so it would
        silently no-op on an account that has no pinned states yet.

        The payload travels as base64 so no upstream-controlled byte can ever
        reach the SQL text; the alphabet is [A-Za-z0-9+/=] by construction.
        """
        if not MODEL_NAME_RE.match(model):
            raise ValueError(f"unsafe model name: {model!r}")
        if len(state) > MAX_STATE_BYTES:
            raise ValueError(f"state exceeds {MAX_STATE_BYTES} bytes")
        if not is_valid_header_value(state):
            raise ValueError("state contains characters illegal in an HTTP header value")
        if cookie and not is_valid_header_value(cookie):
            cookie = ""

        model_entry: Dict[str, Any] = {
            "state": state,
            "state_len": state_len,
            "expires_at": expires_at_iso,
            "updated_at": datetime.now(timezone.utc).replace(microsecond=0).isoformat(),
        }
        if cookie:
            model_entry["cookie"] = cookie
            if cookie_expires_at_iso:
                model_entry["cookie_expires_at"] = cookie_expires_at_iso

        entry = {model.lower(): model_entry}
        encoded = base64.b64encode(
            json.dumps(entry, ensure_ascii=True).encode("utf-8")
        ).decode("ascii")
        if not BASE64_RE.match(encoded):
            raise RuntimeError("encoded payload failed its own alphabet check")

        routing_cookie_sql = ""
        if cookie:
            cookie_payload = {
                "pinned_codex_routing_cookie": {
                    "cookie": cookie,
                    "expires_at": cookie_expires_at_iso,
                    "updated_at": datetime.now(timezone.utc).replace(microsecond=0).isoformat(),
                }
            }
            encoded_cookie = base64.b64encode(
                json.dumps(cookie_payload, ensure_ascii=True).encode("utf-8")
            ).decode("ascii")
            routing_cookie_sql = f"|| convert_from(decode('{encoded_cookie}', 'base64'), 'UTF8')::jsonb"

        sql = f"""
UPDATE accounts
SET extra = COALESCE(extra, '{{}}'::jsonb)
            {routing_cookie_sql}
            || jsonb_build_object(
                 'pinned_codex_turn_states',
                 COALESCE(extra->'pinned_codex_turn_states', '{{}}'::jsonb)
                 || convert_from(decode('{encoded}', 'base64'), 'UTF8')::jsonb
               ),
    updated_at = NOW()
WHERE id = {int(account_id)} AND deleted_at IS NULL;
"""
        self.run_sql(sql, read_only=False)
        written = self.read_pinned_states(account_id).get(model.lower(), {})
        return written.get("state") == state


# --------------------------------------------------------------------------
# probe
# --------------------------------------------------------------------------

PROBE_PAYLOAD = {
    "model": None,  # filled per call
    "input": [{"role": "user", "content": [{"type": "input_text", "text": "hi"}]}],
    "stream": True,
    "store": False,
    "instructions": "You are a coding assistant.",
}


def probe_turn_state(
    proxy_url: str,
    creds: Dict[str, Any],
    model: str,
    connect_timeout: int = 15,
    max_time: int = 30,
) -> Dict[str, Any]:
    """Issue one codex-tui-shaped request and abort as soon as the header lands.

    The response is a stream; tearing the connection down the moment
    x-codex-turn-state appears avoids waiting for the full generated stream.
    """

    def esc(value: Any) -> str:
        return str(value).replace("\\", "\\\\").replace('"', '\\"')

    with tempfile.TemporaryDirectory(prefix="codex-state-probe-") as raw_dir:
        os.chmod(raw_dir, 0o700)
        work = Path(raw_dir)
        request_file, header_file, body_file = work / "request.json", work / "headers", work / "body"

        payload = dict(PROBE_PAYLOAD)
        payload["model"] = model
        request_file.write_text(json.dumps(payload, separators=(",", ":")), encoding="utf-8")
        os.chmod(request_file, 0o600)

        user_agent = f'codex-tui/{creds["version"]} (Ubuntu 22.4.0; x86_64) xterm-256color'
        curl_config = [
            f'header = "Authorization: Bearer {esc(creds["token"])}"',
            'header = "Content-Type: application/json"',
            'header = "Accept: text/event-stream"',
            'header = "OpenAI-Beta: responses=experimental"',
            f'header = "ChatGPT-Account-ID: {esc(creds["account"])}"',
            'header = "Originator: codex-tui"',
            f'header = "User-Agent: {esc(user_agent)}"',
            f'header = "Version: {esc(creds["version"])}"',
            f'proxy = "{esc(proxy_url)}"',
        ]

        if creds.get("device"):
            curl_config.append(f'header = "X-Codex-Installation-ID: {esc(creds["device"])}"')

        started = time.monotonic()
        proc = subprocess.Popen(
            [
                "curl", "--config", "-", "--silent", "--show-error", "--http2",
                "--request", "POST", "--data-binary", "@" + str(request_file),
                "--dump-header", str(header_file), "--output", str(body_file),
                "--connect-timeout", str(connect_timeout), "--max-time", str(max_time),
                "https://chatgpt.com/backend-api/codex/responses",
            ],
            stdin=subprocess.PIPE,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.PIPE,
            text=True,
        )
        proc.stdin.write("\n".join(curl_config) + "\n")
        proc.stdin.close()

        status_code = 0
        response_stage = "unknown"
        headers: Dict[str, List[str]] = {}
        deadline = time.monotonic() + max_time
        while time.monotonic() < deadline:
            if header_file.exists():
                raw = header_file.read_text(encoding="iso-8859-1").replace("\r\n", "\n")
                blocks = [block for block in raw.split("\n\n") if block.strip()]
                for block in blocks:
                    lines = block.splitlines()
                    if not lines:
                        continue
                    match = re.match(r"HTTP/\S+\s+(\d{3})", lines[0])
                    if not match:
                        continue
                    current: Dict[str, List[str]] = {}
                    for line in lines[1:]:
                        if ":" in line:
                            key, value = line.split(":", 1)
                            current.setdefault(key.strip().lower(), []).append(value.strip())
                    if (
                        "x-codex-turn-state" in current
                        or "openai-model" in current
                        or int(match.group(1)) >= 400
                        or (len(blocks) > 1 and block == blocks[-1])
                    ):
                        status_code = int(match.group(1))
                        response_stage = "upstream" if len(blocks) > 1 else "proxy_connect"
                        headers = current
            if "x-codex-turn-state" in headers or proc.poll() is not None:
                break
            time.sleep(0.05)

        stderr = ""
        if proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(timeout=3)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait(timeout=3)
        else:
            stderr = (proc.stderr.read() or "").strip() if proc.stderr else ""

        state = (headers.get("x-codex-turn-state") or [""])[-1].strip()
        body_excerpt = ""
        if status_code >= 400 and body_file.exists():
            with body_file.open("rb") as response_body:
                body_excerpt = response_body.read(8192).decode("utf-8", errors="replace")
        diagnostic = classify_response(status_code, headers, body_excerpt, response_stage)

        # Parse routing cookies (__cflb, __oailb) from Set-Cookie headers
        raw_set_cookies = headers.get("set-cookie") or []
        parsed_cookies: Dict[str, str] = {}
        for sc in raw_set_cookies:
            first_part = sc.split(";", 1)[0].strip()
            if "=" in first_part:
                k, v = first_part.split("=", 1)
                k = k.strip()
                v = v.strip()
                if k and v:
                    parsed_cookies[k] = v

        routing_parts = []
        for rk in ["__cflb", "__oailb"]:
            if rk in parsed_cookies:
                routing_parts.append(f"{rk}={parsed_cookies[rk]}")
        routing_cookie_str = "; ".join(routing_parts)

        # Live tests show routing cookies persist for ~240s without continuous traffic.
        # Set expiry to 210s from now for safe operational buffer.
        now_utc = datetime.now(timezone.utc).replace(microsecond=0)
        cookie_expires_at = (now_utc + timedelta(seconds=210)).isoformat() if routing_cookie_str else None

        return {
            "http_status": status_code,
            "diagnostic": diagnostic,
            "state": state,
            "state_len": len(state),
            "cookie": routing_cookie_str,
            "cookies": parsed_cookies,
            "cookie_expires_at": cookie_expires_at,
            "served_model": (headers.get("openai-model") or [""])[-1].strip(),
            "retry_after": (headers.get("retry-after") or [""])[-1].strip(),
            "header_ms": int((time.monotonic() - started) * 1000),
            "error": stderr or None,
        }


# --------------------------------------------------------------------------
# manager
# --------------------------------------------------------------------------

class DegradedAccountsClient:
    """Reads /internal/degraded-accounts off the active backend container.

    The container publishes no host port and ships no curl, so the request goes
    over the docker bridge to the container's own address. That address changes
    on every blue/green switch, so it is resolved from the container the rest of
    this tool already discovered rather than being pinned in config.

    The monitor token is re-read per request: HealthService.Authorized does the
    same on its side, so rotating the file needs no restart here either.
    """

    def __init__(self, cfg: Dict[str, Any], host: "Sub2APIHost"):
        self.enabled = bool(cfg.get("enabled", True))
        self.host = host
        self.token_file = cfg.get(
            "token_file", "/opt/sub2api/secrets/internal-health-token"
        )
        self.port = int(cfg.get("port", 8080))
        self.window = cfg.get("window", "30m")
        self.timeout = int(cfg.get("timeout_seconds", 10))
        self.cache_seconds = int(cfg.get("cache_seconds", 30))
        self._lock = threading.Lock()
        self._cached: Optional[List[Dict[str, Any]]] = None
        self._cached_at = 0.0
        self._last_error = ""

    def _container_ip(self) -> str:
        container = self.host.active_container()
        proc = subprocess.run(
            self.host._sudo([
                "docker", "inspect", "-f",
                "{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}",
                container,
            ]),
            text=True,
            capture_output=True,
            check=True,
        )
        for candidate in proc.stdout.split():
            if candidate:
                return candidate
        raise RuntimeError(f"container {container} has no bridge address")

    def _token(self) -> str:
        return Path(self.token_file).read_text(encoding="utf-8").strip()

    def fetch(self, force: bool = False) -> Tuple[List[Dict[str, Any]], str]:
        if not self.enabled:
            return [], "disabled"
        with self._lock:
            fresh = time.time() - self._cached_at < self.cache_seconds
            if self._cached is not None and fresh and not force:
                return list(self._cached), self._last_error

        try:
            url = (
                f"http://{self._container_ip()}:{self.port}/internal/degraded-accounts"
                f"?window={urllib.parse.quote(self.window)}"
            )
            request = urllib.request.Request(
                url,
                headers={
                    "X-Monitor-Token": self._token(),
                    "User-Agent": "codex-state-manager/2.0",
                },
            )
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
            with opener.open(request, timeout=self.timeout) as response:
                body = response.read().decode("utf-8", errors="replace")
            payload = json.loads(body)
            if not isinstance(payload, list):
                # Only the 200 branch is a bare array. Anything else means the
                # request reached something other than the handler -- most
                # likely the embedded frontend, which answers 200 with HTML.
                raise ValueError("expected a JSON array")
            error = ""
        except Exception as exc:  # noqa: BLE001
            payload, error = [], "backend query unavailable"

        with self._lock:
            if error and self._cached is not None:
                # Serve the last good answer rather than blanking the panel on
                # one failed poll; the error is surfaced alongside it.
                self._last_error = error
                return list(self._cached), error
            self._cached = payload
            self._cached_at = time.time()
            self._last_error = error
            return list(payload), error


class StateManager:
    def __init__(self, config_path: Path):
        self.config_path = config_path
        self.config = self._load_config()
        self.host = Sub2APIHost(self.config.get("sub2api", {}))
        state_dir = Path(
            self.config.get("state_dir", "/var/lib/codex-turn-state-manager")
        )
        self.state_dir = state_dir
        state_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
        state_dir.chmod(0o700)
        self.stats = ProbeStats(state_dir)
        self._accounts_overlay_file = state_dir / "accounts.json"
        self._accepted_overlay = {}
        self._error_streaks = {}
        self._short_retries = set()
        self._diagnostics = {}
        self.notifier = FeishuNotifier(self.config.get("alerts", {}), state_dir)
        self.proxies: List[str] = []
        self._rotating = False
        self._dynamic_proxies = []
        self._proxy_sources = {}
        self._dynamic_indices = {}
        self._static_pending = {}
        self._continue_harvest = False
        self._harvest_retry_delay = 0
        self._forced_pending = set()
        self._probe_attempts = {}
        self._creds_cache: Dict[int, Dict[str, Any]] = {}
        self._retry_after: Dict[str, float] = {}
        self._logged_skips: Dict[str, float] = {}
        try:
            saved = json.loads((state_dir / "probe-diagnostics.json").read_text())
            now = time.time()
            for slot, row in saved.items():
                if row.get("http_status") not in (403, 429):
                    continue
                deadline = float(row["at"]) + float(row["retry_delay_seconds"])
                if math.isfinite(deadline) and deadline > now:
                    self._retry_after[slot] = deadline
                    self._diagnostics[slot] = row
        except (OSError, ValueError, TypeError, AttributeError, KeyError):
            pass
        self.failure_backoff_seconds = int(
            self.config.get("failure_backoff_seconds", 30)
        )
        self._usage_lock = threading.Lock()
        # Guards _retry_after only. Writes need no lock: write_pinned_state does
        # its whole merge inside one UPDATE statement.
        self._retry_lock = threading.Lock()
        if "max_workers" in self.config:
            print(
                "[!] config key 'max_workers' is obsolete and ignored: a single"
                " account's models are now probed one at a time. Remove it."
            )
        self.refresh_advance_minutes = float(
            self.config.get("refresh_advance_minutes", 15)
        )
        self._settings_file = state_dir / "settings.json"
        self._settings_lock = threading.RLock()
        self._settings_override = None
        self._settings_error = False
        self._reload_settings()
        self._renewal_file = state_dir / "renewal-timing.json"
        self._renewal_lock = threading.RLock()
        self._renewal_started = {}
        self._renewal_samples = self._load_renewal_samples()

        # Caps how many addresses one pass may burn. Without it, a pass over a
        # large pool costs len(pool) x request_timeout in the worst case (21
        # proxies x 30s = 10.5 min), which alone can outlast the refresh window
        # it was started in. A bounded pass fails fast, backs off, and comes
        # back on fresher addresses instead of grinding through a flagged pool.
        self.max_probes_per_pass = int(self.config.get("max_probes_per_pass", 8))
        self.proxy_cooldown_seconds = int(
            self.config.get("proxy_cooldown_seconds", 900)
        )
        self._proxy_state_file = state_dir / "proxy-usage.json"
        self._proxy_usage: Dict[str, Dict[str, Any]] = self._load_proxy_usage()
        # -- panel plumbing ------------------------------------------------
        # The panel runs its HTTP server on other threads, but it must never
        # probe there: that would undo the "one account's models are probed
        # strictly one at a time" guarantee below. Instead a panel request only
        # enqueues and wakes the daemon, and every probe still happens on the
        # daemon thread. Serialisation stays true by construction rather than
        # by locking discipline.
        self._wake = threading.Event()
        self._manual_queue: "collections.deque[Dict[str, Any]]" = collections.deque()
        self._queue_lock = threading.Lock()
        self._jobs: "collections.OrderedDict[str, Dict[str, Any]]" = collections.OrderedDict()
        self._jobs_lock = threading.Lock()
        self._job_slots = {}

        self._overlay_file = state_dir / "accounts.json"
        self._overlay_lock = threading.RLock()
        self._history_file = state_dir / "probe-history.json"
        self._history_lock = threading.Lock()
        self._history: Dict[str, List[Dict[str, Any]]] = self._load_history()
        self.degraded = DegradedAccountsClient(self.config.get("degraded", {}), self.host)

        self._refresh_proxies()

    def accounts(self):
        with self._overlay_lock:
            return self._accounts_unlocked()

    def _accounts_unlocked(self):
        """Hot-reload non-secret full-account overlays; invalid edits keep last good config."""
        def validate(entry, key=None):
            if not isinstance(entry, dict):
                raise ValueError("account must be object")
            account = dict(entry)
            account["id"] = int(key if key is not None else account["id"])
            if account["id"] <= 0 or not is_safe_account_name(account.get("name", "")):
                raise ValueError("invalid account identity")
            if not isinstance(account.get("enabled", True), bool):
                raise ValueError("enabled must be boolean")
            advance = self.account_advance_minutes(account)
            if not 0 <= advance < 60:
                raise ValueError("invalid refresh window")
            models = account.get("models", [])
            if not isinstance(models, list):
                raise ValueError("models must be array")
            checked, seen = [], set()
            for item in models:
                if not isinstance(item, dict):
                    raise ValueError("model must be object")
                model = dict(item)
                name = model.get("name", "")
                if not isinstance(name, str) or not MODEL_NAME_RE.fullmatch(name):
                    raise ValueError("invalid model name")
                if name.lower() in seen:
                    raise ValueError("duplicate model")
                seen.add(name.lower())
                if not isinstance(model.get("enabled", True), bool):
                    raise ValueError("model enabled must be boolean")
                if not isinstance(model.get("require_exact_len", True), bool):
                    raise ValueError("require_exact_len must be boolean")
                raw_length = model.get("target_state_len", 292)
                if isinstance(raw_length, bool) or not isinstance(raw_length, int):
                    raise ValueError("state target must be integer")
                length = raw_length
                if not 1 <= length <= MAX_STATE_BYTES:
                    raise ValueError("invalid state target")
                model["target_state_len"] = length
                if model.get("enabled", True):
                    checked.append(model)
            account["models"] = checked
            return account

        baseline = {str(int(a["id"])): dict(a) for a in self.config.get("accounts", [])}
        try:
            overlay = json.loads(self._accounts_overlay_file.read_text())
            if not isinstance(overlay, dict):
                raise ValueError("overlay must be object")
            candidate = dict(baseline)
            for key, value in overlay.items():
                if str(int(key)) != key or int(key) <= 0:
                    raise ValueError("non-canonical account id")
                if not isinstance(value, dict):
                    raise ValueError("overlay account must be object")
                merged = dict(candidate.get(key, {}))
                merged.update(value)
                candidate[key] = validate(merged, key)
            # Validate the entire resulting view before accepting the overlay.
            for key, value in candidate.items():
                validate(value, key)
            self._accepted_overlay = overlay
        except FileNotFoundError:
            self._accepted_overlay = {}
        except (OSError, ValueError, TypeError, KeyError, OverflowError):
            print("[!] Invalid account overlay; keeping last validated settings.")
        merged = dict(baseline)
        for key, value in self._accepted_overlay.items():
            entry = dict(merged.get(key, {}))
            entry.update(value)
            merged[key] = entry
        result = [validate(a, key) for key, a in merged.items()]
        return [a for a in result if a.get("enabled", True)]

    def _load_overlay(self) -> Dict[str, Dict[str, Any]]:
        # Never overwrite a corrupt operator file with an empty overlay.
        try:
            raw = json.loads(self._overlay_file.read_text(encoding="utf-8"))
        except FileNotFoundError:
            return {}
        if not isinstance(raw, dict):
            raise ValueError("invalid account overlay; repair the file before editing")
        self._accounts_unlocked()
        if raw != self._accepted_overlay:
            raise ValueError("invalid account overlay; repair the file before editing")
        return raw

    def _save_overlay(self, overlay: Dict[str, Dict[str, Any]]) -> None:
        atomic_write_json(self._overlay_file, overlay)
        self._accounts_unlocked()

    @staticmethod
    def _validate_settings(value):
        if not isinstance(value, dict) or set(value) != {"refresh_advance_minutes"}:
            raise ValueError("invalid renewal settings")
        minutes = value["refresh_advance_minutes"]
        if isinstance(minutes, bool) or not isinstance(minutes, int) or not 1 <= minutes <= 30:
            raise ValueError("refresh advance must be an integer from 1 to 30")
        return {"refresh_advance_minutes": minutes}

    def _read_settings_file(self):
        try:
            return self._validate_settings(json.loads(self._settings_file.read_text()))
        except FileNotFoundError:
            return None

    def _reload_settings(self):
        with self._settings_lock:
            try:
                candidate = self._read_settings_file()
                if candidate is not None:
                    self._settings_override = candidate
                self._settings_error = False
            except (OSError, ValueError, TypeError):
                self._settings_error = True

    def settings_snapshot(self):
        self._reload_settings()
        with self._settings_lock:
            value = self._settings_override or {"refresh_advance_minutes": self.refresh_advance_minutes}
            return {**value, "error": "invalid settings file; last valid value retained" if self._settings_error else None}

    def update_settings(self, value):
        candidate = self._validate_settings(value)
        with self._settings_lock:
            # Never replace a corrupt operator file with an assumed empty value.
            self._read_settings_file()
            atomic_write_json(self._settings_file, candidate)
            self._settings_override = candidate
            self._settings_error = False
        self._wake.set()
        return self.settings_snapshot()

    def _load_renewal_samples(self):
        try:
            rows = json.loads(self._renewal_file.read_text())
            if not isinstance(rows, list):
                return []
            return [row for row in rows[-200:]
                    if isinstance(row, dict)
                    and isinstance(row.get("seconds"), (int, float)) and not isinstance(row["seconds"], bool)
                    and math.isfinite(row["seconds"]) and 0 <= row["seconds"]
                    and isinstance(row.get("at"), int) and not isinstance(row["at"], bool)]
        except (OSError, ValueError, TypeError):
            return []

    def renewal_timing_snapshot(self):
        with self._renewal_lock:
            rows = list(self._renewal_samples)
        return {"samples": len(rows), "average_seconds": sum(row["seconds"] for row in rows) / len(rows) if rows else None,
                "last_seconds": rows[-1]["seconds"] if rows else None,
                "since": rows[0]["at"] if rows else None, "window": "last_200_successes"}

    def _record_renewal_success(self, slot):
        started = self._renewal_started.pop(slot, None)
        if started is None:
            return
        seconds = max(0.0, time.monotonic() - started)
        with self._renewal_lock:
            rows = (self._renewal_samples + [{"at": int(time.time()), "seconds": round(seconds, 3)}])[-200:]
            # A telemetry storage error must not invalidate a verified pin.
            try:
                atomic_write_json(self._renewal_file, rows)
            except OSError:
                print("[!] Renewal timing persistence failed.")
            self._renewal_samples = rows

    def account_advance_minutes(self, account: Dict[str, Any]) -> float:
        """Single source for the refresh window, used by both the status table
        and the refresh decision. These two used to carry different defaults."""
        with self._settings_lock:
            if self._settings_override is not None:
                return float(self._settings_override["refresh_advance_minutes"])
        return float(account.get("refresh_advance_minutes", self.refresh_advance_minutes))

    def _load_history(self) -> Dict[str, List[Dict[str, Any]]]:
        try:
            raw = json.loads(self._history_file.read_text(encoding="utf-8"))
        except (OSError, ValueError):
            return {}
        if not isinstance(raw, dict):
            return {}
        clean = {}
        for slot, entries in raw.items():
            account, sep, model = slot.partition(":")
            if not sep or not account.isdigit() or not MODEL_NAME_RE.fullmatch(model) or not isinstance(entries, list):
                continue
            rows = []
            for entry in entries[-PROBE_HISTORY_PER_SLOT:]:
                if not isinstance(entry, dict):
                    continue
                # Legacy drafts stored proxy/userinfo and exception strings. Do
                # not expose them just because this release has safe new writes.
                row = {k: entry[k] for k in ("at", "state_len", "http_status", "header_ms")
                       if isinstance(entry.get(k), (int, float)) and not isinstance(entry.get(k), bool)}
                row["ok"] = entry.get("ok") is True
                outcome = entry.get("outcome")
                row["outcome"] = outcome if outcome in ("saved", "target_hit", "miss") else "legacy"
                source = entry.get("source", "legacy")
                row["source"] = source if isinstance(source, str) and re.fullmatch(r"[a-z0-9][a-z0-9_-]{0,63}", source) else "legacy"
                rows.append(row)
            clean[slot] = rows
        return clean

    def record_probe(self, slot: str, entry: Dict[str, Any]) -> None:
        with self._history_lock:
            bucket = self._history.setdefault(slot, [])
            bucket.append(entry)
            # Ring buffer: the panel shows a recent trend, not an audit log.
            del bucket[:-PROBE_HISTORY_PER_SLOT]
            snapshot = {key: list(value) for key, value in self._history.items()}
        try:
            atomic_write_json(self._history_file, snapshot)
        except OSError as exc:
            print(f"[!] Could not persist probe history: {exc}")

    def history_snapshot(self) -> Dict[str, List[Dict[str, Any]]]:
        with self._history_lock:
            return {key: list(value) for key, value in self._history.items()}

    def upsert_account(self, account_id: int, name: str, model: str,
                       target_state_len: int) -> Dict[str, Any]:
        if not is_safe_account_name(name):
            raise ValueError("invalid account name")
        if not MODEL_NAME_RE.fullmatch(model or "") or account_id <= 0:
            raise ValueError("invalid account or model")
        if isinstance(target_state_len, bool) or not isinstance(target_state_len, int) or not 1 <= target_state_len <= MAX_STATE_BYTES:
            raise ValueError("target length must be between 1 and 8192")
        with self._overlay_lock:
            overlay = self._load_overlay()
            key = str(account_id)
            entry = dict(next((a for a in self.config.get("accounts", [])
                               if int(a["id"]) == account_id), {}))
            entry.update(overlay.get(key) or {})
            models = [dict(m) for m in entry.get("models", [])
                      if m.get("name", "").lower() != model.lower()]
            models.append({"name": model.lower(), "target_state_len": target_state_len,
                           "require_exact_len": True})
            entry.update(id=account_id, name=name, models=models, enabled=True,
                         source="panel", added_at=datetime.now(timezone.utc).isoformat())
            overlay[key] = entry
            self._save_overlay(overlay)
        self._wake.set()
        return entry

    def remove_model(self, account_id: int, model: str) -> bool:
        """Drop one model from the overlay.

        Removing the last model parks the whole account with enabled=false
        rather than deleting the key, so an account that came from the
        config.json baseline can also be stopped from the panel.
        """
        with self._overlay_lock:
            overlay = self._load_overlay()
            key = str(int(account_id))
            entry = overlay.get(key)
            if entry is None:
                baseline = next(
                    (a for a in self.config.get("accounts", []) if int(a.get("id", 0)) == account_id),
                    None,
                )
                if baseline is None:
                    return False
                entry = dict(baseline)
            if not any(m.get("name", "").lower() == model.lower() for m in entry.get("models", [])):
                return False
            remaining = [m for m in entry.get("models", []) if m.get("name", "").lower() != model.lower()]
            entry["models"] = remaining
            entry["id"] = int(account_id)
            if not remaining:
                entry["enabled"] = False
            overlay[key] = entry
            self._save_overlay(overlay)
        return True

    def get_account_models(self, account_id: int) -> List[str]:
        """Fetch models configured on this account (model_mapping, etc.) plus standard models."""
        models_set = set()
        standard_priority = ["gpt-6-astra", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-6", "gpt-5.5"]
        for default_m in standard_priority:
            models_set.add(default_m)

        sql = f"""
SELECT jsonb_build_object(
  'model_mapping', COALESCE(credentials->'model_mapping', '{{}}'::jsonb),
  'model_whitelist', COALESCE(credentials->'model_whitelist', '[]'::jsonb),
  'pinned_states', COALESCE(extra->'pinned_codex_turn_states', '{{}}'::jsonb)
)
FROM accounts
WHERE id = {int(account_id)} AND deleted_at IS NULL;
"""
        try:
            rows = self.host._rows(self.host.run_sql(sql))
            if rows:
                data = json.loads(rows[0])
                mm = data.get("model_mapping") or {}
                if isinstance(mm, dict):
                    for k in mm.keys():
                        if k and isinstance(k, str) and MODEL_NAME_RE.fullmatch(k):
                            models_set.add(k.lower())
                mw = data.get("model_whitelist") or []
                if isinstance(mw, list):
                    for item in mw:
                        if item and isinstance(item, str) and MODEL_NAME_RE.fullmatch(item):
                            models_set.add(item.lower())
                ps = data.get("pinned_states") or {}
                if isinstance(ps, dict):
                    for k in ps.keys():
                        if k and isinstance(k, str) and MODEL_NAME_RE.fullmatch(k):
                            models_set.add(k.lower())
        except Exception as exc:
            print(f"[!] get_account_models({account_id}) error: {exc}")

        ordered = [m for m in standard_priority if m in models_set]
        remaining = sorted([m for m in models_set if m not in standard_priority])
        return ordered + remaining

    def enqueue_manual(self, account_id: int, model: Optional[str], force: bool) -> str:
        if model is not None and (not isinstance(model, str) or not MODEL_NAME_RE.fullmatch(model)):
            raise ValueError("invalid model")
        account = next((a for a in self.accounts() if int(a["id"]) == account_id), None)
        if account is None:
            raise ValueError("account is not in the active probe list")
        names = [m["name"] for m in account.get("models", [])]
        if model is not None:
            model = next((n for n in names if n.lower() == model.lower()), None)
            if model is None:
                raise ValueError("model is not in the active probe list")
        slots = {f"{account_id}:{n}" for n in names if model is None or n == model}
        if not slots:
            raise ValueError("account has no active models")
        with self._jobs_lock:
            active = [j for j in self._jobs.values() if j["status"] in ("queued", "running", "waiting")]
            for j in active:
                if j["account_id"] == account_id and j["model"] == model:
                    return j["id"]
                if slots.intersection(self._job_slots.get(j["id"], set())):
                    raise ValueError("a probe for this target is already pending")
            if len(active) >= 32:
                raise ValueError("manual probe queue is full")
            job_id = uuid.uuid4().hex[:12]
            job = dict(id=job_id, account_id=account_id, model=model, force=force,
                       status="queued", queued_at=int(time.time()), started_at=None,
                       finished_at=None, updated=0, error=None)
            self._job_slots[job_id] = slots
            self._jobs[job_id] = job
            while len(self._jobs) > 200:
                oldest = next(k for k,v in self._jobs.items()
                              if v["status"] not in ("queued", "running", "waiting"))
                del self._jobs[oldest]
                self._job_slots.pop(oldest, None)
            with self._queue_lock:
                self._manual_queue.append(dict(job))
        self._wake.set()
        return job_id

    def job(self, job_id: str) -> Optional[Dict[str, Any]]:
        with self._jobs_lock:
            found = self._jobs.get(job_id)
            return dict(found) if found else None

    def jobs(self) -> List[Dict[str, Any]]:
        with self._jobs_lock:
            return [dict(job) for job in reversed(self._jobs.values())][:40]

    def _set_job(self, job_id: str, **fields: Any) -> None:
        with self._jobs_lock:
            job = self._jobs.get(job_id)
            if job:
                job.update(fields)

    def drain_manual_queue(self) -> int:
        # A bounded snapshot prevents HTTP requests starving the scheduled sweep.
        with self._queue_lock:
            queued = list(self._manual_queue)
            self._manual_queue.clear()
        for job in queued:
            live = {f"{a['id']}:{m['name']}" for a in self.accounts() for m in a.get("models", [])}
            if not self._job_slots[job["id"]].issubset(live):
                self._set_job(job["id"], status="cancelled", finished_at=int(time.time()))
                continue
            self._set_job(job["id"], status="running", started_at=int(time.time()))
            try:
                if job["force"]:
                    self._forced_pending.update(self._job_slots[job["id"]])
                updated = self.run_check_and_refresh(force=job["force"],
                    only_account=job["account_id"], only_model=job["model"])
                pending = self._job_slots[job["id"]].intersection(self._forced_pending)
                self._job_slots[job["id"]] = pending
                self._set_job(job["id"], status="waiting" if pending else "done",
                              updated=updated, finished_at=None if pending else int(time.time()))
            except Exception:
                self._set_job(job["id"], status="error", error="probe failed; inspect service diagnostics",
                              finished_at=int(time.time()))
        return len(queued)

    def reconcile_manual_jobs(self) -> None:
        live = {f"{a['id']}:{m['name']}" for a in self.accounts() for m in a.get("models", [])}
        with self._jobs_lock:
            for job in self._jobs.values():
                if job["status"] != "waiting":
                    continue
                slots = self._job_slots[job["id"]]
                if not slots.issubset(live):
                    job.update(status="cancelled", finished_at=int(time.time()))
                    continue
                remaining = slots.intersection(self._forced_pending)
                job["updated"] += len(slots - remaining)
                self._job_slots[job["id"]] = remaining
                if not remaining:
                    job.update(status="done", finished_at=int(time.time()))

    def source_stats(self, start_day=None, end_day=None):
        # Each HTTP thread owns a read-only connection; daemon SQLite is thread-bound.
        with ProbeStats(self.state_dir, read_only=True) as stats:
            return stats.report(start_day, end_day)

    def snapshot(self) -> Dict[str, Any]:
        """Everything the panel renders, in one read-only pass."""
        self._reload_settings()
        now = time.time()
        degraded, degraded_error = self.degraded.fetch()

        # Index the backend's view by account+model so each row can answer the
        # "still degraded after the last pin?" question below.
        by_slot: Dict[str, Dict[str, Any]] = {}
        for row in degraded:
            key = f"{row.get('account_id')}:{str(row.get('sent_model', '')).lower()}"
            existing = by_slot.get(key)
            if not existing or str(row.get("last_seen", "")) > str(existing.get("last_seen", "")):
                by_slot[key] = row

        accounts: List[Dict[str, Any]] = []
        for account in self.accounts():
            account_id = int(account["id"])
            advance = self.account_advance_minutes(account)
            entry: Dict[str, Any] = {
                "id": account_id,
                "name": account.get("name", str(account_id)),
                "refresh_advance_minutes": advance,
                "source": account.get("source", "config"),
                "models": [],
                "error": None,
            }
            try:
                pinned = self.host.read_pinned_states(account_id)
            except Exception as exc:  # noqa: BLE001
                entry["error"] = "account state unavailable; inspect service diagnostics"
                pinned = {}

            for model_cfg in account.get("models", []):
                model = model_cfg["name"]
                target_len = int(model_cfg.get("target_state_len", 292))
                pin = pinned.get(model.lower(), {}) or {}
                state = pin.get("state", "")
                info = inspect_turn_state(state) if state else {"valid": False, "length": 0}

                slot = f"{account_id}:{model}"
                with self._retry_lock:
                    retry_at = self._retry_after.get(slot, 0.0)

                cookie = pin.get("cookie", "")
                cookie_exp_str = pin.get("cookie_expires_at", "")
                cookie_remaining = 0
                cookie_valid = False
                if cookie and cookie_exp_str:
                    try:
                        c_exp_dt = datetime.fromisoformat(str(cookie_exp_str).replace("Z", "+00:00"))
                        cookie_remaining = max(0, int((c_exp_dt - datetime.now(timezone.utc)).total_seconds()))
                        cookie_valid = cookie_remaining > 0
                    except Exception:
                        pass

                row: Dict[str, Any] = {
                    "model": model,
                    "target_len": target_len,
                    "state_len": info.get("length", 0),
                    "valid": bool(info.get("valid")),
                    "expired": bool(info.get("is_expired")),
                    "expires_at": info.get("expires_at"),
                    "remaining_minutes": info.get("remaining_minutes", 0),
                    "remaining_seconds": info.get("remaining_seconds", 0),
                    "cookie_present": bool(cookie),
                    "cookie_valid": cookie_valid,
                    "cookie_remaining_seconds": cookie_remaining,
                    "cookie_expires_at": cookie_exp_str,
                    "pinned_updated_at": pin.get("updated_at"),
                    # A pinned state whose length differs from the target is the
                    # signal that a harvest silently regressed.
                    "length_degraded": bool(info.get("valid")) and info.get("length") != target_len,
                    "backoff_seconds": max(0, int(retry_at - now)),
                    "next_probe_seconds": 0,
                    "degradation": "unknown" if degraded_error else self._degradation_verdict(
                        by_slot.get(f"{account_id}:{model.lower()}"), pin
                    ),
                    "recent_degradation": by_slot.get(f"{account_id}:{model.lower()}"),
                }
                if row["backoff_seconds"] <= 0 and info.get("valid"):
                    if cookie:
                        # Cookie routing mode: refresh proactively when cookie has <= 60s left
                        row["next_probe_seconds"] = max(0, int(cookie_remaining - 60))
                    else:
                        row["next_probe_seconds"] = max(
                            0, int(float(info.get("remaining_seconds", 0)) - advance * 60)
                        )
                entry["models"].append(row)
            accounts.append(entry)

        return {
            "generated_at": int(now),
            "settings": self.settings_snapshot(),
            "renewal_timing": self.renewal_timing_snapshot(),
            "read_only": bool(self.config.get("panel", {}).get("read_only", False)),
            "degraded_enabled": self.degraded.enabled,
            "accounts": accounts,
            "degraded": degraded,
            "degraded_error": degraded_error,
            "degraded_window": self.degraded.window,
            "proxy_count": len(self.proxies),
            "static_proxy_count": len([p for p in self.proxies if p not in self._dynamic_proxies]),
            "dynamic_provider_count": len(self._dynamic_proxies),
            "poll_interval_seconds": int(self.config.get("poll_interval_seconds", 60)),
            "jobs": self.jobs(),
        }

    @staticmethod
    def _degradation_verdict(row: Optional[Dict[str, Any]], pin: Dict[str, Any]) -> str:
        # These are model mismatch observations, not a model-quality measurement.
        if not row:
            return "no_record"
        try:
            last = datetime.fromisoformat(str(row.get("last_seen", "")).replace("Z", "+00:00"))
            pinned = datetime.fromisoformat(str(pin.get("updated_at", "")).replace("Z", "+00:00"))
            if last.tzinfo is None or pinned.tzinfo is None:
                return "unknown"
        except (ValueError, TypeError):
            return "unknown"
        return "degraded" if last > pinned else "handled"

    def _record_diagnostic(self, slot, diagnostic, state_len):
        # Diagnostic helper emits only allowlisted codes/IDs, never raw content.
        last_error = self._diagnostics.get(slot, {}).get("last_error")
        if diagnostic.get("http_status", 0) >= 400:
            last_error = dict(diagnostic, at=int(time.time()))
        self._diagnostics[slot] = dict(diagnostic, at=int(time.time()),
                                      state_len=state_len,
                                      consecutive_errors=self._error_streaks.get(slot, 0),
                                      retry_delay_seconds=self._harvest_retry_delay,
                                      last_error=last_error)
        try:
            self.state_dir.mkdir(parents=True, exist_ok=True)
            with tempfile.NamedTemporaryFile(mode="w", dir=self.state_dir,
                                             prefix=".diagnostic-", delete=False) as out:
                tmp = Path(out.name)
                json.dump(self._diagnostics, out)
            os.replace(tmp, self.state_dir / "probe-diagnostics.json")
        except OSError:
            print("[!] Could not persist sanitized diagnostics.")

    # -- proxy usage bookkeeping ------------------------------------------

    @staticmethod
    def _proxy_identity(proxy: str) -> str:
        return hashlib.sha256(proxy.encode()).hexdigest()

    def _load_proxy_usage(self) -> Dict[str, Dict[str, Any]]:
        try:
            data = json.loads(self._proxy_state_file.read_text(encoding="utf-8"))
            clean = {(key if re.fullmatch(r"[0-9a-f]{64}", key) else self._proxy_identity(key)): value
                     for key, value in data.items()}
            # Migrate legacy credential-bearing keys before returning control.
            self._write_proxy_usage(clean)
            return clean
        except FileNotFoundError:
            return {}
        except (ValueError, TypeError, AttributeError):
            return {}

    def _write_proxy_usage(self, data) -> None:
        with tempfile.NamedTemporaryFile(mode="w", dir=self.state_dir,
                                         prefix=".proxy-usage-", delete=False) as out:
            tmp = Path(out.name)
            json.dump(data, out)
            out.flush()
            os.fsync(out.fileno())
        try:
            os.replace(tmp, self._proxy_state_file)
        finally:
            tmp.unlink(missing_ok=True)

    def _save_proxy_usage(self) -> None:
        with self._usage_lock:
            data = dict(self._proxy_usage)
        try:
            self._write_proxy_usage(data)
        except OSError:
            print("[!] Could not persist proxy usage state.")

    def _note_proxy_use(self, proxy: str, state_len: int) -> None:
        with self._usage_lock:
            self._proxy_usage[self._proxy_identity(proxy)] = {"last_used": time.time(), "last_state_len": state_len}

    def _ordered_proxies(self) -> List[str]:
        """Least-recently-used first, resting addresses moved to the back.

        Resting entries are appended rather than dropped so a pool that is
        entirely within its cooldown window still gets tried instead of
        reporting a false 'no clean IP' alert.

        Successive passes do not need a rotation offset: every probe calls
        _note_proxy_use, so an address a previous model just tried has already
        sunk to the back of this ordering.
        """
        now = time.time()
        ready, resting = [], []
        with self._usage_lock:
            usage = dict(self._proxy_usage)
        for proxy in self.proxies:
            last_used = float(usage.get(self._proxy_identity(proxy), {}).get("last_used", 0))
            (resting if now - last_used < self.proxy_cooldown_seconds else ready).append(
                (last_used, proxy)
            )
        ready.sort(key=lambda item: item[0])
        resting.sort(key=lambda item: item[0])
        if resting and not ready:
            print(f"[*] All {len(resting)} proxies are within their cooldown; trying oldest first.")
        return [proxy for _, proxy in ready] + [proxy for _, proxy in resting]

    def _load_config(self) -> Dict[str, Any]:
        if not self.config_path.exists():
            raise FileNotFoundError(
                f"Config not found: {self.config_path}\n"
                f"Copy {self.config_path.name}.example and fill it in."
            )
        return json.loads(self.config_path.read_text(encoding="utf-8"))

    # -- proxy pool --------------------------------------------------------

    def _refresh_proxies(self) -> None:
        pool: List[str] = []
        sources = self.config.get("proxies", {}).get("sources", [])
        rotating = [x for x in sources if x.get("enabled", True)
                    and x.get("type") == "rotating_residential"]
        self._rotating = bool(rotating)
        self._proxy_sources = {}
        if rotating:
            # A gateway URL is not an exit IP. New connections through the
            # ordinary endpoint rotate at the provider; never add sticky IDs.
            for source in rotating:
                name = source.get("name", "rainproxy")
                if not isinstance(name, str) or not re.fullmatch(r"[a-z][a-z0-9_-]{0,63}", name) or name == "static":
                    raise ValueError("invalid dynamic source name")
                if source.get("endpoint_file"):
                    try:
                        path = Path(source["endpoint_file"])
                        if not path.is_absolute():
                            path = self.config_path.parent / path
                        raw = json.loads(path.read_text())["proxy_url"]
                        if not isinstance(raw, str) or any(c in raw for c in "\r\n\x00"):
                            raise ValueError("invalid endpoint")
                        url = normalize_proxy_url(raw)
                        parsed = urllib.parse.urlsplit(url or "")
                        if parsed.scheme not in ("http", "https", "socks5", "socks5h") or not parsed.hostname or not parsed.port:
                            raise ValueError("invalid endpoint")
                    except (OSError, ValueError, KeyError, TypeError):
                        print(f"[!] [{name}] protected proxy endpoint unavailable; source skipped.")
                        continue
                    if not url:
                        print(f"[!] [{name}] invalid proxy endpoint; source skipped.")
                        continue
                else:
                    user = source.get("user", "")
                    password = source.get("password", "")
                    gateway = source.get("gateway", "")
                    country = source.get("country", "us")
                    if not user or not password or not gateway:
                        continue
                    url = ("http://" + urllib.parse.quote(user + "-country-" + country, safe="")
                           + ":" + urllib.parse.quote(password, safe="") + "@" + gateway)
                if url not in pool:
                    pool.append(url)
                    self._proxy_sources[url] = name
        self._dynamic_proxies = list(pool)
        pool = []

        def add(raw: str) -> None:
            norm = normalize_proxy_url(raw)
            if norm and norm not in pool:
                pool.append(norm)

        proxy_cfg = self.config.get("proxies", {})
        for entry in proxy_cfg.get("static_proxies", []):
            add(entry)

        for source in proxy_cfg.get("sources", []):
            if not source.get("enabled", True):
                continue
            if source.get("type") == "webshare_url":
                url = source.get("url")
                if not url:
                    continue
                try:
                    print("[*] Fetching proxy list from a Webshare download link...")
                    request = urllib.request.Request(
                        url, headers={"User-Agent": "codex-state-manager/2.0"}
                    )
                    with urllib.request.urlopen(request, timeout=20) as response:
                        text = response.read().decode("utf-8", errors="replace")
                    if text.lstrip()[:1] in "{[":
                        print(f"[!] Webshare returned an error payload, not a list: {text[:200]}")
                        continue
                    for line in text.splitlines():
                        add(line)
                except Exception as exc:  # noqa: BLE001
                    print(f"[!] Could not fetch the Webshare list: {exc}")
            elif source.get("type") == "webshare_api":
                # Preferred over a download link: download tokens are rotated by
                # the dashboard and silently expire, an API key does not.
                api_key = (source.get("api_key") or "").strip()
                if not api_key:
                    print("[!] webshare_api source has no api_key; skipping.")
                    continue
                try:
                    print("[*] Fetching proxy list from the Webshare API...")
                    page, fetched = 1, 0
                    while page <= int(source.get("max_pages", 20)):
                        endpoint = (
                            "https://proxy.webshare.io/api/v2/proxy/list/"
                            f"?mode=direct&page={page}&page_size=100"
                        )
                        request = urllib.request.Request(
                            endpoint,
                            headers={
                                "Authorization": f"Token {api_key}",
                                "User-Agent": "codex-state-manager/2.0",
                            },
                        )
                        with urllib.request.urlopen(request, timeout=20) as response:
                            envelope = json.loads(response.read().decode("utf-8"))
                        results = envelope.get("results", [])
                        for item in results:
                            if not item.get("valid", True):
                                continue
                            add(
                                f"{item.get('proxy_address')}:{item.get('port')}:"
                                f"{item.get('username')}:{item.get('password')}"
                            )
                            fetched += 1
                        if not envelope.get("next"):
                            break
                        page += 1
                    print(f"[*] Webshare API returned {fetched} proxies.")
                except Exception as exc:  # noqa: BLE001
                    print(f"[!] Could not query the Webshare API: {exc}")
            elif source.get("type") == "file":
                path = Path(source.get("path", ""))
                if not path.is_absolute():
                    path = self.config_path.parent / path
                if path.exists():
                    print(f"[*] Reading proxies from {path}")
                    for line in path.read_text(encoding="utf-8").splitlines():
                        add(line)
                else:
                    print(f"[!] Proxy file not found: {path}")

        self.proxies = pool + self._dynamic_proxies
        print(f"[*] Proxy routing: {len(pool)} static entries first, then {len(self._dynamic_proxies)} dynamic gateways.")

    # -- credentials -------------------------------------------------------

    def _credentials(self, account: Dict[str, Any]) -> Dict[str, Any]:
        account_id = int(account["id"])
        cached = self._creds_cache.get(account_id)
        if cached and time.time() - cached[0] < 600:
            return cached[1]
        creds = self.host.fetch_account(account_id, account["name"])
        self._creds_cache[account_id] = (time.time(), creds)
        return creds

    # -- harvesting --------------------------------------------------------

    def _stats_attempt(self, account, model_cfg, source, result):
        state = result.get("state", "")
        status = result.get("http_status", 0)
        info = inspect_turn_state(state) if state and status == 200 else {}
        hit = bool(status == 200 and state and info.get("valid")
                   and not info.get("is_expired", True)
                   and info.get("remaining_minutes", 0) > self.account_advance_minutes(account)
                   and len(state) <= MAX_STATE_BYTES and is_valid_header_value(state)
                   and (not model_cfg.get("require_exact_len", True)
                        or len(state) == int(model_cfg.get("target_state_len", 292))))
        self.record_probe(f"{account['id']}:{model_cfg['name']}", {
            "at": int(time.time()), "ok": hit, "outcome": "target_hit" if hit else "miss",
            "state_len": len(state), "source": source, "http_status": status,
            "header_ms": int(result.get("header_ms", 0))})
        try:
            self.stats.record_attempt(int(account["id"]), model_cfg["name"], source,
                                      int(status), len(state), int(result.get("header_ms", 0)),
                                      target_hit=hit)
        except Exception:
            print("[!] Source statistics write failed; probe processing continues.")

    def _harvest_rotating(self, account, model_cfg):
        """One attempt per model: static list once, then rotating gateway."""
        self._harvest_retry_delay = self.failure_backoff_seconds
        model = model_cfg["name"]
        slot = f"{account['id']}:{model}"
        if not self.proxies:
            self._refresh_proxies()
        if not self.proxies:
            print(f"[!] [{model}] no usable dynamic gateway; check proxy configuration.")
            return None
        try:
            creds = self._credentials(account)
        except Exception:
            print(f"[!] [{model}] credentials unavailable; waiting for recovery.")
            return None
        if slot not in self._static_pending:
            pending = [p for p in self.proxies if p not in self._dynamic_proxies]
            if self.config.get("static_proxy_order") == "random":
                random.SystemRandom().shuffle(pending)
            self._static_pending[slot] = pending
        pending = self._static_pending[slot]
        if pending:
            proxy = pending.pop(0)
            source_kind = "static"
        elif self._dynamic_proxies:
            index = self._dynamic_indices.get(slot, 0)
            proxy = self._dynamic_proxies[index % len(self._dynamic_proxies)]
            self._dynamic_indices[slot] = index + 1
            source_kind = "dynamic"
        else:
            return None
        source_name = self._proxy_sources.get(proxy, "static")
        attempt = self._probe_attempts.get(slot, 0) + 1
        self._probe_attempts[slot] = attempt
        try:
            result = probe_turn_state(proxy, creds, model,
                                      max_time=int(self.config.get("request_timeout_seconds", 30)))
        except Exception:
            self._stats_attempt(account, model_cfg, source_name, {"http_status": 0, "state": "", "header_ms": 0})
            print(f"[!] [{model}] probe exception; waiting for recovery.")
            return None
        self._stats_attempt(account, model_cfg, source_name, result)
        status, state = result["http_status"], result["state"]
        diagnostic = result.get("diagnostic") or classify_response(status, {}, "", "unknown")
        print(f"[*] [account={account['id']}] [{model}] {source_kind} source={source_name} ({mask_proxy(proxy)}) attempt={attempt} HTTP={status} state_len={len(state)} header_ms={result.get('header_ms', 0)}", flush=True)
        if status == 0:
            # Transport failures may be exit-specific. A small delay prevents
            # immediate local failures from creating a CPU/network busy loop.
            time.sleep(1)
            self._harvest_retry_delay = 0
            self._record_diagnostic(slot, diagnostic, len(state))
            return None
        if status != 200:
            # HTTP error scheduling is separate from DB/auth/network policy.
            if status in (403, 429):
                overrides = self.config.get("http_error_backoff_seconds", {})
                self._harvest_retry_delay = max(1, int(overrides.get(
                    str(status), self.failure_backoff_seconds)))
            else:
                self._harvest_retry_delay = self.failure_backoff_seconds
            retry_after = (result.get("headers") or {}).get("retry-after") or result.get("retry_after", "")
            if retry_after:
                try:
                    try:
                        delay = float(retry_after)
                    except ValueError:
                        delay = parsedate_to_datetime(retry_after).timestamp() - time.time()
                    if math.isfinite(delay):
                        self._harvest_retry_delay = max(self._harvest_retry_delay, delay)
                except (ValueError, TypeError, OverflowError):
                    pass
            self._static_pending.pop(slot, None)
            self._probe_attempts[slot] = 0
            # Rate limit or HTTP failure applies to the entire account
            with self._retry_lock:
                for m in account.get("models", []):
                    m_slot = f"{account['id']}:{m.get('name')}"
                    self._retry_after[m_slot] = time.time() + self._harvest_retry_delay
            if diagnostic.get("retryable") and not retry_after:
                count = self._error_streaks.get(slot, 0) + 1
                self._error_streaks[slot] = count
                if count < 3:
                    self._harvest_retry_delay = 2 ** count
                    self._short_retries.add(slot)
                else:
                    self._short_retries.discard(slot)
            else:
                self._short_retries.discard(slot)
                self._error_streaks.pop(slot, None)
            if status == 401:
                self._creds_cache.pop(int(account["id"]), None)
            self._record_diagnostic(slot, diagnostic, len(state))
            print(f"[!] [account={account['id']}] [{model}] diagnostic="
                  + json.dumps(diagnostic, sort_keys=True)
                  + f" retry_seconds={self._harvest_retry_delay}", flush=True)
            return None
        self._error_streaks.pop(slot, None)
        self._short_retries.discard(slot)
        self._harvest_retry_delay = 0
        self._record_diagnostic(slot, diagnostic, len(state))
        target = int(model_cfg.get("target_state_len", 292))
        if not state or (bool(model_cfg.get("require_exact_len", True)) and len(state) != target):
            max_attempts = max(10, len([p for p in self.proxies if p not in self._dynamic_proxies]) + min(4, len(self._dynamic_proxies) * 2))
            if attempt >= max_attempts:
                self._probe_attempts[slot] = 0
                self._static_pending.pop(slot, None)
                delay = min(int(self.failure_backoff_seconds), 20)
                self._harvest_retry_delay = delay
                print(f"[*] Skipping [{account.get('name')}] [{model}]: backing off for {delay}s after {attempt} un-hit attempts.")
                with self._retry_lock:
                    for m in account.get("models", []):
                        m_slot = f"{account['id']}:{m.get('name')}"
                        self._retry_after[m_slot] = time.time() + delay
            return None
        info = inspect_turn_state(state)
        if (not info.get("valid") or info.get("is_expired", True)
                or info.get("remaining_minutes", 0) <= self.account_advance_minutes(account)
                or len(state) > MAX_STATE_BYTES or not is_valid_header_value(state)):
            print(f"[*] [{model}] rejected invalid or insufficient-lifetime state.")
            return None
        info["cookie"] = result.get("cookie", "")
        info["cookie_expires_at"] = result.get("cookie_expires_at", "")
        return state, info, proxy

    def harvest(
        self, account: Dict[str, Any], model_cfg: Dict[str, Any]
    ) -> Optional[Tuple[str, Dict[str, Any], str]]:
        if self._rotating:
            return self._harvest_rotating(account, model_cfg)
        self._harvest_retry_delay = self.failure_backoff_seconds
        model = model_cfg["name"]
        target_len = int(model_cfg.get("target_state_len", 292))
        require_exact = bool(model_cfg.get("require_exact_len", True))

        print(f"\n[*] Harvesting [{account.get('name')}] [{model}] (target_len={target_len})")
        if not self.proxies:
            self._refresh_proxies()
        if not self.proxies:
            # An empty pool is the most severe form of "no IP yielded a clean
            # state" — every proxy source failed — so it must alert, not just log.
            print("[!] Proxy pool is empty.")
            self._alert_exhausted(account, model, target_len, [])
            return None

        try:
            creds = self._credentials(account)
        except Exception as exc:  # noqa: BLE001
            print(f"[!] Could not load account credentials: {exc}")
            return None

        seen_lengths: List[int] = []
        timeout = int(self.config.get("request_timeout_seconds", 30))
        ordered = self._ordered_proxies()
        budget = max(1, self.max_probes_per_pass)
        if len(ordered) > budget:
            print(f"  [{model}] trying {budget} of {len(ordered)} addresses this pass.")
            ordered = ordered[:budget]

        try:
            for index, proxy in enumerate(ordered, 1):
                print(f"  [{model}] -> [{index}/{len(ordered)}] {mask_proxy(proxy)} ...", end=" ", flush=True)
                try:
                    result = probe_turn_state(proxy, creds, model, max_time=timeout)
                except Exception:
                    self._stats_attempt(account, model_cfg, "static", {"http_status": 0, "state": "", "header_ms": 0})
                    raise

                self._stats_attempt(account, model_cfg, "static", result)
                if result["http_status"] == 0 and not result["state"]:
                    print(f"FAILED ({result.get('error') or 'no response'})")
                    continue

                state_len = result["state_len"]
                self._note_proxy_use(proxy, state_len)
                print(f"HTTP {result['http_status']} state_len={state_len}", end=" ")

                if not result["state"]:
                    print("[no turn-state header]")
                    continue
                if result["http_status"] != 200:
                    print("[upstream rejected]")
                    continue

                seen_lengths.append(state_len)
                if require_exact and state_len != target_len:
                    print(f"[REJECT: want {target_len}]")
                    continue

                info = inspect_turn_state(result["state"])
                if (not info.get("valid") or info.get("is_expired", True)
                        or info.get("remaining_minutes", 0) <= self.account_advance_minutes(account)
                        or len(result["state"]) > MAX_STATE_BYTES
                        or not is_valid_header_value(result["state"])):
                    print("[REJECT: invalid or insufficient-lifetime state]")
                    continue

                info["cookie"] = result.get("cookie", "")
                info["cookie_expires_at"] = result.get("cookie_expires_at", "")
                print("[HIT]")
                return result["state"], info, proxy
        finally:
            self._save_proxy_usage()

        print(f"[!] Pool exhausted; no acceptable state for {model}.")
        self._alert_exhausted(account, model, target_len, seen_lengths)
        return None

    def _alert_exhausted(
        self,
        account: Dict[str, Any],
        model: str,
        target_len: int,
        seen_lengths: List[int],
    ) -> None:
        key = f"exhausted:{account.get('id')}:{model}"
        if not self.proxies:
            observed = "代理池为空：所有代理来源都没有返回可用 IP（检查 Webshare token / API key / proxies.txt）"
        elif seen_lengths:
            distribution = ", ".join(
                f"{length}×{seen_lengths.count(length)}"
                for length in sorted(set(seen_lengths))
            )
            observed = f"实际返回长度分布：{distribution}"
        else:
            observed = "没有任何代理成功拿到 turn-state 响应头"

        message = "\n".join([
            "【Sub2】Codex Turn-State 告警",
            "状态：未能获取未降智 state",
            f"账号：{account.get('name')} (id={account.get('id')})",
            f"模型：{model}",
            f"期望长度：{target_len}",
            f"代理池规模：{len(self.proxies)}",
            observed,
            f"时间：{datetime.now(timezone.utc).strftime('%Y-%m-%d %H:%M:%S UTC')}",
            "影响：该模型将回退到原生逻辑，可能降智。请补充干净出口 IP。",
        ])
        self.notifier.send(key, message)

    # -- reporting ---------------------------------------------------------

    def print_status_table(self) -> None:
        print("\n" + "=" * 100)
        header = (
            f"{'Account':<18} {'Model':<16} {'Len':<6} {'Remaining':<12} "
            f"{'Expires At (UTC)':<22} {'Status'}"
        )
        print(header)
        print("-" * 100)

        for account in self.accounts():
            name = account.get("name", str(account.get("id")))
            try:
                pinned = self.host.read_pinned_states(int(account["id"]))
            except Exception as exc:  # noqa: BLE001
                print(f"{name:<18} {'-':<16} {'-':<6} {'-':<12} {'-':<22} [DB ERROR: {exc}]")
                continue

            for model_cfg in account.get("models", []):
                model = model_cfg["name"]
                target_len = int(model_cfg.get("target_state_len", 292))
                entry = pinned.get(model.lower(), {}) or {}
                state = entry.get("state", "")

                if not state:
                    print(f"{name:<18} {model:<16} {'None':<6} {'-':<12} {'-':<22} [UNCONFIGURED]")
                    continue

                info = inspect_turn_state(state)
                if not info.get("valid"):
                    print(f"{name:<18} {model:<16} {len(state):<6} {'-':<12} {'-':<22} [INVALID]")
                    continue

                tag = "[OK]"
                if info["is_expired"]:
                    tag = "[EXPIRED]"
                elif info["remaining_minutes"] <= self.account_advance_minutes(account):
                    tag = "[EXPIRING SOON]"
                if info["length"] != target_len:
                    tag += " [DEGRADED!]"

                print(
                    f"{name:<18} {model:<16} {info['length']:<6} "
                    f"{str(info['remaining_minutes']) + ' min':<12} "
                    f"{info['expires_at']:<22} {tag}"
                )
        print("=" * 100 + "\n")

    # -- main loop ---------------------------------------------------------

    def run_check_and_refresh(self, force: bool = False,
                              only_account: Optional[int] = None,
                              only_model: Optional[str] = None) -> int:
        self._reload_settings()
        round_started = time.monotonic()
        self._continue_harvest = False
        updated, attempted = 0, 0
        accounts = self.accounts()
        live_slots = {f"{a['id']}:{m['name']}" for a in accounts for m in a.get("models", [])}
        self._renewal_started = {k: v for k, v in self._renewal_started.items() if k in live_slots}
        self._forced_pending.intersection_update(live_slots)
        self._short_retries.intersection_update(live_slots)
        self._retry_after = {k: v for k, v in self._retry_after.items() if k in live_slots}
        self._error_streaks = {k: v for k, v in self._error_streaks.items() if k in live_slots}
        self._static_pending = {k: v for k, v in self._static_pending.items() if k in live_slots}
        self._dynamic_indices = {k: v for k, v in self._dynamic_indices.items() if k in live_slots}
        for account in accounts:
            if only_account is not None and int(account['id']) != only_account:
                continue
            advance = self.account_advance_minutes(account)
            try:
                pinned = self.host.read_pinned_states(int(account["id"]))
            except Exception as exc:  # noqa: BLE001
                print(f"[!] Could not read pinned states for {account.get('name')}: {exc}")
                continue

            models_to_harvest = []
            cookie_refresh_models = []
            for model_cfg in account.get("models", []):
                model = model_cfg["name"]
                if only_model is not None and model != only_model:
                    continue
                target_len = int(model_cfg.get("target_state_len", 292))
                entry = pinned.get(model.lower(), {}) or {}
                state = entry.get("state", "")

                slot = f"{account['id']}:{model}"
                if force:
                    self._forced_pending.add(slot)

                ticket_needs_refresh = slot in self._forced_pending or not state
                if state and not ticket_needs_refresh:
                    info = inspect_turn_state(state)
                    ticket_needs_refresh = (
                        not info.get("valid")
                        or info["is_expired"]
                        or info["remaining_minutes"] <= advance
                        or (
                            bool(model_cfg.get("require_exact_len", True))
                            and info["length"] != target_len
                        )
                    )

                cookie_needs_refresh = False
                if not ticket_needs_refresh and state:
                    cookie_exp = entry.get("cookie_expires_at")
                    if cookie_exp:
                        try:
                            exp_dt = datetime.fromisoformat(str(cookie_exp).replace("Z", "+00:00"))
                            now_dt = datetime.now(timezone.utc)
                            if (exp_dt - now_dt).total_seconds() <= 120:
                                cookie_needs_refresh = True
                        except Exception:
                            cookie_needs_refresh = True

                needs_refresh = ticket_needs_refresh or cookie_needs_refresh
                if not needs_refresh:
                    self._renewal_started.pop(slot, None)
                    self._static_pending.pop(slot, None)
                    continue

                slot = f"{account['id']}:{model}"
                with self._retry_lock:
                    retry_at = self._retry_after.get(slot, 0.0)
                if time.time() < retry_at:
                    if slot in self._short_retries:
                        self._continue_harvest = True
                    last_log = self._logged_skips.get(slot, 0)
                    if time.time() - last_log > 60:
                        self._logged_skips[slot] = time.time()
                        print(
                            f"[*] Skipping [{account.get('name')}] [{model}]: backing off for "
                            f"{int(retry_at - time.time())}s after the last exhausted pass."
                        )
                    continue
                else:
                    self._logged_skips.pop(slot, None)

                if ticket_needs_refresh:
                    models_to_harvest.append(model_cfg)
                elif cookie_needs_refresh:
                    cookie_refresh_models.append(model_cfg)

            # Routing cookies are account-wide: if only cookies need refresh across
            # multiple models, harvest only ONE model to refresh the account routing cookie!
            # Round-robin across models so one stubborn model doesn't block cookie refresh.
            if not models_to_harvest and cookie_refresh_models:
                c_idx = getattr(self, "_cookie_model_idx", 0)
                models_to_harvest.append(cookie_refresh_models[c_idx % len(cookie_refresh_models)])
                self._cookie_model_idx = c_idx + 1

            if not models_to_harvest:
                continue

            def _process_one(model_cfg: Dict[str, Any]) -> bool:
                model = model_cfg["name"]
                slot = f"{account['id']}:{model}"
                self._renewal_started.setdefault(slot, time.monotonic())
                harvested = self.harvest(account, model_cfg)
                if not harvested:
                    with self._retry_lock:
                        if self._rotating and self._harvest_retry_delay == 0:
                            self._retry_after.pop(slot, None)
                            self._continue_harvest = True
                        else:
                            self._retry_after[slot] = time.time() + self._harvest_retry_delay
                            if slot in self._short_retries:
                                self._continue_harvest = True
                            elif self._harvest_retry_delay > 0:
                                for m in account.get("models", []):
                                    m_name = m.get("name") if isinstance(m, dict) else m
                                    self._retry_after[f"{account['id']}:{m_name}"] = time.time() + self._harvest_retry_delay
                    return False
                with self._retry_lock:
                    self._retry_after.pop(slot, None)

                new_state, info, proxy = harvested
                try:
                    try:
                        ok = self.host.write_pinned_state(
                            account_id=int(account["id"]),
                            model=model,
                            state=new_state,
                            expires_at_iso=info["expires_at_iso"],
                            state_len=info["length"],
                            cookie=info.get("cookie", ""),
                            cookie_expires_at_iso=info.get("cookie_expires_at", ""),
                        )
                    except TypeError:
                        ok = self.host.write_pinned_state(
                            int(account["id"]),
                            model,
                            new_state,
                            info["expires_at_iso"],
                            info["length"],
                        )
                except Exception as exc:  # noqa: BLE001
                    print(f"[!] Write failed for [{model}]; keeping model pending.")
                    with self._retry_lock:
                        for m in account.get("models", []):
                            m_name = m.get("name") if isinstance(m, dict) else m
                            self._retry_after[f"{account['id']}:{m_name}"] = time.time() + self.failure_backoff_seconds
                    return False

                if ok:
                    self._record_renewal_success(slot)
                    self.record_probe(slot, {"at": int(time.time()), "ok": True,
                        "outcome": "saved", "state_len": info["length"],
                        "source": self._proxy_sources.get(proxy, "static")})
                    try:
                        self.stats.record_persisted(int(account["id"]), model,
                                                    self._proxy_sources.get(proxy, "static"))
                    except Exception:
                        print("[!] Source statistics write failed; pin was saved.")
                    self._static_pending.pop(slot, None)
                    self._forced_pending.discard(slot)
                    self._probe_attempts.pop(slot, None)
                    print(
                        f"[+] Pinned {info['length']}-byte state for "
                        f"[{account.get('name')}] [{model}] via {'dynamic gateway' if proxy in self._dynamic_proxies else 'static proxy'} "
                        f"(expires {info['expires_at']})"
                    )
                    self.notifier.clear(f"exhausted:{account.get('id')}:{model}")
                    return True
                else:
                    print(f"[!] Write verification failed for [{account.get('name')}] [{model}]")
                    with self._retry_lock:
                        for m in account.get("models", []):
                            m_name = m.get("name") if isinstance(m, dict) else m
                            self._retry_after[f"{account['id']}:{m_name}"] = time.time() + self.failure_backoff_seconds
                    return False

            # Models of one account are probed strictly one at a time. Firing
            # three concurrent requests that carry the SAME OAuth token from
            # three different exit IPs is the textbook credential-sharing /
            # impossible-travel signature, aimed at an upstream we already know
            # flags repeat probers. Serialising costs ~12s per account per hour
            # and removes that pattern entirely.
            for m_cfg in models_to_harvest:
                slot = f"{account['id']}:{m_cfg['name']}"
                with self._retry_lock:
                    if self._retry_after.get(slot, 0) > time.time():
                        continue
                try:
                    attempted += 1
                    if _process_one(m_cfg):
                        updated += 1
                    elif not (self._rotating and self._harvest_retry_delay == 0):
                        break
                except Exception:  # noqa: BLE001
                    with self._retry_lock:
                        for m in account.get("models", []):
                            m_name = m.get("name") if isinstance(m, dict) else m
                            self._retry_after[f"{account['id']}:{m_name}"] = time.time() + self.failure_backoff_seconds
                    print(f"[!] Harvest error for [{m_cfg.get('name')}]; waiting for recovery.")
                    break

        if self._continue_harvest:
            # No per-pass rest. Only prevent a zero-latency response path from
            # busy-spinning: real network rounds normally already exceed this.
            floor = 0.1
            if not attempted and self._short_retries:
                waits = [self._retry_after.get(k, 0) - time.time() for k in self._short_retries]
                future = [w for w in waits if w > 0]
                if future:
                    floor = min(1.0, min(future))
            remaining = floor - (time.monotonic() - round_started)
            if remaining > 0:
                time.sleep(remaining)
        return updated

    def test_proxies(self) -> None:
        print(f"[*] Testing {len(self.proxies)} proxies against api64.ipify.org")
        ok = 0
        for index, proxy in enumerate(self.proxies, 1):
            try:
                escaped = proxy.replace("\\", "\\\\").replace('"', '\\"')
                response = subprocess.run(
                    ["curl", "--config", "-", "--silent", "--max-time", "8",
                     "https://api64.ipify.org"],
                    input=f'proxy = "{escaped}"\n', text=True,
                    capture_output=True, timeout=10, check=True,
                )
                egress = response.stdout.strip()
                print(f"  [{index}/{len(self.proxies)}] {mask_proxy(proxy)} -> {egress}")
                ok += 1
            except Exception as exc:  # noqa: BLE001
                print(f"  [{index}/{len(self.proxies)}] {mask_proxy(proxy)} -> FAILED ({type(exc).__name__})")
        print(f"[*] {ok}/{len(self.proxies)} proxies reachable.")


PANEL_HTML_FILE = Path(__file__).parent / "panel.html"
PANEL_CSRF_HEADER = "X-CTSM-Panel"

class PanelHandler(BaseHTTPRequestHandler):
    server_version = "codex-turn-state-panel"
    manager: "StateManager" = None  # type: ignore[assignment]

    # -- plumbing ----------------------------------------------------------

    def log_message(self, fmt: str, *args: Any) -> None:
        # One line per request on a 5s-polling panel would bury the harvest log
        # that is the actual reason to read this journal.
        return

    def _send(self, code: int, payload: Any, content_type: str = "application/json") -> None:
        if content_type == "application/json":
            body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        else:
            body = payload if isinstance(payload, bytes) else str(payload).encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.send_header("X-Content-Type-Options", "nosniff")
        self.send_header("Referrer-Policy", "no-referrer")
        self.send_header("X-Frame-Options", "DENY")
        self.end_headers()
        self.wfile.write(body)

    def _mutation_allowed(self) -> bool:
        if self.manager.config.get("panel", {}).get("read_only", False):
            return False
        if self.headers.get(PANEL_CSRF_HEADER) != "1":
            return False
        origin = self.headers.get("Origin")
        if not origin:
            # Same-origin fetch() from the panel page omits Origin on some
            # browsers; the custom header already proves it was not a simple
            # cross-site form post.
            return True
        try:
            origin_host = urllib.parse.urlparse(origin).netloc
        except ValueError:
            return False
        return bool(origin_host) and origin_host == (self.headers.get("Host") or "")

    def _body(self) -> Dict[str, Any]:
        length = int(self.headers.get("Content-Length") or 0)
        if length <= 0 or length > 64 * 1024:
            raise ValueError("invalid body size")
        try:
            parsed = json.loads(self.rfile.read(length).decode("utf-8"))
        except (ValueError, UnicodeDecodeError):
            return {}
        return parsed if isinstance(parsed, dict) else {}

    # -- routes ------------------------------------------------------------

    def do_GET(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler API
        route = urllib.parse.urlparse(self.path)
        path = route.path.rstrip("/") or "/"
        try:
            if path == "/":
                try:
                    html = PANEL_HTML_FILE.read_bytes()
                except OSError:
                    self._send(500, "panel.html is missing", "text/plain; charset=utf-8")
                    return
                self._send(200, html, "text/html; charset=utf-8")
            elif path == "/api/settings":
                self._send(200, self.manager.settings_snapshot())
            elif path == "/api/state":
                self._send(200, self.manager.snapshot())
            elif path == "/api/degraded":
                force = urllib.parse.parse_qs(route.query).get("force", ["0"])[0] == "1"
                rows, error = self.manager.degraded.fetch(force=force)
                self._send(200, {"degraded": rows, "error": error})
            elif path == "/api/stats":
                query = urllib.parse.parse_qs(route.query)
                self._send(200, self.manager.source_stats(
                    query.get("start_day", [None])[0], query.get("end_day", [None])[0]))
            elif path == "/api/history":
                self._send(200, self.manager.history_snapshot())
            elif path.startswith("/api/jobs/"):
                job = self.manager.job(path.rsplit("/", 1)[-1])
                self._send(200 if job else 404, job or {"error": "unknown job"})
            elif path == "/api/jobs":
                self._send(200, self.manager.jobs())
            elif re.match(r"^/api/accounts/\d+/models$", path):
                account_id = int(path.split("/")[3])
                models = self.manager.get_account_models(account_id)
                self._send(200, {"account_id": account_id, "models": models})
            else:
                self._send(404, {"error": "not found"})
        except (ValueError, TypeError):
            self._send(400, {"error": "invalid request"})
        except Exception as exc:  # noqa: BLE001
            self._send(500, {"error": "panel operation failed; inspect service diagnostics"})

    def do_POST(self) -> None:  # noqa: N802
        path = urllib.parse.urlparse(self.path).path.rstrip("/") or "/"
        if not self._mutation_allowed():
            self._send(403, {"error": "forbidden"})
            return
        try:
            body = self._body()
            for field in ("account_id", "target_state_len"):
                if field in body and (isinstance(body[field], bool) or not isinstance(body[field], int)):
                    raise ValueError(field + " must be an integer")
            if path == "/api/settings":
                self._send(200, self.manager.update_settings(body))
            elif path == "/api/probe":
                account_id = int(body.get("account_id") or 0)
                if account_id <= 0:
                    self._send(400, {"error": "account_id is required"})
                    return
                if not isinstance(body.get("force", True), bool):
                    raise ValueError("force must be boolean")
                model = body.get("model") or None
                job_id = self.manager.enqueue_manual(
                    account_id, model, bool(body.get("force", True))
                )
                self._send(202, {"job_id": job_id})
            elif path == "/api/accounts":
                entry = self.manager.upsert_account(
                    int(body.get("account_id") or 0),
                    str(body.get("name") or ""),
                    str(body.get("model") or ""),
                    int(body.get("target_state_len", 292)),
                )
                self._send(200, entry)
            else:
                self._send(404, {"error": "not found"})
        except ValueError as exc:
            self._send(400, {"error": str(exc)})
        except TypeError:
            self._send(400, {"error": "invalid request"})
        except Exception as exc:  # noqa: BLE001
            self._send(500, {"error": "panel operation failed; inspect service diagnostics"})

    def do_DELETE(self) -> None:  # noqa: N802
        path = urllib.parse.urlparse(self.path).path.rstrip("/")
        if not self._mutation_allowed():
            self._send(403, {"error": "forbidden"})
            return
        parts = [p for p in path.split("/") if p]
        # /api/accounts/<id>/<model>
        if len(parts) != 4 or parts[0] != "api" or parts[1] != "accounts":
            self._send(404, {"error": "not found"})
            return
        try:
            removed = self.manager.remove_model(
                int(parts[2]), urllib.parse.unquote(parts[3])
            )
            self._send(200 if removed else 404, {"removed": removed})
        except ValueError as exc:
            self._send(400, {"error": str(exc)})
        except TypeError:
            self._send(400, {"error": "invalid request"})
        except Exception as exc:  # noqa: BLE001
            self._send(500, {"error": "panel operation failed; inspect service diagnostics"})

def start_panel(manager: "StateManager") -> Optional[ThreadingHTTPServer]:
    cfg = manager.config.get("panel", {})
    if not cfg.get("enabled", False):
        return None
    # Local-only by default. A verified private bridge bind plus authenticated
    # reverse proxy must be configured explicitly before remote exposure.
    bind = cfg.get("bind", "127.0.0.1")
    if not cfg.get("read_only", False):
        try:
            loopback = ipaddress.ip_address(bind).is_loopback
        except ValueError:
            loopback = False
        if not loopback:
            raise ValueError("management panel must bind a literal loopback address; remote panels require read_only")
    port = int(cfg.get("port", 8787))

    handler = type("BoundPanelHandler", (PanelHandler,), {"manager": manager})
    httpd = ThreadingHTTPServer((bind, port), handler)
    httpd.daemon_threads = True
    thread = threading.Thread(target=httpd.serve_forever, name="panel", daemon=True)
    thread.start()
    print(f"[*] Panel listening on http://{bind}:{port}")
    return httpd

def main() -> int:
    parser = argparse.ArgumentParser(description="Codex Turn-State Proactive Manager")
    parser.add_argument("--config", default=str(Path(__file__).parent / "config.json"))
    parser.add_argument("--stats", action="store_true", help="print aggregate source statistics as JSON")
    parser.add_argument("--status", action="store_true", help="print the status table and exit")
    parser.add_argument("--once", action="store_true", help="refresh what is expiring, then exit")
    parser.add_argument("--force", action="store_true", help="refresh every model regardless of TTL")
    parser.add_argument("--daemon", action="store_true", help="poll continuously")
    parser.add_argument("--test-proxies", action="store_true", help="check proxy reachability")
    parser.add_argument("--alert-test", action="store_true", help="send a test Feishu alert")
    args = parser.parse_args()

    try:
        if args.stats:
            config = json.loads(Path(args.config).read_text())
            stats = ProbeStats(Path(config.get("state_dir", "/var/lib/codex-turn-state-manager")))
            print(json.dumps(stats.report(), ensure_ascii=False, indent=2))
            return 0
        manager = StateManager(Path(args.config))
    except Exception as exc:  # noqa: BLE001
        print(f"[!] Startup failed: {exc}")
        return 1

    if args.alert_test:
        delivered = manager.notifier.send(
            "selftest",
            "\n".join([
                "【Sub2】Codex Turn-State 告警",
                "状态：投递自检",
                f"时间：{datetime.now(timezone.utc).strftime('%Y-%m-%d %H:%M:%S UTC')}",
                "这是一条测试消息，用于确认告警通路可用。",
            ]),
            force=True,
        )
        return 0 if delivered else 1

    if args.test_proxies:
        manager.test_proxies()
        return 0

    if args.status:
        manager.print_status_table()
        return 0

    if args.once or args.force:
        manager.print_status_table()
        updated = manager.run_check_and_refresh(force=args.force)
        while manager._continue_harvest:
            updated += manager.run_check_and_refresh()
        manager.print_status_table()
        print(f"[*] One-shot pass complete; {updated} state(s) updated.")
        return 1 if manager._forced_pending else 0

    if args.daemon:
        interval = int(manager.config.get("poll_interval_seconds", 60))
        proxy_refresh = int(manager.config.get("proxy_refresh_seconds", 3600))
        print(f"[*] Daemon starting; polling every {interval}s.")
        start_panel(manager)
        next_proxy_refresh = time.time() + proxy_refresh
        while True:
            try:
                if time.time() >= next_proxy_refresh:
                    manager._refresh_proxies()
                    next_proxy_refresh = time.time() + proxy_refresh
                manager.drain_manual_queue()
                manager.run_check_and_refresh()
                manager.reconcile_manual_jobs()
            except Exception as exc:  # noqa: BLE001
                print(f"[!] Loop error: {exc}")
            sys.stdout.flush()
            if not manager._continue_harvest:
                # Wake for a scheduled retry rather than overshooting it by
                # another full idle polling interval.
                wait = interval
                now = time.time()
                deadlines = [v - now for v in manager._retry_after.values() if v > now]
                if deadlines:
                    wait = min(interval, max(15.0, min(deadlines)))
                manager._wake.wait(timeout=wait)
                manager._wake.clear()
            else:
                delay = float(manager.config.get("rotating_continue_delay_seconds", 2.0))
                manager._wake.wait(timeout=max(0.5, delay))
                manager._wake.clear()

    manager.print_status_table()
    print("Use --once, --daemon, --status, --test-proxies, or --alert-test.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
