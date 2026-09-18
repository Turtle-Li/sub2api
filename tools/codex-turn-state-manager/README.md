# Codex turn-state manager and private panel

This host-side Python tool refreshes per-account, per-model turn states before
expiry and verifies each database write. The private panel shows expiry,
backoff, probe history and durable source statistics. A target byte length is
an operational acceptance rule, not proof of model identity or output quality.

## Run and test

Python 3.10+, curl with HTTP/2, and the project's existing host/database helper
are required. Runtime configuration and proxy credentials remain outside Git.

```sh
python3 -m unittest discover -s tools/codex-turn-state-manager -p 'test_*.py'
python3 tools/codex-turn-state-manager/preview_panel.py --port 18787
python3 tools/codex-turn-state-manager/manager.py --config /protected/config.json --daemon
```

The preview uses synthetic accounts and probes. Open `/ctsm/` on its local port.
The daemon uses the existing account credential injection mechanism; it does
not require an admin API credential. Missing installation IDs are omitted, not
invented; existing IDs are retained. Token, account and client version remain
mandatory. Never run tests against production accounts or paste runtime files
into an issue or pull request.

## Configuration and operation

`accounts` contains account `id`, `name`, and `models` with `name`,
`target_state_len` and `require_exact_len`. The optional `state_dir/accounts.json`
overlay maps string account IDs to overrides; `enabled: false` pauses an account
while retaining its baseline configuration. Missing overlays use the baseline;
invalid overlays preserve the last validated configuration.

Use `panel.enabled`, `panel.bind`, `panel.port`, and `panel.read_only` to enable
the viewer on an explicitly approved private address. The panel has no login
system: never expose it directly to the Internet. In read-only mode all
POST/DELETE requests are rejected. Management mode requires a literal loopback bind; private network binds must be read-only. Default binding is loopback. Keep
`degraded.enabled=false` for renewal-only viewing; optional model mismatch
reporting needs the monitor-authenticated `/internal/degraded-accounts` API.
No mismatch observations does not establish that model quality has recovered.

The daemon owns probe execution. HTTP refreshes only read snapshots. Management
mode queues work and preserves backoff/Retry-After; forced work cannot bypass
those limits. Accounts retain sibling models when one model is edited.
Static proxies are attempted first, followed by configured rotating sources.
Source statistics are observational and cannot establish supplier superiority.
Daily statistics use UTC; the browser renders write timestamps in local time.

## Deployment and recovery

Package `manager.py`, `panel.html`, `probe_stats.py` and `probe_diagnostics.py`
together. Preserve protected configuration, account overlays, pins and the
state directory. Use the project's maintenance lock for replacement/restart,
then release it before HTTP smoke checks: node-state discovery also takes that
lock. Reacquire and compare code/config identities before rollback; never
blindly restore over concurrent maintenance. Keep a protected backup and use
the project's service manager. This repository merge is not an application or
probe deployment.

The first integrated release passed 62 mock tests, including optional device
identity, wire parsing, retry sequencing, sibling model preservation, queueing,
read-only mutation rejection and concurrent statistics readers. Application
endpoint tests cover token authorization, bounded windows, model-name variants,
drain exclusion and JSON routing. Production query performance remains an
operator validation item before enabling the optional mismatch feed.

Proxy cooldown state uses hashed endpoint identities, with automatic legacy key migration; the state directory is private (0700) and usage writes are atomic (0600).
