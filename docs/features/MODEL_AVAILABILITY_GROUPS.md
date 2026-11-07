# Availability inside model groups

Owner correction, 2026-11-07: the homepage and signed-in model plaza must show
availability inside each existing group card. All viewers use their existing
plaza visibility rules. A link to a separate status page does not satisfy this
requirement. Existing status pages remain supplementary.

Later owner correction, same day: `/status` and signed-in `/monitor` are a
dedicated, price-free status dashboard, organized by platform and group cards.
Keep the compact inline summaries on the homepage and model plaza. The status
dashboard owns the administrator's scheduled-check editor; owners choose the
exact models themselves. Do not automatically substitute a different model
when a selected model fails. The repeatedly failing Kiro target (group 29) was
paused through the existing admin UI, retaining its model and history.

The owner authorized one inexpensive representative model per enabled group,
one check every ten minutes and minimal output. Active checks supplement quiet
groups; they never enter real-request success-rate denominators. Unknown,
disabled, pending, stale and failed collection must be explicit rather than
blank or fabricated green history. The representative check does not certify
every model, public ingress or customer entitlement admission.

## Architecture decision

Reuse the pinned ranxi2001 revision documented in MODEL_AVAILABILITY_STATUS.md:
the unique group/slot claim, separate probe results, group scheduler admission,
bounded workers, shutdown cancellation and freshness rules. Adapt its Pelican
router to the existing gateway services; do not import Pelican's broad test
suite, challenge workload, account failover or blended request/probe percentage.
Start with the existing OpenAI and Anthropic group paths. Unsupported platforms
retain passive statistics and cannot accidentally submit a paid media job.

Probes use the existing scheduler, account concurrency and provider forwarding
with a tiny text request, one selected account and no monitor-owned replay. No
new credential or customer billing identity is introduced. Store synthetic
outcomes separately; the existing account/provider egress rules still apply.
Use a persisted ten-minute claim before sending, so a restart or overlapping
generation cannot duplicate a paid check. The runtime background gate fences
standby/draining generations. A failed/unfinished attempt is never retried in
the same slot. Preserve cancellation as unknown.

The plaza service computes one bounded batch for only already-visible groups.
Its public DTO adds sanitized state, observation source/time, representative
model, separate probe and real-request percentages and a compact recent strip.
No accounts, errors, traffic volumes or previously hidden groups are published.
Public publication remains governed by the explicit status opt-in.

## Execution DAG

All implementation tasks have one writer in the existing isolated worktree.

| ID | Depends | Owner / files | Acceptance / evidence | Rollback / stop |
| --- | --- | --- | --- | --- |
| G1 | Pinned source comparison | Developer / backend probe model, additive migration, repository and runner | Off by default; one claim/10 min; cancellation/lease/disable/error boundaries; no user-fact writes; minimal request | Disable probes; retain rows; stop if admission or cost cannot be bounded |
| G2 | G1 | Developer / plaza handler, status service, admin API | Batch visible groups; private-group isolation; real/probe rates separate; no invented samples | Optional additive DTO; preserve existing pricing |
| G3 | G2 | Developer / existing plaza cards and versioned static homepage assets | Inline per-group status for anonymous/ordinary user; enabled/disabled/stale/failure states; desktop/mobile screenshots | Restore previous HTML; additive assets may remain |
| G4 | G1–G3 | QA / frozen changes and real surfaces | PostgreSQL claims, scope and dedup regressions; frontend build; browser screenshots and interactions | Fix failures before release |
| G5 | G4 | Operator / exact main build and static publication | Fresh runtime preflight; canonical release; small owner-authorized probe trial; inspect actual outcomes | Canonical compatible application rollback plus atomic static HTML restore |

## Implemented contracts

The fixed upstream reference is `ranxi2001/sub2api` at
`5f0b6321a50153a738aef3fbade57c969dbe710c` (LGPL-3.0). The executed reference
is `backend/internal/service/channel_monitor_v3_probe.go` (claim / runProbe /
verdict), its `channel_monitor_v3_probe_test.go`, the corresponding repository
probe methods and migrations 264/273. Local source attribution is retained.
Local differences: no Pelican, cognitive challenge, account failover, or
synthetic rows in customer usage; at most ten configured targets; one unique
group/ten-minute slot claim even after model changes. A selected-account native
forwarder may retain its own protocol compatibility handling.

Migration `268_channel_monitor_group_probes.sql` is additive. The global and
per-group probe switches start disabled. Scheduled-check configuration is at
`/monitor` → Manage checks (administrators only); the former settings panel
links there. A target chooses an
exact public model name. Initial support is OpenAI/Anthropic text groups.
The admin runner's private exact-group context prevents Claude client fallback
from certifying a different group. This is provider readiness, not customer
client/admission certification; ordinary client restrictions remain in force.
Selected account model mappings to media are rejected before submission.

Claude requests use `max_tokens: 1`; OpenAI requests use
`max_completion_tokens: 16` and `reasoning_effort: none`. Existing protocol
conversion floors still apply, and native Codex OAuth strips unsupported output
limits. Do not promise a strict one-token upper bound for that protocol. A tiny
prompt, inexpensive selected model, 45-second deadline and a single claimed
attempt bound the trial; actual input/output tokens are retained privately.
The existing HTTP stream-drain helpers preserve the private probe execution
deadline/cancellation; ordinary clients still retain their normal usage drain.

The same-origin plaza endpoint returns an optional sanitized availability object
only after the original viewer/group filter. Current state is unknown after
15 minutes without observation; passive collection must also be fresh within
ten minutes. A fresh independent probe still works during stale collection.
The 90-minute real-request percentage and representative-check percentage are
separate. All request metrics and the 18 five-minute cells use the same window.
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
It is observed history, not a time-based SLA. No-data cells stay gray.

The surfaces are static `/` → `#models`, the existing model plaza, and dedicated
cards at `/status` and signed-in `/monitor`. The additive `/api/v1/model-status`
endpoint uses the original plaza group-visibility boundary and a price-free DTO;
anonymous publication still requires the explicit opt-in. It discloses neither
request/token counts nor accounts, upstream errors or custom prices.
Vue and static clients refresh every minute
while visible. Failed refresh removes old status while preserving prices.
Homepage assets are additive under `turtleroute-status-20261107-v2`; the prior
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
