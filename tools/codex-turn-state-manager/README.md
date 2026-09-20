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

## Own-admin source management adapter

Run `integrated.py --config /protected/config.json --daemon` to add proxy-source
management. It preserves the own manager's active-container discovery, optional
device ID, per-account/model static-first policy, backoff, and atomic pin writer.
Do not install the other project's `legacy_host_adapter.py` or shared-static-round
policy. The original `manager.py` entry point remains available for rollback.

`GET /api/proxy-sources` lists safe metadata for baseline and managed sources.
Baseline configuration is read-only. Panel-created sources support import,
`POST /api/proxy-sources/<id>/enabled` with a boolean `enabled`, and deletion.
Changes are saved to the private `state_dir/proxy-sources.json` overlay and applied
by the worker; pending changes are distinguished from loaded/disabled sources.
Invalid overlay files produce an explicit read error while the worker retains
the last accepted pool. Source names cannot collide with baseline statistics.
Reads do not resolve DNS, call extraction providers or issue model requests.

Dynamic extraction is deferred until that account/model exhausts static entries,
with bounded HTTPS fetches, public-address validation, no redirects and no
forwarding of proxy credentials to the extraction API. A failed due refresh
drops prior extraction results. Gateway count does not measure exit IP count.

The admin app uses `CODEX_TURN_STATE_PANEL_URL` to reach the private HTTP service.
The default is `http://127.0.0.1:8787`, suitable only when the app and daemon share
the host network namespace. A container's localhost is not the host's localhost.
Keep Tailnet/non-loopback viewers read-only; management still requires loopback.
Do not expose an unauthenticated management listener to solve container access.
Install/transport changes require the project's release and maintenance process.
See `docs/features/own-codex-monitor-panel.md` for the reference and rollout limits.

## Authenticated same-host admin transport

For a Docker app that cannot reach the daemon loopback, `integrated.py` optionally
starts a second API-only listener from `admin_panel`: `enabled`, literal private
IPv4 `bind`, `port`, and absolute `token_file`. The existing `panel` listener
keeps its read-only setting. Every admin-listener request requires its bearer
token; mutations additionally require the existing CSRF header. No HTML page
or unprotected mutation API is exposed on that listener.

The own host reuses its protected internal-health token file, already mounted
read-only in the app. Go injects the token from `CODEX_TURN_STATE_PANEL_TOKEN_FILE`
and only permits token-bearing requests to a literal private/loopback target.
Set `CODEX_TURN_STATE_PANEL_URL` in the root-owned release environment so the
canonical blue-green helper carries both non-secret settings into new slots.
No token value enters environment variables, source, audit bodies or logs.
Token-file read failure is fail-closed; file rotation is picked up on requests.

Set `static_proxy_order=random` to shuffle the static queue once per account/model
renewal cycle. Each static endpoint is tried at most once before dynamic fallback;
continuation passes do not reshuffle/reset the queue. The absent-key behavior
retains the historical configured order. This does not bypass backoff or force
extra live probes.
