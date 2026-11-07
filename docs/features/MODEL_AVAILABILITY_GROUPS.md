# Public group availability module

Historical owner correction, 2026-11-07: the homepage and signed-in model plaza must show
availability inside each existing group card. All viewers use their existing
plaza visibility rules. A link to a separate status page does not satisfy this
requirement. Existing status pages remain supplementary.

Later historical correction, same day: `/status` and signed-in `/monitor` are a
dedicated, price-free status dashboard, organized by platform and group cards.
Keep the compact inline summaries on the homepage and model plaza. The status
dashboard owns the administrator's scheduled-check editor; owners choose the
exact models themselves. Do not automatically substitute a different model
when a selected model fails. The repeatedly failing Kiro target (group 29) was
paused through the existing admin UI, retaining its model and history.

Earlier owner correction, 2026-11-08, superseded the surface and publication
rules above: `/status` and `/monitor` are the primary standalone status module
for all users. Administrators configure it inline. The homepage embeds a
separate `#status` section; neither model plaza nor its pricing cards display
status. Discover all active nonexclusive public groups, including domestic
providers, independently of plaza enablement or user binding eligibility.
Private groups remain excluded. Anonymous data publication remains opt-in.
Display names are independently editable without changing routing or billing.
Hidden private/inactive group settings retain their model and display name;
saving the editor pauses those checks instead of deleting owner choices.
Public cards and JSON do not disclose the selected request model. Admin-only
collapsed request settings retain the exact owner-selected model for checks.

Current owner correction, later November 8: the active public catalog is only
the administrator's candidate list. Publish **only added groups**, identically
on the homepage, anonymous `/status` and signed-in `/monitor`. Unselected groups
are absent even if passive statistics exist. The inline editor adds/removes
groups with the incumbent searchable Select; it does not render the full catalog
as checkboxes. New JSON field `listed` owns publication independently of the
scheduled-check `enabled` flag. Legacy targets without `listed` inherit their
old `enabled` selection, so unchecked blank/paused rows stay hidden. Removing
sets `listed=false` and pauses checks while retaining exact model/display name;
readding restores the fields with checks disabled. A selected group may use only
passive traffic; empty selected history remains unknown. No selection returns an
empty dashboard; anonymous opt-in is checked even when the list is empty.

Request models are an admin-only searchable dropdown, loaded per added group
through `GET /admin/groups/:id/model-allowlist-candidates?available_only=true`.
This opt-in mode uses bound schedulable accounts, the actual group platform,
existing model admission/allowlist rules and exact text IDs; it excludes media
including account-mapped media and wildcard IDs. No accounts returns no models,
and lookup failure remains an error. Existing unavailable choices are retained
and explained, never silently substituted. Discovery sends no provider requests.

Later November8 refinement: singleton platform sections share a fluid grid;
platforms with multiple selected groups expand across the row. Search recomputes
the layout. The homepage uses a fluid card grid in immutable asset namespacev4.
Each card explicitly labels its own group's request scope and explains missing
real-request cache/latency metrics. Cache share is an observed input-token ratio,
not a cache-service availability percentage, and never blends other groups.

The owner authorized one inexpensive request model per enabled group,
one check every ten minutes and minimal output. Active checks supplement quiet
groups; they never enter real-request success-rate denominators. Unknown,
disabled, pending, stale and failed collection must be explicit rather than
blank or fabricated green history. The scheduled check does not certify
every model, public ingress or customer entitlement admission.

## Architecture decision

Reuse the pinned ranxi2001 revision documented in MODEL_AVAILABILITY_STATUS.md:
the unique group/slot claim, separate probe results, group scheduler admission,
bounded workers, shutdown cancellation and freshness rules. Adapt its Pelican
router to the existing gateway services; do not import Pelican's broad test
suite, challenge workload, account failover or blended request/probe percentage.
Reuse OpenAI and Anthropic group paths plus the catalog's four CN text providers
(DeepSeek, Kimi, Zhipu and MiniMax), through their native OpenAI-compatible gateway.
Unsupported platforms
retain passive statistics and cannot accidentally submit a paid media job.

Probes use the existing scheduler, account concurrency and provider forwarding
with a tiny text request, one selected account and no monitor-owned replay. No
new credential or customer billing identity is introduced. Store synthetic
outcomes separately; the existing account/provider egress rules still apply.
Use a persisted ten-minute claim before sending, so a restart or overlapping
generation cannot duplicate a paid check. The runtime background gate fences
standby/draining generations. A failed/unfinished attempt is never retried in
the same slot. Preserve cancellation as unknown.

The status service computes one bounded batch for selected active public groups.
Its public DTO contains sanitized state, observation source/time, separate probe
and real-request percentages and a compact recent strip. Public cards show only
real-request percentages; probe results inform history/current connectivity.
No accounts, selected model, errors, traffic volumes or private groups are published.
Public publication remains governed by the explicit status opt-in.

## Execution DAG

All implementation tasks have one writer in the existing isolated worktree.

| ID | Depends | Owner / files | Acceptance / evidence | Rollback / stop |
| --- | --- | --- | --- | --- |
| G1 | Pinned source comparison | Developer / backend probe model, additive migration, repository and runner | Off by default; one claim/10 min; cancellation/lease/disable/error boundaries; no user-fact writes; minimal request | Disable probes; retain rows; stop if admission or cost cannot be bounded |
| G2 | G1 | Developer / status handler, service, admin API | Batch active public groups; private-group isolation; real/probe rates separate; no invented samples | Additive DTO; preserve existing pricing |
| G3 | G2 | Developer / dedicated status module and versioned static homepage assets | Public/ordinary user group cards; admin-only configuration; desktop/mobile screenshots; no plaza enrichment | Restore previous HTML; additive assets may remain |
| G4 | G1–G3 | QA / frozen changes and real surfaces | PostgreSQL claims, scope and dedup regressions; frontend build; browser screenshots and interactions | Fix failures before release |
| G5 | G4 | Operator / exact main build and static publication | Fresh runtime preflight; canonical release; small owner-authorized probe trial; inspect actual outcomes | Canonical compatible application rollback plus atomic static HTML restore |

## Implemented contracts

The fixed upstream reference is `ranxi2001/sub2api` at
`5f0b6321a50153a738aef3fbade57c969dbe710c` (LGPL-3.0). The executed reference
is `backend/internal/service/channel_monitor_v3_probe.go` (claim / runProbe /
verdict), its `channel_monitor_v3_probe_test.go`, the corresponding repository
probe methods and migrations 264/273. Local source attribution is retained.
Local differences: no Pelican, cognitive challenge, or
synthetic rows in customer usage; at most ten enabled checks and 1000 configured
display-name rows; one unique
group/ten-minute slot claim even after model changes. A selected-account native
forwarder may retain its own protocol compatibility handling.

Owner correction on2026-11-08: one claimed check may make at most three
selected-account attempts, stopping at the first success. The normal scheduler
and account concurrency admission run for every attempt. Only three completed
failures publish a failed check. Configuration withdrawal, execution fencing,
shutdown, or failure to persist progress stops further spending and leaves the
check unknown. Each attempt retains the45-second deadline; retries wait250ms.
The runner remains serial, so persistent timeouts can delay later groups and
freshness then becomes unknown. It never reclaims an unfinished slot to catch up.

Migration269adds private attempt/missing-usage counters and a status-history
visibility flag. Progress writes retain cumulative tokens before any retry;
the account/latency in the final receipt describe the final attempt. Clearing
display history preserves the unique paid claim and private consumption totals.
Old writers can finish during natural drain; their attempt counter stays NULL,
while new claims explicitly start at0. The report treats only NULL legacy
completions as one attempt; an unexecuted new cancellation stays0. Rollback retains
the additive columns and receipts, and disables probes if restoring an older
verdict would republish Kimi's false outage.

Migration `268_channel_monitor_group_probes.sql` is additive. The global and
per-group probe switches start disabled. Scheduled-check configuration is at
`/monitor` → Manage checks (administrators only); the former settings panel
links there. A target chooses an
exact request model name. Support covers OpenAI/Anthropic and the four CN text providers.
The admin runner's private exact-group context prevents Claude client fallback
from certifying a different group. This is provider readiness, not customer
client/admission certification; ordinary client restrictions remain in force.
Selected account model mappings to media are rejected before submission.

Claude requests use `max_tokens: 1`; OpenAI requests use
`max_completion_tokens: 16` and `reasoning_effort: none`. Existing protocol
conversion floors still apply, and native Codex OAuth strips unsupported output
limits. Do not promise a strict one-token upper bound for that protocol. A tiny
prompt, inexpensive selected model,45-second attempt deadline and at most three
attempts per claimed check bound the trial; actual input/output tokens from all
attempts are retained privately.
The existing HTTP stream-drain helpers preserve the private probe execution
deadline/cancellation; ordinary clients still retain their normal usage drain.

Private check input receipts count all provider-reported input tokens. OpenAI
already reports a total, while native Claude requires summing its disjoint
ordinary, cache-creation and cache-read buckets. Historical receipts are not
rewritten. The operator-only24h consumption report is
`deploy/model-status-probe-consumption.sql`; missing upstream usage remains
unknown rather than proven zero. It never reads customer usage or bills users.

For Chat Completions, nonempty reasoning followed by the explicit `length`
terminal also proves connectivity: the monitor intentionally exhausts a tiny
output budget before some reasoning models emit visible text. Empty streams,
reasoning without this terminal, and any provider error still fail. This is
connectivity evidence, not an answer-quality test. The observed WorkBuddy Kimi
contract, fixed source, numeric diagnostic and correction are recorded in
`docs/operations/MODEL_STATUS_RETRY_CORRECTION_20261108.md`.

The independent same-origin status endpoint returns only sanitized public groups.
The plaza endpoint no longer enriches pricing with availability. Current state is unknown after
15 minutes without observation; passive collection must also be fresh within
ten minutes. A fresh independent probe still works during stale collection.
The 180-minute real-request percentage and scheduled-check percentage are
separate. All request metrics and the 18 ten-minute cells use the same window.
Publish request success rate only after at least 20 valid requests (or a higher
configured minimum); publish check success rate only after six completed
checks. Ignored outcomes, pending checks and empty slots never enter either
denominator. The most recent check can still report a current failure before
there are enough samples for a percentage. Stale request collection hides all
request metrics. Cache share is cache-read tokens divided by input plus cache
creation plus cache-read tokens; token counts stay private. TTFT P50 uses the
existing histogram and requires the same minimum number of latency samples.
Missing metrics render as an em dash with a sample/freshness explanation.
The recent strip uses real traffic where available, checks otherwise.
It is observed history, not a time-based SLA. Sparse real observations are
`observed` (successes) or `errors_observed` (counted errors), without a percentage.
They are not no-data cells. Empty history stays gray; the current empty partial
slot is explicitly `collecting`. Ignored outcomes cannot create observations.

The surfaces are static `/` → `#status`, and dedicated cards at `/status` and
signed-in `/monitor`. The additive `/api/v1/model-status` endpoint uses the
active public-group boundary and a price-free DTO;
anonymous publication still requires the explicit opt-in. It discloses neither
request/token counts nor accounts, upstream errors or custom prices.
Vue and static clients refresh every minute
while visible. Failed refresh removes old status while preserving group identity.
Homepage assets are additive under `turtleroute-status-20261108-v4`; prior
catalog module remains authoritative and untouched.

Failures and recoveries use existing ops email recipients, templates, minimum
severity, recovery preference and a per-runner hourly limit. Delivery identities
deduplicate group/outcome/hour; sustained failure sends at most hourly reminders.
No error body, account credential or usage volume is published. Disabling the
target fences queued sends. Live email configuration was inspected enabled;
delivery requires a real probe failure or recovery and is verified separately.
The shared email service explicitly includes `model_group_probe` in its final
active-generation/BeforeSend fence, after SMTP configuration has loaded.

## Local verification

November 8 module correction: scoped service/handler/routes tests and Go build,
real PostgreSQL JSONB/claim tests, 100 frontend tests, production frontend build,
and 14 static tests pass. Owned Chrome verifies public/admin/ordinary-user
journeys, custom-name persistence, domestic/public-only configuration, hidden
private groups, no status in model plaza, independent homepage section, and
390/320px layouts. A fresh independent reviewer approved the code and visual
finish after fixing hidden-target configuration preservation. Earlier evidence
below describes the historical inline-plaza revision.

- Go service/handler/routes tests with `-tags=unit`, including existing monitor,
  plaza and group-resolution regressions, pass. New tests execute actual OpenAI
  API-key/OAuth and Anthropic forwarding against deterministic HTTP transports;
  validate minimal wire payload, completed output/usage and no media submission.
- Real local SMTP tests verify failure/recovery delivery, hourly deduplication
  and suppression at the final send fence. The fence test failed before the
  shared sender fix and passes after it; no production fault is manufactured.
- Real PostgreSQL integration passes: concurrent claims, same-slot model change,
  idempotent finish, private-group scope, retention and independent user facts.
- Frontend production build passes; six targeted files / 52 tests pass,
  including saving the new group/model switches through the existing editor.
- Real owned-Chrome QA passes at homepage widths 1440/768/390/320 and ordinary
  user widths 1440/390 in light/dark themes. Filtering, inline cards and failed
  refresh are checked. Fixture screenshots establish layout, not live uptime.

The later card revision passes 99 targeted frontend tests, 12 static-catalog
regressions, production frontend and Go builds, scoped Go service/handler/routes
tests and real PostgreSQL repository integration. Chrome checks cover public,
ordinary-user and administrator views, desktop/mobile, light/dark, search,
inline editor save and no horizontal overflow. The deliberately insufficient
fixture contains non-null percentages to verify defensive client suppression.
Reference source and tests were rechecked at the fixed newer revision and the
original `747f77c496e90c9eb250fee78796eb84b434f37d` before this correction.

Rollback: disable representative checks before any older application binary;
retain additive tables/history and use the canonical blue-green release path.
Static publication is a separate assets-first atomic HTML switch under the
production maintenance lock. Restore only its saved HTML for static rollback.
Never restore the database to undo this feature.

knowledge_candidate: no — project-specific integration and rollout contract.
