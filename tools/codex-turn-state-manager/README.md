# Integrated Codex renewal panel

This package reuses the existing, deployed manager from the sibling project's
`runtime-tools/codex-turn-state-manager/releases/20260918-panel`. The original
`manager.py`, `probe_stats.py`, `probe_diagnostics.py` and `panel.html` are copied
byte-for-byte; `runtime-sha256.json` pins their source. Their probe, scheduling,
Retry-After, source statistics, account/model overlay and verified JSONB writer
are not reimplemented.

`integrated.py` adds proxy-source import and shared temporary pool allocation
around the existing probe/rotation logic. `legacy_host_adapter.py` adapts only
account-name SQL lookup to support the project's Unicode account names and
reuses the project's native fingerprint seed for installation identity, and
adds the project's existing scheduler event to the atomic pin-write transaction. The Vue admin page embeds a derived copy
of the existing HTML with a sandboxed message bridge, and the backend forwards
allowlisted API calls through its existing administrator authentication. The
frame receives no administrator token and cannot directly access the parent.

## Per-project setup

1. Copy `config.json.example` to a protected `config.json` (0600). Set this
   project's backend container name and a dedicated state directory. Do not use
   another project's container, credentials, state directory or database.
2. The existing manager uses `docker exec ... psql` with the backend container's
   own `DATABASE_*` variables. Confirm that container has `psql` installed and
   the operator can access Docker. Python 3 and curl with HTTP/2 are required.
   Set the project's Codex version in its existing admin settings; the original
   manager validates it rather than guessing.
3. Run `python3 integrated.py --config /absolute/protected/config.json --daemon`.
   Keep a **single manager process** for each state directory/project, just as
   the original runtime. Do not start one in every gateway replica.
4. Run the Sub2API backend with `CODEX_TURN_STATE_PANEL_URL` pointing to that
   private manager, default `http://127.0.0.1:8787`. A container's loopback is not
   the host's loopback: explicitly configure a dedicated private bridge address
   if necessary. Do not expose the unauthenticated Python listener publicly.
5. Open **Codex 续期监控** in the administrator sidebar. Select an existing account by name using the application account picker
   and enter its model. IDs are internal only. Import static/rotating/extraction proxy
   sources there. Imported credentials are stored only in the daemon's 0600
   runtime overlay and are never echoed by the listing API.

Account/model scheduling, pause semantics (existing pins remain until expiry),
50-record per-model history, jobs, static-first/dynamic continuation, counters
and cooldown all retain the original behavior. State length is an observation,
not proof of model intelligence. Native upstream WS pins retain the old helper's
handshake semantics; an existing upstream WS connection requires reconnection
for a refreshed state to take effect. HTTP paths apply pins per request.

The native project has no old private HealthService contract. The old degraded
SQL/model normalization is reused behind the new project's existing admin auth;
the embedded page reads that authenticated route rather than copying the old
server's monitoring token or deployment system.

## Validation

```
python3 -m unittest discover -s . -p 'test_*.py' -q
```

Tests and `preview_panel.py` use mock-only data. Never copy a real configuration,
proxy list, account token, database, or state file into this repository. Secret
records belong in Vault; runtime files are protected injection copies.

Source baseline: ZvonimirSun/sub2api `881f3202694c6bc932446931a30c27d9675178b9`.
Legacy Go helper source: `b4baa9a66f36c56532423225447aec9a895272b4` / helper last
change `01ad6a3cb34aab300b3b2e94d9da8f864d0a69d3`. Preserve repository LGPL-3.0
license and applicable distribution/source obligations.

## Shared probe IP allocation

Each maintenance round has one shared temporary static pool across all accounts
and models. A random unused host is allocated once per round; different ports or
credentials on the same hostname/literal IP do not create extra slots. The pool
survives continuation passes. A newly imported unused host can join the current
round. Only after the static pool is exhausted are rotating gateways used or
extraction APIs called. Extraction requests are bounded and rate-limited; errors
are redacted. A completed round releases the temporary pool for the next round.

The inherited worker runs probes sequentially. A state-directory process lock
also excludes concurrent `--daemon`, `--once` and `--force` runs. Use one dedicated
state directory per project: independent hosts/directories do not share this
lock. Host identity is not a measurement of a rotating provider's real exit IP;
sequential execution prevents simultaneous account probes, but cannot promise
that different provider gateways always return different public exits.

Startup requires `alerts.enabled: false` and `degraded.enabled: false` explicitly.
This prevents inherited Feishu/Vault and old internal-monitor defaults from
running. Use the adapter entry point only, never launch the frozen `manager.py`
directly. The imported source overlay is 0600; use a private state directory and
inject source credentials from the private credential registry/Vault at runtime.
