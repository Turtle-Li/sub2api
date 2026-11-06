#!/usr/bin/env python3
"""Stage the DeepSeek weekly sales switch under the Sub2 maintenance lock."""

import argparse
import copy
import fcntl
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import tempfile
from typing import Final

import yaml

CONFIG: Final = Path("/var/lib/docker/volumes/sub2api-candidate-data/_data/config.yaml")
LOCK: Final = Path("/run/sub2api-maintenance/sub2api-maintenance.lock")
STAGING: Final = Path("/opt/sub2api/staging")


def update_text(text: str, current: bool, enabled: bool) -> str:
    """Replace one scalar, or add it to the existing day-pass section."""
    before = yaml.safe_load(text)
    assert isinstance(before, dict) and isinstance(before.get("shop_daypass"), dict)
    section = before["shop_daypass"]
    assert section.get("enabled", True) is True and section.get("product_id", 3) == 3
    assert section.get("weekly_enabled", False) is current, "Sales switch changed concurrently"
    sections = list(re.finditer(r"(?m)^shop_daypass:[ \t]*\n", text))
    assert len(sections) == 1, "Unsupported day-pass section format"
    start = sections[0].end()
    end = re.search(r"(?m)^[^\s#][^\n]*:", text[start:])
    stop = start + end.start() if end else len(text)
    setting = f"  weekly_enabled: {str(enabled).lower()}"
    if "weekly_enabled" in section:
        body, count = re.subn(
            r"(?m)^  weekly_enabled: (?:true|false)([ \t]*(?:#[^\n]*)?)$",
            lambda match: setting + match[1],
            text[start:stop],
        )
        assert count == 1, "Expected one canonical weekly sales scalar"
    else:
        body = setting + "\n" + text[start:stop]
    updated = text[:start] + body + text[stop:]
    wanted = copy.deepcopy(before)
    wanted["shop_daypass"]["weekly_enabled"] = enabled
    assert yaml.safe_load(updated) == wanted, "Unrelated configuration changed"
    return updated


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--expected", required=True, choices=["false", "true"])
    parser.add_argument("--enabled", required=True, choices=["false", "true"])
    parser.add_argument("--expected-revision", required=True)
    args = parser.parse_args()
    assert __debug__ and os.geteuid() == 0
    assert re.fullmatch(r"[a-f0-9]{40}", args.expected_revision)
    assert args.expected != args.enabled
    held, expected = os.fstat(8), LOCK.stat()
    assert (held.st_dev, held.st_ino) == (expected.st_dev, expected.st_ino)
    assert stat.S_IMODE(held.st_mode) == 0o600 and held.st_uid == 0
    fcntl.flock(8, fcntl.LOCK_EX | fcntl.LOCK_NB)

    caddy = subprocess.check_output(
        ["docker", "exec", "sub2api-candidate-caddy", "sh", "-c",
         'wget -Y off -qO- http://127.0.0.1:2019/config/ 2>/dev/null || '
         'curl --noproxy "*" -fsS http://127.0.0.1:2019/config/'],
        text=True,
    )
    targets = set(re.findall(r'"dial"\s*:\s*"(sub2api(?:-blue|-green)?):8080"', caddy))
    assert len(targets) == 1, "Cannot identify a unique live app"
    apps = subprocess.check_output(
        ["docker", "ps", "--filter", "name=^/sub2api(-blue|-green)?$", "--format", "{{.Names}}"],
        text=True,
    ).splitlines()
    assert len(targets.intersection(apps)) == 1
    sales_environment = subprocess.check_output(
        ["docker", "exec", next(iter(targets)), "sh", "-c",
         'printf "%s\n" "${SHOP_DAYPASS_ENABLED-}" "${SHOP_DAYPASS_PRODUCT_ID-}" "${SHOP_DAYPASS_WEEKLY_ENABLED-}"'],
        text=True,
    ).splitlines()
    assert sales_environment == ["true", "3", ""], "Day-pass environment no longer matches the sales switch"
    enabling = args.enabled == "true"
    for app in apps:
        if app in targets or enabling:
            result = subprocess.check_output(
                ["docker", "inspect", "--format",
                 '{{.State.Health.Status}} {{index .Config.Labels "org.opencontainers.image.revision"}}',
                 app], text=True,
            ).split()
            assert result == ["healthy", args.expected_revision], "An unaware app is still serving or draining"

    metadata = CONFIG.lstat()
    assert stat.S_ISREG(metadata.st_mode) and metadata.st_nlink == 1
    assert (metadata.st_uid, metadata.st_gid, stat.S_IMODE(metadata.st_mode)) == (1000, 1000, 0o600)
    updated = update_text(CONFIG.read_bytes().decode(), args.expected == "true", enabling)
    backup = Path(tempfile.mkdtemp(prefix="daypass-weekly-config-", dir=STAGING))
    shutil.copy2(CONFIG, backup / "config.yaml")
    fd, temporary = tempfile.mkstemp(prefix=".daypass-weekly-", dir=CONFIG.parent)
    try:
        with os.fdopen(fd, "wb") as output:
            os.fchown(output.fileno(), metadata.st_uid, metadata.st_gid)
            os.fchmod(output.fileno(), 0o600)
            output.write(updated.encode())
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, CONFIG)
    finally:
        Path(temporary).unlink(missing_ok=True)
    print("WEEKLY_SALES_STAGED", args.enabled, "running_apps", len(apps), "backup", backup)


if __name__ == "__main__":
    main()
