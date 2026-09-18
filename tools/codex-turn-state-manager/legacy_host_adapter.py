"""Name/SQL compatibility for the host project's existing account picker.

fetch_account is copied from pinned manager.py; only name validation, the
base64 SQL literal, OpenAI OAuth eligibility and native fingerprint identity
resolution differ. Token/version validation stays unchanged. The copied atomic JSONB writer additionally emits
the target project's existing scheduler outbox event in the same transaction.
"""
import base64
import hashlib
import uuid
import json
import re
from datetime import datetime, timezone
from typing import Any, Dict

import manager
from manager import (MIN_CODEX_CLIENT_VERSION, _version_tuple, MODEL_NAME_RE,
                     MAX_STATE_BYTES, is_valid_header_value, BASE64_RE)

ACCOUNT_NAME_RE = re.compile(r"[^\x00-\x1f\x7f-\x9f]{1,128}\Z")


class IntegratedSub2APIHost(manager.Sub2APIHost):
    def fetch_account(self, account_id: int, account_name: str) -> Dict[str, Any]:
        """Load the OAuth credentials and codex client version for one account."""
        if not ACCOUNT_NAME_RE.fullmatch(account_name):
            raise ValueError(f"unsafe account name: {account_name!r}")
        encoded_name = base64.b64encode(account_name.encode("utf-8")).decode("ascii")
        sql = f"""
SELECT jsonb_build_object(
  'token', a.credentials->>'access_token',
  'account', a.credentials->>'chatgpt_account_id',
  'device', a.extra->>'openai_device_id',
  'fingerprint_mode', a.extra->>'codex_fingerprint_mode',
  'fingerprint_seed', a.extra->>'codex_fingerprint_seed',
  'version', COALESCE(
    NULLIF((SELECT value FROM settings WHERE key='openai_codex_client_version'),''),
    NULLIF((SELECT value FROM settings WHERE key='openai_codex_client_version_synced'),''))
)
FROM accounts a
WHERE a.id = {int(account_id)}
  AND a.name = convert_from(decode('{encoded_name}', 'base64'), 'UTF8')
  AND a.platform = 'openai'
  AND a.type IN ('oauth', 'setup-token')
  AND a.status = 'active'
  AND a.deleted_at IS NULL;
"""
        rows = self._rows(self.run_sql(sql))
        if len(rows) != 1:
            raise RuntimeError(f"account {account_id}/{account_name} not found or not active")
        data = json.loads(rows[0])
        # Match the target gateway's resolveConvergedInstallationID contract
        # (openai_codex_fingerprint.go), without mutating its managed identity.
        mode = data.pop("fingerprint_mode", None)
        seed = data.pop("fingerprint_seed", None)
        device = data.get("device")
        data["device"] = device.strip() if isinstance(device, str) else ""
        if not data["device"] and isinstance(mode, str) and mode.strip() in ("device", "session", "full"):
            try:
                canonical = seed.strip() if isinstance(seed, str) else ""
                parsed = uuid.UUID(canonical)
                if not parsed.int or canonical != str(parsed):
                    raise ValueError("noncanonical identity seed")
                digest = bytearray(hashlib.sha256(
                    ("sub2api:codex-install-id:v2:" + canonical).encode()
                ).digest()[:16])
                digest[6] = (digest[6] & 0x0f) | 0x40
                digest[8] = (digest[8] & 0x3f) | 0x80
                data["device"] = str(uuid.UUID(bytes=bytes(digest)))
            except ValueError:
                pass  # Fail closed through the existing device validation below.
        for field in ("token", "account", "device", "version"):
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


    def write_pinned_state(
        self,
        account_id: int,
        model: str,
        state: str,
        expires_at_iso: str,
        state_len: int,
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

        entry = {
            model.lower(): {
                "state": state,
                "state_len": state_len,
                "expires_at": expires_at_iso,
                "updated_at": datetime.now(timezone.utc).replace(microsecond=0).isoformat(),
            }
        }
        encoded = base64.b64encode(
            json.dumps(entry, ensure_ascii=True).encode("utf-8")
        ).decode("ascii")
        if not BASE64_RE.match(encoded):
            raise RuntimeError("encoded payload failed its own alphabet check")

        sql = f"""
WITH updated AS (
UPDATE accounts
SET extra = COALESCE(extra, '{{}}'::jsonb)
            || jsonb_build_object(
                 'pinned_codex_turn_states',
                 COALESCE(extra->'pinned_codex_turn_states', '{{}}'::jsonb)
                 || convert_from(decode('{encoded}', 'base64'), 'UTF8')::jsonb
               ),
    updated_at = NOW()
WHERE id = {int(account_id)} AND deleted_at IS NULL
RETURNING id
)
INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
SELECT 'account_changed', id, NULL, NULL FROM updated;
"""
        self.run_sql(sql, read_only=False)
        written = self.read_pinned_states(account_id).get(model.lower(), {})
        return written.get("state") == state


def install() -> None:
    manager.ACCOUNT_NAME_RE = ACCOUNT_NAME_RE
    manager.Sub2APIHost = IntegratedSub2APIHost
