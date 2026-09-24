#!/usr/bin/env python3
"""Run on the Tokyo data host as root, under its backup maintenance lock."""
import datetime
import argparse
import fcntl
import ipaddress
import os
from pathlib import Path
import re
import shlex
import shutil
import stat
import subprocess

CONF = Path("/etc/sub2api-db-firewall.conf")
HBA = Path("/opt/sub2api-migration/postgres_data/18/docker/pg_hba.conf")
BACKUP_LOCK = Path("/run/sub2api-db-backup/pipeline.lock")
BACKUP_ROOT = Path("/opt/sub2api-migration")
AZURE_SOURCE = "4.216.216.16/32"
PUBLIC_SOURCES_RE = re.compile(
    r'^(?P<prefix>PRODUCTION_PUBLIC_SOURCES=\()(?P<body>.*)(?P<suffix>\))$',
    re.MULTILINE,
)


def cmd(*args):
    return subprocess.check_output(args, text=True).strip()


def write_existing(path, data):
    # Preserve the host inode, owner, and mode for mounted configuration files.
    with path.open("r+") as stream:
        stream.seek(0)
        stream.write(data)
        stream.truncate()
        stream.flush()
        os.fsync(stream.fileno())


def update_firewall_config(data, source_cidr):
    matches = list(PUBLIC_SOURCES_RE.finditer(data))
    assert len(matches) == 1, "expected one canonical PRODUCTION_PUBLIC_SOURCES array"
    match = matches[0]
    sources = shlex.split(match.group("body"))
    assert AZURE_SOURCE in sources, "Azure rollback source must remain allowed"
    if source_cidr not in sources:
        sources.append(source_cidr)
    rendered = " ".join(f'"{value}"' for value in sources)
    replacement = match.group("prefix") + rendered + match.group("suffix")
    return data[: match.start()] + replacement + data[match.end() :]


def update_hba(data, source_cidr):
    lines = data.splitlines()
    azure_lines = [
        line
        for line in lines
        if line.strip().startswith("hostssl") and AZURE_SOURCE in line.split()
    ]
    assert azure_lines, "Azure rollback source must remain in pg_hba.conf"
    additions = []
    for line in azure_lines:
        candidate = line.replace(AZURE_SOURCE, source_cidr)
        if candidate not in lines and candidate not in additions:
            additions.append(candidate)
    if not additions:
        return data
    return data.rstrip() + "\n" + "\n".join(additions) + "\n"


def ufw_rule_present(source, port):
    status = cmd("ufw", "status")
    pattern = re.compile(
        rf"^{re.escape(port)}/tcp\s+on\s+eth0\s+ALLOW IN\s+"
        rf"{re.escape(source)}(?:\s+#.*)?$"
    )
    return any(pattern.match(line.strip()) for line in status.splitlines())


def validate_backup_lock(path):
    assert path.is_file() and not path.is_symlink()
    lock_stat = path.stat()
    parent_stat = path.parent.stat()
    assert lock_stat.st_uid == 0 and lock_stat.st_gid == 0
    assert stat.S_IMODE(lock_stat.st_mode) == 0o600
    assert parent_stat.st_uid == 0 and parent_stat.st_gid == 0
    assert stat.S_IMODE(parent_stat.st_mode) == 0o700


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--source-ip", default="54.248.123.174")
    args = parser.parse_args()
    source = str(ipaddress.ip_address(args.source_ip))
    source_cidr = source + "/32"
    assert os.geteuid() == 0
    validate_backup_lock(BACKUP_LOCK)
    with BACKUP_LOCK.open("r+") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        for path in (CONF, HBA):
            assert path.is_file() and not path.is_symlink()
        original_conf = CONF.read_text()
        original_hba = HBA.read_text()
        new_conf = update_firewall_config(original_conf, source_cidr)
        new_hba = update_hba(original_hba, source_cidr)
        backup = BACKUP_ROOT / (
            "aws-allowlist-"
            + datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
        )
        backup.mkdir(mode=0o700)
        shutil.copy2(CONF, backup / "firewall.conf")
        shutil.copy2(HBA, backup / "pg_hba.conf")
        try:
            write_existing(CONF, new_conf)
            write_existing(HBA, new_hba)
            invalid = cmd("docker", "exec", "sub2api-migration-postgres", "psql", "-U", "sub2api", "-d", "sub2api", "-Atqc", "select count(*) from pg_hba_file_rules where error is not null")
            assert invalid == "0"
            cmd("docker", "exec", "sub2api-migration-postgres", "psql", "-U", "sub2api", "-d", "sub2api", "-Atqc", "select pg_reload_conf()")
            cmd("/usr/local/sbin/sub2api-db-docker-firewall")
            added_ufw_ports = []
            for port in ("5432", "6379"):
                if not ufw_rule_present(source, port):
                    cmd("ufw", "allow", "in", "on", "eth0", "proto", "tcp", "from", source, "to", "any", "port", port, "comment", "Sub2API AWS candidate TLS")
                    added_ufw_ports.append(port)
        except Exception:
            for port in reversed(locals().get("added_ufw_ports", [])):
                subprocess.run(
                    ("ufw", "--force", "delete", "allow", "in", "on", "eth0", "proto", "tcp", "from", source, "to", "any", "port", port),
                    check=False,
                    stdout=subprocess.DEVNULL,
                    stderr=subprocess.DEVNULL,
                )
            write_existing(CONF, original_conf)
            write_existing(HBA, original_hba)
            cmd("docker", "exec", "sub2api-migration-postgres", "psql", "-U", "sub2api", "-d", "sub2api", "-Atqc", "select pg_reload_conf()")
            cmd("/usr/local/sbin/sub2api-db-docker-firewall")
            raise
        print("aws_database_source_allowed=true backup=" + str(backup))


if __name__ == "__main__":
    main()
