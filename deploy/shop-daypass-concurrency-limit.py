#!/usr/bin/env python3
"""Change only the day-pass sales limit while caller holds the canonical lock."""
import argparse
import copy
import json
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import tempfile
import time

import yaml

CONFIG = Path('/var/lib/docker/volumes/sub2api-candidate-data/_data/config.yaml')


def update_text(text, expected, limit):
    before = yaml.safe_load(text)
    assert isinstance(before, dict)
    section = before.get('shop_daypass', {})
    assert isinstance(section, dict) and section.get('max_concurrency', 0) == expected, 'Sales limit changed concurrently'
    if 'shop_daypass' not in before:
        assert expected == 0 and limit == 2, 'Must stage before activation'
        updated = text.rstrip() + '\n\nshop_daypass:\n  max_concurrency: 2\n'
    else:
        matches = list(re.finditer(r'(?m)^shop_daypass:[ \t]*$', text))
        assert len(matches) == 1, 'Unsupported day-pass section format'
        start = matches[0].end()
        end = re.search(r'(?m)^[^\s#][^\n]*:', text[start:])
        stop = start + end.start() if end else len(text)
        body, count = re.subn(r'(?m)^  max_concurrency: [23][ \t]*$', '  max_concurrency: ' + str(limit), text[start:stop])
        assert count == 1, 'Expected one explicit sales limit'
        updated = text[:start] + body + text[stop:]
    wanted = copy.deepcopy(before)
    wanted.setdefault('shop_daypass', {})['max_concurrency'] = limit
    assert yaml.safe_load(updated) == wanted, 'Unrelated configuration changed'
    return updated


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--expected-limit', required=True, type=int, choices=[0, 2, 3])
    parser.add_argument('--limit', required=True, type=int, choices=[2, 3])
    parser.add_argument('--expected-revision', required=True)
    args = parser.parse_args()
    assert __debug__ and os.geteuid() == 0
    assert re.fullmatch('[a-f0-9]{40}', args.expected_revision)
    # Validate the caller actually inherited the canonical, exclusively held FD.
    lock = Path('/run/sub2api-maintenance/sub2api-maintenance.lock')
    held = os.fstat(8)
    assert (held.st_dev, held.st_ino) == (lock.stat().st_dev, lock.stat().st_ino)
    assert stat.S_IMODE(held.st_mode) == 0o600 and held.st_uid == 0
    raw = subprocess.check_output(['docker', 'ps', '-q'], text=True).split()
    running = json.loads(subprocess.check_output(['docker', 'inspect', *raw], text=True))
    apps = [v for v in running if v['Name'].lstrip('/') in ('sub2api', 'sub2api-blue', 'sub2api-green')]
    assert apps
    caddy = Path('/opt/sub2api/Caddyfile').read_text()
    targets = set(re.findall(r'(sub2api-(?:blue|green)):8080', caddy))
    assert len(targets) == 1
    selected = [v for v in apps if v['Name'].lstrip('/') in targets]
    assert len(selected) == 1 and selected[0]['State'].get('Health', {}).get('Status') == 'healthy'
    assert selected[0]['Config']['Labels'].get('org.opencontainers.image.revision') == args.expected_revision
    if args.limit == 3:
        assert args.expected_limit == 2
        assert all(v['Config']['Labels'].get('org.opencontainers.image.revision') == args.expected_revision for v in apps), 'An unaware app process is still running'
    metadata = CONFIG.lstat()
    assert stat.S_ISREG(metadata.st_mode) and metadata.st_nlink == 1 and (metadata.st_uid, metadata.st_gid) == (1000, 1000)
    assert stat.S_IMODE(metadata.st_mode) == 0o600
    updated = update_text(CONFIG.read_text(), args.expected_limit, args.limit)
    backup = Path('/opt/sub2api/staging') / time.strftime('daypass-limit-%Y%m%dT%H%M%SZ', time.gmtime())
    backup.mkdir(mode=0o700)
    shutil.copy2(CONFIG, backup / 'config.yaml')
    descriptor, temporary = tempfile.mkstemp(prefix='.daypass-limit-', dir=CONFIG.parent)
    try:
        os.fchmod(descriptor, 0o600)
        os.fchown(descriptor, metadata.st_uid, metadata.st_gid)
        with os.fdopen(descriptor, 'w') as output:
            output.write(updated)
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, CONFIG)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)
    print('SALES_LIMIT_STAGED', args.limit, 'running_apps', len(apps), 'backup', backup)
    print('Static configuration takes effect only on the next canonical application release.')


if __name__ == '__main__':
    main()
