# Model availability status

Implementation contract (2026-11-07). User requested reuse of the researched
Sub2 status logic and good design, with anonymous public status and logged-in
group-scoped views. Preserve the incumbent UI if adequate; a broad redesign is
outside this change. Implementation and release evidence are recorded below.

## Reference and intended differences

Primary reference: https://github.com/ranxi2001/sub2api at
`5f0b6321a50153a738aef3fbade57c969dbe710c` (LGPL v3, retain attribution).
Inspect `backend/internal/service/channel_monitor_v3*.go`, corresponding
repository/handler/tests, migrations 264/273, `frontend/src/features/channel-monitor-v3`,
`frontend/src/api/channelMonitorV3.ts`, and its locale files before porting.
Historical visual evidence is pinned at
`297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/channel-monitor-v3`.
The passive port starts from release v2.9.9, commit
`747f77c496e90c9eb250fee78796eb84b434f37d`, comparing the newer fixed revision
for freshness, privacy and regression behavior. Both revisions are read before
adapting the related files; preserve attribution to ranxi2001/Sub2API.

Adopt the component-to-group/optional-model mapping, request-derived slot
classification, unknown/stale semantics, aligned timelines, scoped/sanitized
output and sustained-outage event calculation. Reuse local V2 aggregation and
group entitlement authority. The reference's `public` still requires login;
our explicit published components will also support anonymous viewing through
a separate sanitized endpoint. Public access starts disabled and empty.

Do not import unrelated payment, provider, scheduler or deployment changes.
Do not import Pelican solely to acquire idle probes. Synthetic probes, if
adapted, remain separately opt-in and must not be mixed into request success
rates or silently begin spending. No samples or stale collection means unknown.
Existing V1/V2 selection remains unchanged until an administrator chooses V3.
V3 keeps passive aggregation running. Internal probes do not prove public ingress.

Implementation scope decision: do not add an active-probe adapter in this change.
The existing V1 checker tests direct provider endpoints and the reference V3
uses unavailable Pelican group scheduling; neither is an equivalent drop-in
probe of the current authenticated group path. Passive statistics, unknown for
low traffic, history and incidents are complete independently. Future synthetic
probes need separate source labels, budget, lease and end-to-end route tests.

Visual inspection: the pinned desktop and mobile screenshots show compact
categories, aligned strips and an incident dialog worth retaining. Preserve the
incumbent theme; improve status-rate labeling and keyboard/touch accessibility.
Do not copy the reference's promotional quota/rate details to anonymous output.

Historical availability is request success rate, not time-based SLA uptime.
Use user-facing labels that identify this denominator. Unknown does not resolve
an ongoing incident, and histories/events must obey the same viewer scope.

### HTTP request outcomes

Both pinned passive aggregation versions build usage successes and final
errors independently. A failed HTTP stream can still write a positive-cost
usage record for tokens it already delivered. For availability and latency
only, the local V2 collector gives a terminal Ops error precedence over that
partial usage. It matches the `client:` usage key to the gateway-generated
`ops_error_logs.client_request_id` with the same API key, using the existing
index. The client-facing `X-Request-ID` can be reused and is not used for
this match; WebSocket billing keys are per turn and are not matched to a
connection-wide ID.

Recovered attempts logged with a final 2xx do not override a success. The
existing final-error deduplication, V3 ignored error categories, usage token
volumes and billing remain unchanged. Usage, errors and latency are computed
from one transaction snapshot. The recent overlap rechecks late writes even
when usage and its terminal error fall in different minutes. Older aggregates
are refreshed only by the existing collector when it recomputes their range.

## Delivery plan

| Task | Owner / allowed writes | Dependencies | Acceptance and evidence |
| --- | --- | --- | --- |
| T1 Core API | Backend worker: backend only in main task worktree | Pinned reference and current local V2 contracts | Components/config persistence, mode, status/history/incidents, anonymous opt-in and authenticated group authorization; failing-to-passing scoped tests |
| T2 UI | Frontend worker: frontend only in separate UI worktree | Match T1's contract, retain upstream API shape where possible | Inspect original screenshots; admin configuration, public route, logged-in status, localized loading/empty/error/stale states; unit tests and typecheck |
| T3 Integration | Root | T1, T2 | Merge scoped frontend diff without unrelated changes; reference/memory docs; build and targeted regressions |
| T4 QA and review | Independent read-only reviewers / QA executor | Frozen integrated diff | Real local UI captures desktop/mobile and themes, authorization and history boundary tests, no secrets in public DTO, regression checks |

Runtime rollback is disable public exposure and select the previous monitor mode;
retain additive configuration/history. New migrations must use local free numbers,
never overwrite already applied upstream numbers. No automatic enablement.

## Quota and publication controls

V3 never calls a model. Adding, hiding or refreshing a component only reads
passively aggregated real requests. Its `enabled` switch controls display;
`visibility` independently selects group-authorized or public viewing. The
optional exact requested-model filter limits statistics to that model. An empty
filter covers all models in its group. Filtering for the cheapest model saves no
additional quota here and must not be presented as proof of other models' health.

Start the owner-authorized production trial with two or three representative
components. Prefer existing traffic, explicit names and five-minute slots; keep
no traffic/stale data unknown. Publish only sanitized components and keep private
groups restricted. Do not send synthetic requests to make a row green. A future
active probe would require a separate off-by-default toggle, model selection,
frequency and spend cap; it is outside this release.

Cost-safety delta: the pinned upstream settings reader falls back to enabled V1
on a read failure. Locally, absent settings dependencies and read errors now
disable monitoring, so a store outage cannot revive active probes. Healthy
missing settings keep the legacy defaults. Existing V1 requests already in
flight are not cancelled by a mode switch; finish them before asserting a quiet
trial. Production preflight found monitoring disabled and saved mode V2.

Branch preflight helper reported missing `.agent-worktree.toml` trunk. Manual
read-only branch/worktree inspection found no matching status task. The isolated
task starts from freshly fetched `fork/main` at
`ce5f17142704f0fbd269b76b7b9d2a266a4a6f20`; unrelated dirty source worktrees are preserved.

## Implemented entry points

- Public page: `/status`; anonymous GET
  `/api/v1/public/channel-monitor-v3/status` and `/incidents`.
- Signed-in page: existing `/monitor`, selected when monitor mode is V3;
  GET `/api/v1/channel-monitor-v3/status` and `/incidents`.
- Admin: `/admin/channels/monitor`, V3 configuration tab, category/component
  editors and preview. Mode writes use PUT
  `/api/v1/admin/channel-monitor-v3/mode`. System settings also preserve/select V3.
- Additive migration `267_channel_monitor_v3.sql` creates configuration and
  component/category tables. `public_enabled` defaults false; component visibility
  defaults to group. Public reads require enabled monitoring, V3 mode, public
  opt-in and active published components; backend-only site mode remains protected.

Enable monitoring and choose V3, create categories/components mapped to active
groups (and exact models when needed), then explicitly enable anonymous access
and mark only intended components public. Signed-in viewers also see their
authorized group components. V3 uses the existing passive collector even when
the V2 view's local enable flag is false, without rewriting that saved flag.

Public DTOs omit internal group IDs, multiplier and request volumes; status and
incident responses use no-store. Collector data older than ten minutes is stale;
sample freshness is the larger of two slots or ten minutes. Featured history
stays live; category strips may page through history while their current status
remains current. Ongoing incident counts use the same seven-day scoped runs as
the incident list, independent of the selected strip. A no-traffic gap does not
resolve an incident; its end is the first observed non-down recovery slot.

## Validation record (2026-11-07)

Local implementation and scoped independent code review are complete. No commit,
push, production configuration change or deployment had been performed at the
initial validation checkpoint. The owner subsequently authorized submission,
release and a small passive production trial.

- Final integrated backend: `go test -json ./internal/service ./internal/handler
  ./internal/server/routes -run ChannelMonitorV3 -count=1`: 22 top-level tests,
  three packages passed, no skips/failures. Broader ChannelMonitor regression
  after incident repairs: 50 top-level tests passed.
- Repository: `CI=1 go test -v -tags=integration ./internal/repository
  -run ChannelMonitorV3 -count=1`: two real PostgreSQL/Redis integration tests
  passed without skips, including migration/config persistence and passive facts.
- `go build -o /tmp/sub2api-model-status-server ./cmd/server` passed; Wire was
  regenerated. Backend evidence lives in `.omo/evidence/model-status-backend/`.
- Final integrated frontend targeted suite: eight files, 44 tests passed.
  `pnpm --dir frontend build` passed with locale validation and Vue typechecking.
  Existing large-bundle warnings remain; no dependency change was introduced.
- Independent review: CLEAR / APPROVE, with mode endpoint, event-count and
  recovery-time findings fixed and regression-tested. Review record:
  `.omo/evidence/model-status-backend-code-review.md`.
- Real-browser QA used deterministic localhost API fixtures: desktop/mobile,
  light/dark, anonymous access, history, incidents, empty/error/stale/no-data,
  keyboard/touch tooltips and explicit anonymous publication disclosure. These
  screenshots demonstrate UI behavior, not production availability. Backend
  authorization/serialization guarantees are covered separately by real Go tests.

Changed backend/frontend source manifest SHA-256:
`a27a6d4628d21258b3d3b4790a71ae0377dc9b4a9729dc7a2e3ca55e4bed2e77`.
Computed over sorted changed source paths and bytes with NUL separators; excludes
documentation, dependencies and generated build output.

Final release delta: nil dependency/read failure regression tests failed before
the safety repair and passed after it. All ChannelMonitor service/handler/routes
tests passed again. Final frontend production build passed after cost/scope copy
and English unavailable-label corrections. Independent delta review is recorded
in `.omo/evidence/model-status-release-delta-review.md`; runtime evidence is in
`.omo/evidence/model-status-backend/RUNTIME_FAIL_CLOSED.md`.

Limits: passive-only, no paid synthetic requests; automatic incident history is
bounded by the seven-day query over retained minute facts, not a manually edited
incident-announcement system. Request success rate is not time-based SLA uptime.
No full-repository or production acceptance claim is made. UI is retained from
the pinned design with local accessibility/copy improvements, ready for a future
visual redesign if desired.

## Production trial (2026-11-07 local / 2026-11-06 UTC)

Owner explicitly authorized commit, merge, release and a few live components.
Feature revision `0c699b5dd9892608443f436c3ec68f806f693b71` was fast-forwarded
to fork `main`; canonical GitHub release run `42297682332` succeeded. AWS
selected healthy `sub2api-blue`, traffic accepting/background active, image
`sha256:1c158fb1cf9d4a67c6b21489e53ba479b87549222e8e87ef346a25a6fc6b6b47`.
Previous green revision `ce5f17142704f0fbd269b76b7b9d2a266a4a6f20` stopped
through the canonical drain flow. Public health returned 200; release audit
app_5xx/app_fatal/caddy_5xx were all zero. No source compilation on production.

The existing owner browser session configured the trial through normal admin UI:
mode V3, monitoring enabled, public enabled, category `模型服务`, five-minute
slots, 90 cells, seven-day request success rate, no featured card. V1 monitor
list was empty before activation. Display multipliers are off for all three.

| Component | Bound group | Model filter | Audience | TTFT threshold |
| --- | --- | --- | --- | --- |
| GPT 按量 · 分组整体 | 按量-0.25倍率 | All requested models | Public | 60 seconds |
| Claude 官方 · 分组整体 | Claude 官方 | All requested models | Public | 30 seconds |
| GPT PLUS · 分组整体 | GPT PLUS | All requested models | Group-authorized | 60 seconds |

Anonymous `/status` and status/incidents APIs returned only the first two
components, no internal group IDs, multiplier or request volumes, and no-store.
Unauthenticated access to the signed-in API returned 401. Historical paging
returned `latest:false` with the same scope. The owner signed-in `/monitor`
displayed all three; ordinary-user group authorization remains separately
covered by the Go tests. Briefly hiding Claude removed only that component
from public output; restoring it returned the two public rows.

Collector watermark advanced from 21:50 through 21:54 UTC without synthetic
model calls. All three rows had no recent samples and correctly remained
unknown; this is not a claim that those models were healthy. At the final
bounded observation the application was healthy with zero restarts/OOM and no
monitor collection errors in the inspected log interval. A single resource
snapshot was 2.33% CPU / 117.8 MiB; it does not establish long-term overhead.
Leave the three components enabled to collect natural traffic. No scheduled
polling automation or paid probe was created.

Rollback configuration: disable public access to withdraw the anonymous page,
or toggle one component off to hide it. Disable monitoring to stop aggregation;
the prior saved state was disabled/V2. Preserve V3 tables/history. For an actual
binary rollback, use the canonical release procedure and compatible retained
image; never restore a database backup to remove this feature.

Sanitized live QA: `.omo/evidence/model-status-live-public-qa.md`. Owner-local
release report and screenshot are under `reports/model-availability-release-20261107.md`
and `artifacts/model-status-qa-20261107/production-public-desktop.png` in the
Sub2API workspace outside Git. These observations are a bounded startup smoke,
not a full-production or sustained-availability acceptance claim.
