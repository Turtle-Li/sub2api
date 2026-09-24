#!/usr/bin/python3 -B
"""Bounded AWS Komari enrollment; invoke `coordinator`, never consumer directly.

Requests and credentials stay in process memory. The existing deploy helper's
protected temporary agent config is the sole staging exception. Each Vault
operation finishes (including re-lock) before the next operation starts. A
failed/uncertain create is never retried automatically: rerun the coordinator
to reconcile against the synchronized item index. Local runs on this file are
serialized; Vault has no cross-device atomic create-if-absent contract.
"""

import argparse
import contextlib
import fcntl
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import re
import resource
import stat
import subprocess
import sys

sys.dont_write_bytecode = True
SELF = Path(__file__).absolute()
MONITOR = Path.home() / "auto_work/projects/infra-monitoring"
HELPER = MONITOR / "scripts/manage-komari-client.py"
DEPLOY = MONITOR / "scripts/deploy-komari-agent.sh"
VAULT = Path.home() / ".local/bin/infra-vault"
HELPER_SHA = "8aa63f1d6706e0813ca5ad17abf002e2e5192fb99f5a915f2986371dd6cb6d71"
DEPLOY_SHA = "ba8f34bf5e8561ee7e482ec1c4fecb7e236869eae3fc9d79fb62f76f498bfabf"
ADMIN_ID = "ac448c67-e075-42d3-89c9-ed27e16eb233"
FOLDER_ID = "8bfa654b-cd70-42e3-9b68-021f04885ed9"
ITEM_NAME = "monitor-komari-agent-aws-sub2api-20260923"
CLIENT_NAME = "Sub2API AWS Tokyo"
ENDPOINT = "https://111.231.164.29:28443"
UUID_RE = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}", re.I)


class EnrollmentError(Exception):
    """Only fixed, non-secret status codes may be used as messages."""


def uuid_value(value):
    if not isinstance(value, str) or not UUID_RE.fullmatch(value):
        raise EnrollmentError("invalid_uuid")
    return value


def trusted_hash(path, expected=None):
    info = path.lstat()
    if (not stat.S_ISREG(info.st_mode) or info.st_mode & 0o022
            or info.st_uid not in (0, os.getuid())
            or not info.st_mode & 0o111):
        raise EnrollmentError("untrusted_executable")
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    if expected is not None and digest != expected:
        raise EnrollmentError("dependency_hash_changed")
    return digest


def vault_call(operation, request=None, parse=True):
    # No shell, request files, secret argv, or forwarded child diagnostics.
    result = subprocess.run(
        [str(VAULT), "unlock-run", "--", operation],
        input=json.dumps(request) if request is not None else "",
        capture_output=True, text=True, check=False,
    )
    if result.returncode != 0:
        raise EnrollmentError("vault_" + operation.replace("-", "_") + "_failed")
    if not parse:
        return None
    try:
        return json.loads(result.stdout)
    except (ValueError, TypeError):
        raise EnrollmentError("invalid_vault_response") from None


def injection(item_id, path, digest, mappings, args):
    return {
        "itemId": item_id,
        "env": [{"source": source, "name": name} for source, name in mappings],
        "command": {"path": str(path), "sha256": digest, "args": args},
        "cwd": str(SELF.parent),
        "timeoutSeconds": 600,
    }


def item_metadata(record):
    if (not isinstance(record, dict) or record.get("name") != ITEM_NAME
            or record.get("folderId") != FOLDER_ID or record.get("type") != 2):
        raise EnrollmentError("vault_item_metadata_mismatch")
    return {"id": uuid_value(record.get("id"))}


def existing_item():
    records = vault_call("list-item-index")
    if not isinstance(records, list) or any(not isinstance(r, dict) for r in records):
        raise EnrollmentError("invalid_vault_index")
    matches = [r for r in records if r.get("name") == ITEM_NAME]
    if len(matches) > 1:
        raise EnrollmentError("duplicate_vault_items")
    return item_metadata(matches[0]) if matches else None


def consumer():
    # The wrapper captures this pipe before the coordinator captures its output.
    # Refuse a terminal or redirected regular file before retrieving any token.
    if not stat.S_ISFIFO(os.fstat(sys.stdout.fileno()).st_mode):
        raise EnrollmentError("consumer_requires_capture_pipe")
    if not os.environ.get("KOMARI_ADMIN_USER") or not os.environ.get("KOMARI_ADMIN_PASSWORD"):
        raise EnrollmentError("consumer_requires_injection")
    trusted_hash(HELPER, HELPER_SHA)
    with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
        spec = importlib.util.spec_from_file_location("komari_enrollment_helper", HELPER)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        opener = module.authenticated_opener()
        try:
            client_uuid, token, created = module.get_or_create_client(opener, CLIENT_NAME)
        finally:
            os.environ.pop("KOMARI_ADMIN_USER", None)
            os.environ.pop("KOMARI_ADMIN_PASSWORD", None)
    uuid_value(client_uuid)
    if not isinstance(token, str) or not re.fullmatch(r"[A-Za-z0-9_-]{20,65536}", token):
        raise EnrollmentError("invalid_agent_token")
    print(json.dumps({"client_uuid": client_uuid, "agent_token": token}))


def create_item(self_hash):
    credential = vault_call("run-item-env", injection(
        ADMIN_ID, SELF, self_hash,
        [("login.username", "KOMARI_ADMIN_USER"),
         ("login.password", "KOMARI_ADMIN_PASSWORD")], ["consumer"],
    ))
    if not isinstance(credential, dict) or set(credential) != {"client_uuid", "agent_token"}:
        raise EnrollmentError("invalid_consumer_response")
    client_uuid = uuid_value(credential["client_uuid"])
    token = credential["agent_token"]
    if not isinstance(token, str) or not re.fullmatch(r"[A-Za-z0-9_-]{20,65536}", token):
        raise EnrollmentError("invalid_agent_token")
    request = {
        "type": "secure-note", "name": ITEM_NAME, "folderId": FOLDER_ID,
        "notes": "Independent AWS Sub2API Komari Agent. Outbound WSS only; web SSH and automatic updates disabled.",
        "fields": [
            {"name": "client_uuid", "value": client_uuid, "type": "text"},
            {"name": "agent_token", "value": token, "type": "hidden"},
            {"name": "endpoint", "value": ENDPOINT, "type": "text"},
            {"name": "client_name", "value": CLIENT_NAME, "type": "text"},
        ],
    }
    try:
        # Recheck after enrollment in case another operator saved the item.
        metadata = existing_item()
        if metadata is None:
            metadata = item_metadata(vault_call("create-item", request))
        confirmed = existing_item()
        if confirmed != metadata:
            raise EnrollmentError("vault_creation_not_confirmed")
        return metadata, client_uuid
    finally:
        credential.clear()
        request.clear()
        token = None


def coordinator(identity=None, target="ubuntu@54.248.123.174", port=22, interface="ens5"):
    self_hash = trusted_hash(SELF)
    trusted_hash(HELPER, HELPER_SHA)
    trusted_hash(DEPLOY, DEPLOY_SHA)
    trusted_hash(VAULT)
    if identity is not None:
        identity = Path(identity).expanduser().absolute()
        if not stat.S_ISREG(identity.lstat().st_mode):
            raise EnrollmentError("invalid_ssh_identity")
    # Lock the existing source inode without creating a lock/request file.
    with SELF.open("rb") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise EnrollmentError("enrollment_already_running") from None
        metadata = existing_item()
        client_uuid = None
        if metadata is None:
            metadata, client_uuid = create_item(self_hash)
        print(json.dumps({"item_id": metadata["id"], "status": "vault_confirmed"}), flush=True)
        # The creation frame is gone; retrieve the saved token only into deploy.
        args = [target, str(port), interface]
        if identity is not None:
            args.extend([str(identity), "amd64"])
        vault_call("run-item-env", injection(
            metadata["id"], DEPLOY, DEPLOY_SHA,
            [("field:agent_token", "KOMARI_AGENT_TOKEN")], args,
        ), parse=False)
        result = {"item_id": metadata["id"], "status": "deployment_completed"}
        if client_uuid is not None:
            result["client_uuid"] = client_uuid
        print(json.dumps(result))


def main():
    resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("coordinator", "consumer"))
    parser.add_argument("--ssh-identity", help="Existing device-local SSH key path (coordinator only)")
    parser.add_argument("--target", default="ubuntu@54.248.123.174")
    parser.add_argument("--port", type=int, default=22)
    parser.add_argument("--interface", default="ens5")
    args = parser.parse_args()
    try:
        if args.mode == "consumer":
            if args.ssh_identity:
                raise EnrollmentError("invalid_consumer_arguments")
            consumer()
        else:
            coordinator(args.ssh_identity, args.target, args.port, args.interface)
        return 0
    except EnrollmentError as error:
        print(json.dumps({"status": str(error)}), file=sys.stderr)
    except KeyboardInterrupt:
        print('{"status":"interrupted_reconcile_before_retry"}', file=sys.stderr)
    except Exception:
        # Exception strings/tracebacks may carry HTTP bodies or subprocess data.
        print('{"status":"enrollment_failed"}', file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
