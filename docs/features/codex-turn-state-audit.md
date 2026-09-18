# Codex turn-state integration audit

Audit date: 2026-09-18
Feature source baseline: `881f3202694c6bc932446931a30c27d9675178b9`
Online integration baseline: `4a043a77269daac0da75723b4b7ba9e16a83f51e` (`origin/custom`, `v0.2.5.1`; 12 commits after the feature source baseline)
Frozen Python source: existing Sub2API tool release `20260918-panel`, identified by the SHA-256 manifest below

## Conclusion

The four copied Python artifacts are byte-for-byte copies of the frozen source.
They retain source deployment assumptions and defaults, including Docker/psql
access, optional Feishu notifications, the former degraded-account bridge,
Webshare import support, and a legacy 292-state metric. The target integration
contains those assumptions at the intended `integrated.py` entry point: it
requires a project-specific state directory and container, and requires both
legacy alerting and degraded-service access to be explicitly disabled.

The four gateway seams differ from the baseline by four additive pin-application
calls (five added lines including one blank line). No identity, account/session isolation, model routing, account scheduling,
or response-audit implementation was replaced. A valid account/model pin
intentionally has precedence over the caller or compatibility turn state; an
absent or expired pin leaves the established behavior unchanged.

`legacy_host_adapter.py` is a narrow compatibility layer. It supports the host
project's Unicode account names without interpolating the name into SQL, retains
the source credential validation and atomic JSONB merge, and emits the existing
`account_changed` scheduler event in the same transaction as a successful pin
write.

## Frozen source evidence

| Target file | SHA-256 |
| --- | --- |
| `tools/codex-turn-state-manager/manager.py` | `66821b1b2f21846d280fb3463128bbd6ccff5417e167b8025314780e5a3886bd` |
| `tools/codex-turn-state-manager/probe_stats.py` | `f84b553cbb7811b07b6336b7a4f8e284a49448f1cb2f439c94069615897678ed` |
| `tools/codex-turn-state-manager/probe_diagnostics.py` | `626849dde03e2edfda7a3b49458e391b487c6dfedc94f72733d90e4d23cede31` |
| `tools/codex-turn-state-manager/panel.html` | `f07db8fec452521573c3973195da6ff427ec9c52c7ac0f45036f20e12b0c0e11` |

`cmp` and SHA-256 comparison against the fixed release matched for every file.
The same values are recorded in the runtime hash manifest. There is therefore
no target-specific code inside these four source artifacts.

## Source behavior and target boundary

| Component | Inherited external behavior or default | Target treatment |
| --- | --- | --- |
| `manager.py` | Uses the legacy `Sub2APIHost` Docker/psql command path and defaults its state directory to `/var/lib/codex-turn-state-manager`. | `integrated.py` rejects that state directory and requires an explicit valid target container. |
| `manager.py` | `FeishuNotifier` defaults to enabled and contains the former Vault socket/path behavior. | `integrated.py` rejects any configuration where `alerts.enabled` is not exactly `false`. |
| `manager.py` | `DegradedAccountsClient` defaults to enabled and uses an old token file, Docker bridge lookup, and `/internal/degraded-accounts`. | `integrated.py` rejects any configuration where `degraded.enabled` is not exactly `false`; the target handler obtains degradation data through its existing authenticated database path. |
| `manager.py` | Webshare import requests occur only for an explicitly configured `webshare_url`/`webshare_api` source. Manual proxy testing requests `api64.ipify.org`. | These remain explicit operator actions/configuration, not startup traffic. |
| `manager.py` and `panel.html` | Manual add defaults to `gpt-6-astra`, target length `292`, and exact-length acceptance. | These are frozen source defaults. Target account/model configuration must provide the intended model and target length. |
| `probe_stats.py` | Persists aggregate counters in local `probe-stats.sqlite3`; `state_292` is a fixed legacy observation metric. | Health/quality interpretation must use `target_hits` and `persisted`, not `state_292`. |
| `probe_diagnostics.py` | Uses stdlib-only, bounded and redacted diagnostic records. | No network, Docker, notification, token, or raw-state retention behavior was found. |
| `panel.html` | Uses same-origin API calls and expects the local panel/Caddy header convention. It contains no remote script, stylesheet, or CDN dependency. | The Vue wrapper uses its bounded authenticated bridge; no administrator credential enters the frame. |

The operational boundary is the entry point. Running `python manager.py` directly
would bypass `integrated.py`'s explicit-runtime guard and restore the source
defaults for Feishu and the old degraded bridge. Deployment and operator
instructions must start `integrated.py` only. The example target configuration
already sets both legacy services to `false`.

## Gateway baseline comparison

The exact diff from `881f3202694c6bc932446931a30c27d9675178b9` contains five
insertions and no deletion in these files:

| File | Added call | Placement |
| --- | --- | --- |
| `backend/internal/service/openai_gateway_forward.go` | `applyPinnedCodexTurnState` | After account header handling and Codex beta features; before the existing routing hint and audit call. |
| `backend/internal/service/openai_gateway_messages.go` | `applyPinnedCodexTurnState` | After the compatibility turn state and restored OAuth identity. |
| `backend/internal/service/openai_gateway_passthrough.go` | `applyPinnedCodexTurnState` | After account header handling and Codex beta features; before the existing routing hint and audit call. |
| `backend/internal/service/openai_ws_forwarder_payload.go` | `applyPinnedCodexTurnState` | After the supplied WS turn state; the existing identity, account-header, routing, and audit steps remain in place. |

The helper validates the account/model lookup and header value before writing
`x-codex-turn-state`. Existing cross-account echoed-state protection still runs
before the HTTP and passthrough requests are built. OAuth accounts do not enable
the account header-override feature, so its later WS call cannot displace the
pin for this monitor's eligible accounts.

Existing native WS pooling retains handshake semantics: a refreshed pin takes
effect on a new upstream WS connection, while HTTP requests apply it per
request. This is documented scope, not a new gateway regression.

## Host adapter and scheduler propagation

`tools/codex-turn-state-manager/legacy_host_adapter.py` copies the source
`fetch_account` and pin writer with only these target adaptations:

- Account names may contain Unicode, spaces, and quotes but cannot contain
  control characters. The name is UTF-8/base64 encoded before it is compared in
  SQL, so it is never inserted as a raw SQL literal.
- Eligibility is limited to active, non-deleted OpenAI `oauth` or `setup-token`
  accounts. Token, ChatGPT account, device, and Codex-version checks stay in
  the copied source behavior.
- The JSONB merge remains one `UPDATE ... RETURNING` operation. Its CTE inserts
  `('account_changed', account_id, NULL, NULL)` into `scheduler_outbox` only
  for an updated account, within the same `run_sql(..., read_only=False)`
  transaction. The source read-back verification remains intact.

The target outbox schema requires only `event_type`; null `group_id` and
`payload` are valid. The scheduler consumes `account_changed` by loading the
fresh account with `GetByID`, updating the account snapshot cache, and rebuilding
the account's groups. Its default outbox poll interval is one second; the
300-second full rebuild is a fallback rather than the normal pin-propagation
path. No schema change or duplicate outbox implementation is needed.

## Validation and operational boundary

- Keep the source-hash check in release validation so future source changes are
  deliberate.
- Explicit-runtime rejection coverage includes enabled alerts, enabled degraded
  access, the legacy state directory and missing project target configuration.
- `TestCodexPinIntegrationPreservesForkIdentity` checks HTTP, passthrough and
  WebSocket header builders for pin precedence, expiry fallback, account isolation
  and unchanged identity/routing headers. The Anthropic compatibility insertion
  has source review; real-server smoke is still pending.
