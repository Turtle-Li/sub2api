# Model status: dense history, quota context and cache capability

Owner correction, November9: refine the existing public status module and
homepage cards. Preserve selected groups/models, ten-minute checks, stored
results, pricing separation and normal connection drain during release.

## Behavior and reference

- History:72 thin ten-minute cells span12h. Success, TTFT and raw check-cache
  ratios retain their independent180-minute window and existing thresholds.
  Public and homepage strips accept prior18-cell payloads while upgrading;
  arrow/Home/End navigation uses one keyboard stop per strip.
- Cache: qualified real shared-pool best complete hour in24h stays the numeric
  metric, including actual low values. If absent, show “缓存已验证” only after
  a positive valid native receipt from the currently selected check model in
  the previous24h, with checks enabled and versioned capability metadata.
  Otherwise show “待验证”. This establishes capability, not conversational hit
  rate. Raw zero/low probe ratios remain unchanged; no extra warm-up or probe
  frequency changes.
- Quota: a bounded batch snapshot reads only explicit Claude global5h/7d
  utilization/reset fields of currently bound active enabled accounts. Ignore
  expired/manual-disabled accounts and plain429s without quota evidence.
  All relevant windows must reset before an account can restore quota: use
  the latest known blocking window per account, then earliest recoverable
  account. Undated old usage alone cannot prove a current block. Unknown
  blocking reset defeats that account's prediction. Other configured routes
  mean partial restriction, not full-pool exhaustion. Scope is the whole
  group, independent of the cheap probe model. Model-specific Fable limits
  are not global-pool exhaustion and are not inferred here.
- Public DTOs contain only limited/exhausted, observed/reset times; no account
  names, IDs, counts, credentials or raw provider errors. The clients expire a
  quota snapshot after90s and show “等待检测确认” at a reached reset. A known
  countdown estimates quota restoration, not guaranteed route recovery.
- A failed supplementary quota snapshot omits quota context without erasing
  independently available status/history.
- For an API-key account in a Claude-Code-only group, the internal check reuses
  AccountTestService's native client headers, system/metadata test payload and
  exact model, reducing its output budget to one token. OAuth/setup-token and
  other account kinds keep the original request so their normal gateway mimic
  path is preserved. The API-key outbound builder test confirms actual forwarded
  client headers, exact model and one-token budget; ordinary client admission,
  credentials and owner configuration are unchanged.

Pinned reference inspected: ranxi2001/sub2api
`5f0b6321a50153a738aef3fbade57c969dbe710c` (LGPLv3),
`backend/internal/service/channel_monitor_v3.go` (accumulation/cells),
`channel_monitor_v3_probe_test.go`, and `account.go` (`IsSchedulable`). Retain
attribution. Reuse interval bucketing and explicit reset semantics; dense local
group cards, separate metric/history windows, capability metadata and sanitized
quota batch snapshot are intentional local additions. Existing native cache
normalization and source boundaries remain documented in
`MODEL_STATUS_PROBE_CACHE_20261108.md`.
Also inspected the same pin's `account_test_service.go` (`createTestPayload`,
`claude.DefaultHeaders`) before reusing the native test profile; the one-token
budget and exact restricted-group routing are intentional local differences.

## Read-only diagnosis

At17:28UTC November8, Claude group10 had OAuth account6 at100% five-hour
utilization, an explicit18:00UTC reset and92% seven-day utilization. Another
bound API-key account248 supported the configured check model but repeatedly
returned HTTP403 in upstream logs; no403 detail was persisted in ops error rows
for the synthetic runner. The group explicitly has `claude_code_only=true`;
the old API-key check forwarded `Sub2API-Availability-Probe` to its provider.
The internal check bypasses local HTTP client validation, so that validator is
not evidence that the old probe was rejected locally. An upstream client's
allowlist can reject this generic profile; without the original403 body its
relationship to these particular upstream403s remains an inference. It is not
proof of depleted credit or a model mapping
error. Do not change credentials, provider settings,
bindings or reserve policy, or issue paid diagnostic requests, on that evidence.
Concurrent owner main606df1c459f0d5071154ef9468e136b22a71fb5d was safely
fast-forwarded before review. It distinguishes client-policy403 and normalizes
the real CLI's degraded UA; its related tests also pass. The normal gateway's
native retry policy is inherited by each outer check
attempt; this change does not alter forwarding or retry policy.

Qualified real-pool best-hour cache values during diagnosis were GPT98.94%,
Claude98.08%, DeepSeek99.04%. Kimi/GLM/MiniMax had no token-bearing real traffic
in that window, while native checks contained both cache hits and cold misses.
Those isolated ratios do not establish normal continuous-conversation rates.
No production configuration or stored history was modified.

## Validation and release

Scoped service tests passed, including12h/3h separation and quota boundaries;
real PostgreSQL validates the bounded public-group snapshot and excludes
credentials/private fields. Frontend94 tests and homepage14 tests pass. The
production frontend builds with the existing advisory chunk-size warning.
Desktop1440 and mobile390 were manually verified on the app and homepage:
72 cells, no horizontal overflow, one keyboard stop, live countdown, native
cache verification/pending states; no local browser errors. Evidence:
`.omo/evidence/status-refinement/` (local only).

Release uses the canonical exact-main GitHub image plus immutable homepagev9,
strict prior-HTML SHA CAS and maintenance lock. No migration required. Retain
the prior compatible app image and immutablev8 as rollback; never rewrite usage,
quota configuration or history. Production verification is recorded below once
the canonical run completes.

Independent Claude Code review initially requested narrowing the restricted
client profile to API-key accounts (OAuth could otherwise select a different
gateway path). That correction and actual outbound-builder coverage were added.
The nonblocking rollback keyboard-index issue was also fixed and tested.
The focused official Claude Code re-review returned APPROVE after verifying
both corrections against the actual forwarding path. The other reviewed source
hashes remained unchanged. Production release and live backup-route recovery
are not established by this local approval.

## Production acceptance — November8 UTC

Source `8403b219df2cd5a46f2ea86b9259a3a2911f4674` was pushed directly to
owner main after a fresh fetch. Canonical GitHub run `42509827294` completed
successfully; image identity, unchanged-main gate, blue-green release and Caddy
activation all passed. The active blue container has that exact revision,
healthy status, zero restarts/OOM and a validated read-only active background
fence. Prior green606df1c45 remains healthy/standby while its old connection
drains; no connection was terminated for this release.

The locked strict-CAS homepage publication installed immutablev9. Served hashes:

- HTML `94586f5e7d5a5f1aaaeb7a37ce2cc3fe6010d9106cd3c8c5cd92ea169721b543`
- JS `f19319df9129b90254f249309a3f490d418a313202d46d27dcda3fc62b3d9b6e`
- CSS `e2eb6929b380bebddfc79a6d02b2de8e02acc7cbd95601a817d01076f3e4ffde`

Prior HTML remains at `/data/sub2-web/home/index.before-status-refinement-20261109.html`;
publication evidence is `/var/log/sub2api-release/model-groups-20261107.f0a87G`.
Both anonymous API/www responses pass72-cell/720-minute history,180-minute
metrics, native-proof version/window, source/privacy and plaza separation
assertions. All six public groups are currently operational. Qualified real
best-hour rates are GPT98.94%, Claude98.08%, DeepSeek99.04%; the other three show
native cache verification rather than isolated probe percentages. Their actual
low probe rates remain present and unchanged in the raw API fields.

Real Chrome acceptance verified both public surfaces at desktop and390px
mobile widths:72 cells per card, one keyboard stop, ArrowLeft focus movement,
no horizontal overflow, no app browser errors. Production captures are under
`.omo/evidence/status-refinement/production-*` (local only). No live countdown
was manufactured: the primary Claude quota window had reset and automatic
checks at18:10,18:20 and18:30 succeeded. Quota warning/countdown boundaries were
verified in local fixtures/tests. A further bounded historical-log read found
36 backup403-related log lines without explicit CLI-only, depleted-credit or
model-denial proof. Backup recovery remains unproven; no account, credential,
model or reserve-policy change was made for this task.

Post-release read-only configuration verification retained version5,
`public_enabled=true`, and the original group-probe configuration digest
`ec263ba0fb0fddbde71cc8786192c207`. Model/account choices remain owner-managed.
The first natural post-release18:40UTC check round completed for all six groups
at18:40:34–18:40:42UTC: all succeeded on outer attempt1. No manual check or
warm-up request was submitted. This proves the new generation's scheduler and
ordinary selected routes; it does not establish a particular backup's recovery.
