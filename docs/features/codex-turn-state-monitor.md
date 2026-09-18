# Codex renewal monitor integration

## Confirmed scope

The user explicitly requires reusing our existing stable Sub2API logic, not rewriting the scheduler/probe. This is a separate project based on ZvonimirSun/sub2api `881f3202694c6bc932446931a30c27d9675178b9`. Add the existing monitoring panel as an administrator sidebar feature, with account/model management and static plus rotating/extraction-API proxy import.

## Online integration baseline

The integration is packaged and locally verified against the already deployed customization baseline `4a043a77269daac0da75723b4b7ba9e16a83f51e` (`origin/custom`, tag `v0.2.5.1`), which is 12 commits after `881f3202694c6bc932446931a30c27d9675178b9`. The earlier revision remains the upstream source-compatibility reference; this worktree does not make `main` a merge target.

## Final boundaries

- Original Python manager, statistics, diagnostics and panel are copied unchanged into `tools/codex-turn-state-manager` with their source hash manifest and original tests.
- `integrated.py` extends proxy import and shared temporary pool allocation. Probe/harvest/refresh/Retry-After/account overlay/job/statistics behavior stays in the original tool.
- The Vue wrapper embeds an adapted copy of the existing panel in an opaque sandbox. Only a bounded allowlisted postMessage bridge calls the existing admin-authenticated API; no admin token enters the frame.
- The Go handler forwards allowed control/read requests to the explicitly configured private daemon. It does not own scheduling or probes.
- Existing Go pinned-state helper and model-degradation SQL/helpers are directly reused, with minimal constructor/authentication compatibility adaptation. Preserve OAuth-like, model alias and expiry behavior and the original pause semantics (existing pins expire naturally).
- No live probe, deployment or reuse of sibling production credentials is authorized by this implementation. Dedicated test SSH identity and pending target are kept in the private infrastructure registry and local ignored `docs/deploy/test-server.md`.

## Acceptance and validation

1. Authenticated administrator sidebar opens the old monitoring panel; unauthorized callers cannot access its bridge API.
2. Existing add/pause/manual-run/model/status/jobs/history/source statistics retain behavior; force cannot bypass backoff.
3. Static import accepts `host:port:username:password`; rotating fixed gateways and HTTPS extraction APIs feed the old pool with bounded requests and redacted output.
4. Reuse existing gateway helper on HTTP/compat/WS paths; no pin changes for unrelated accounts. Native WS pin refresh retains the old handshake/reconnect limitation.
5. Original 56 tests, adapter tests, Go helper/handler tests, frontend bridge tests/typecheck/build and visual inspection. No real credentials/upstream probes in validation.

Root owns integration; delegated work is confined to the new proxy-import adapter, migration of existing model audit code, and independent tests. The earlier native-Go rewrite was moved to a temporary draft outside the change and is not shipped. No database schema change. Rollback removes the sidebar/bridge and stops this project's manager; keep its state for recovery. Runtime setup is documented in `tools/codex-turn-state-manager/README.md`.

### Pinned legacy snapshots

- `releases/20260918-panel/manager.py`: SHA-256 `66821b1b2f21846d280fb3463128bbd6ccff5417e167b8025314780e5a3886bd`.
- `probe_stats.py`: `f84b553cbb7811b07b6336b7a4f8e284a49448f1cb2f439c94069615897678ed`; `probe_diagnostics.py`: `626849dde03e2edfda7a3b49458e391b487c6dfedc94f72733d90e4d23cede31`; `panel.html`: `f07db8fec452521573c3973195da6ff427ec9c52c7ac0f45036f20e12b0c0e11`.
- Legacy gateway helper is in sibling `sub2api-main-release-worktree/backend/internal/service/openai_codex_pinned_turn_state.go`, SHA-256 `8edbd104f1288c70ee9b18e76033b98ef4069999842f9d33a134eccc839a4136`, with matching `_test.go` behavior tests. It is absent from `sub2api-src`.
- Adopt account/model pin mapping, safe HTTP header handling, 8192-byte limit, 3600-second timestamp-derived lifetime, refresh-ahead, bounded sequential probes, backoff and verified pin write. Preserve legacy optional-expiry string pins and model-prefix/date/tag fallback; do not expose the old broad manual raw-state API.
- Legacy helper revision: `b4baa9a66f36c56532423225447aec9a895272b4` (last helper change `01ad6a3cb34aab300b3b2e94d9da8f864d0a69d3`); tests SHA-256 `2c4b989825ed82ef07b7553b137ace5b09e689e2a7d030880915e8884a17b86d`. Relevant Python tests: `test_probe_wire.py`, `test_accounts_retry.py`, `test_panel.py`, `test_rotating.py`; preserve bounded retries, no force bypass of Retry-After, exact-target acceptance and read-back verification.


## Target project compatibility adaptations

- `legacy_host_adapter.py` copies the pinned `Sub2APIHost.fetch_account` and
  `write_pinned_state` methods. It encodes the selected name as a base64 SQL literal, allows existing
  Unicode/space/quote names, and limits eligibility to active OpenAI OAuth-like
  accounts. Credential/device/version validation and the inherited atomic JSONB
  merge and read-back verification are unchanged. The copied writer emits an
  `account_changed` scheduler outbox event in the same transaction using the
  target repository contract (`036_scheduler_outbox.sql`, `account_repo.go`).
  This avoids waiting for the default 300-second full snapshot rebuild after a
  direct DB write. No new schema, account CRUD, health or deployment service.
- The admin panel handler uses the copied model mismatch SQL against this
  project's database. Snapshot latest-observation selection and pin-time
  verdicts mirror the original manager; query errors are `unknown`, never a
  false healthy result. No old internal-monitor token or network route is used.
- Existing gateway customization remains in place. The four gateway seams add
  a pin lookup after the existing caller/compatibility turn-state. A valid
  account/model pin intentionally takes precedence; absent or expired pins leave
  the existing headers unchanged. Identity, session isolation, model routing,
  account scheduling and response audit implementations are not replaced.

## IP allocation acceptance

- One global temporary static pool per maintenance round across account/model
  slots; randomized host selection without replacement, ignoring credentials and
  port for identity. Keep it through immediate continuation passes and incorporate
  newly imported unused hosts. Release when the original worker ends the round.
- Defer extraction API calls until static hosts are exhausted; then reuse the
  existing dynamic probe branch. Cover the one-static-plus-extract-only case so
  ending a pass cannot reset the pool forever before reaching extraction.
- Preserve serial probing and hold a per-state-directory process lock for daemon,
  once and force modes. Different deployment hosts/state directories remain an
  operator configuration boundary; dynamic real exit identity is provider-owned.
- Audit inventory and preservation evidence: `codex-turn-state-audit.md`.

## Local validation (2026-09-18)

Implementation ready for independent server smoke; not deployed or accepted.

- Four source-file SHA-256 checks match the pinned manifest.
- All 71 Python tests passed, including shared pool continuation, deferred
  extraction, source removal, startup guards, process lock and host adaptation.
- Frontend bridge/picker tests: 15 passed; source locale completeness: 3 passed;
  production build including TypeScript passed. Existing bundle-size warnings
  remain. Structured-clone checks cover Vue objects crossing the iframe boundary.
- Full admin handler, admin routes and middleware Go packages passed. Focused
  gateway Codex/identity/routing/model-audit regressions passed; the new three-path
  pin integration test passed with valid pins, expiry fallback and distinct accounts.
- Browser mock journey selected a Chinese account through the existing Select
  dialog and added its model successfully. Narrow viewport had no page-level
  horizontal overflow; status tables scroll independently. Temporary preview
  files/processes were removed after inspection.
- Isolated live deployment, database outbox writes, and actual upstream probes
  passed; see the live validation record below.

Knowledge candidate: project-specific integration contract, retained here and
in the audit document because future upstream updates must preserve the four
source hashes, pin precedence, host eligibility and scheduler notification seam.


## Claude Code review remediation (2026-09-18)

The independent first review returned CHANGES_REQUIRED for the new managed Extra
key: generic account DTOs exposed raw pins, while generic account updates could
remove them. Fix these together: redact the key from account DTOs, preserve it
on normal edits, and strip caller-supplied values from create/PUT/extra/bulk
inputs. The repository's existing row-locked managed-field merge preserves the
latest database pins against concurrent renewal and prevents resurrecting stale
pins. Retention is gated by platform, type and ChatGPT account identity; changing
the upstream account discards pins, while refreshing its token or changing proxy
preserves them. Account exports omit pins without modifying the source account.
This extends existing account safety mechanisms, not the probe core.

Regression tests reproduce the original loss/disclosure and cover missing/empty/
injected Extra, non-mutating DTO redaction and latest-row retention. Proxy import
audit coverage now executes the actual wildcard middleware route. The frontend
selector includes both OAuth and setup-token; fallback verdict labels are escaped.
Private deployment references and local worktree metadata are excluded from Git.

Known nonblocking scope: the embedded legacy panel remains Chinese; the admin
menu is visible before its private daemon is configured. Live HTTP smoke passed. Live Anthropic compatibility and native WebSocket
smoke remain unverified.

## Native Codex identity adaptation

Real-host inspection showed OAuth accounts with the target fork's managed
`codex_fingerprint_mode`/`codex_fingerprint_seed`, but no legacy `openai_device_id`.
`legacy_host_adapter.py` now follows `resolveConvergedInstallationID`,
`canonicalCodexFingerprintSeed` and `deriveStableUUIDv4` in
`backend/internal/service/openai_codex_fingerprint.go` at target revision
`4a043a77269daac0da75723b4b7ba9e16a83f51e` (same contract at upstream 881f320).
A configured device override wins; otherwise enabled device/session/full modes
derive the same UUID from SHA-256 of `sub2api:codex-install-id:v2:` plus canonical
non-nil seed. Off/invalid/missing identities fail closed. No seed/device write,
random replacement or automatic mode change occurs. The frozen probe core is
unchanged. Fixed-vector and boundary tests live in `test_legacy_host_adapter.py`.
Target reference repository: https://github.com/ZvonimirSun/sub2api.git; retain
the repository's LGPL-3.0 obligations.

## Live validation (2026-09-18)

Integrated against deployed revision `4a043a77269daac0da75723b4b7ba9e16a83f51e`,
without merging its existing custom features into main. The tested application
binary SHA-256 is `850511c9877564cc0651da497e1fce93a7bce2a9b95cccf3155f2899021bafb1`.

- 74 Python tests, 25 focused frontend tests, 3 locale tests, TypeScript and
  frontend production build passed. Affected Go packages and focused gateway
  identity/pin tests passed. The embedded web suite retains two pre-existing
  `/logo.png` failures (the deployed baseline provides `logo.svg`).
- Isolated HTTP smoke observed an astra request initially returning luna; after
  obtaining and persisting a valid 292-byte state, repeated requests returned
  astra. A second account's luna request also returned the requested model.
- A shared maintenance round used three distinct static proxy hosts for three
  attempts across two accounts, obtained two valid states, and observed no 429.
  Account-edit round trips preserved pins; account DTOs did not expose them.
- The exact tested image was promoted to the existing application service.
  Application and private monitor health passed; unrelated containers remained
  unchanged. Fresh production states were persisted with scheduler outbox events.
- Normal monitoring uses 60-second polling and a 15-minute advance. Accelerated
  renewal tests were explicitly not run. Actual scheduled renewal is therefore
  not claimed as validated by this smoke.
- Subsequent real traffic: four astra requests returned astra (first token
  1.82–3.50 s); one luna request returned luna (2.43 s). Seven terra requests
  returned terra but first token was 11.44–31.27 s; terra had no configured pin.
  Pins are account/model-specific, and 292 length alone is not a quality guarantee.
- Real dynamic-provider fallback, live WebSocket/Anthropic compatibility smoke
  and persisted proxy-import audit records remain unverified. Dynamic fallback
  and audit middleware have targeted automated coverage.

No customer credentials, raw states, proxy endpoints or private deployment
records are included in this repository. Only compiled runtime artifacts were
transferred to the host. Legacy notifications remain disabled by the adapter.

## Production panel recovery (2026-09-18)

The original panel worked without CSP in mock validation but stalled on the
production page: srcdoc inherits its parent's nonce-based script-src, so its
un-nonced inline script never ran. Isolated Chromium reproduced zero bridge
requests and an inline-script CSP violation. The Vue adapter now copies the
existing host script's `.nonce` property onto bundled panel scripts, retaining
the opaque sandbox and existing CSP. A startup watchdog displays a recoverable
error if scripts cannot initialize; API failures no longer leave initial tables
saying Loading. The frame reports readiness/content height and follows the
parent's light/dark theme without receiving authentication data.

The redundant nested titles, monospace body style and fixed-height inner scroll
were removed. Colors follow the existing teal/slate tokens; tables retain local
horizontal scrolling. Bounded Chromium tests under strict CSP cover status
rendering, API errors and desktop-dark/mobile-light layouts; nonce/watchdog/
message-source regressions extend the Vue tests.

Operational correction: both selected accounts monitor astra, sol and terra,
each with its own account/model pin. Existing luna monitoring remains separate.
Read-only live diagnostics confirmed normal scheduled renewal had continued;
CSP failure affected visibility, not the daemon. Do not equate a valid 292 pin
or matching response-model label with a guarantee of model quality.

If host settings injection fails and the fallback HTML has no nonce-bearing
script, the existing CSP still blocks initialization; the new watchdog exposes
a retryable error. This does not weaken CSP or bypass the settings failure.
