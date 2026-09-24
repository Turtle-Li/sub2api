#!/usr/bin/env python3
"""Inject the approved Feishu webhook into the AWS memory-only agent."""

import os
import re
import subprocess
import sys
from urllib.parse import urlsplit

SECRET_ENV = "SUB2API_FEISHU_WEBHOOK_URL"
REFERENCE = "vault://secret/data/ops/feishu/payment#webhook_url"
HOST = "sub2api-aws-candidate"
ACK = b"SUB2API_VAULT_AGENT_LOADED"


def valid_webhook(value):
    try:
        parsed = urlsplit(value)
    except ValueError:
        return False
    return (
        value == value.strip()
        and parsed.scheme == "https"
        and parsed.netloc == "open.feishu.cn"
        and not parsed.query
        and not parsed.fragment
        and re.fullmatch(
            r"/open-apis/bot/v2/hook/[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}",
            parsed.path,
        )
        is not None
    )


def command():
    remote = (
        "sudo -n docker exec -i sub2api-feishu-vault "
        "/app/sub2api-vault-agent load "
        "--admin-socket /run/sub2api-feishu-vault-admin/admin.sock "
        "--ref "
        + REFERENCE
    )
    return [
        "/usr/bin/ssh",
        "-o",
        "BatchMode=yes",
        "-o",
        "ConnectTimeout=15",
        HOST,
        remote,
    ]


def activate(environment, runner=subprocess.run):
    value = environment.pop(SECRET_ENV, "")
    if not valid_webhook(value):
        return False
    payload = value.encode("utf-8")
    value = ""
    try:
        result = runner(
            command(),
            input=payload,
            env=dict(environment),
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=45,
            check=False,
        )
        return result.returncode == 0 and ACK in result.stdout.splitlines()
    except (OSError, subprocess.SubprocessError, ValueError):
        return False
    finally:
        payload = b""


def main():
    ok = activate(os.environ)
    print("SUB2API_AWS_FEISHU_BOT_INJECTED" if ok else "SUB2API_AWS_FEISHU_BOT_INJECTION_FAILED")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
