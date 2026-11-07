# Model status cards correction — 2026-11-07

Application source: `e8c77ca5f59d2c4003c47b864a2abb2336685683` on fork main.
Explicit canonical release:
https://github.com/Turtle-Li/sub2api/actions/runs/42388536982.
Canonical workflow completed successfully; live acceptance below was checked
at approximately 2026-11-07 16:15 UTC.

## Owner contract and correction

The dedicated `/status` and signed-in `/monitor` now render platform sections
with group cards, without the model plaza's prices. Three request metrics
(cache-read share, success rate and first-token P50), 18 five-minute history
cells and a separate representative-check footer follow the supplied reference.
The existing homepage and model plaza retain their compact inline summaries.
Administrators manage exact group/model switches inside `/monitor` → Manage
checks; ordinary users see cards within their existing group visibility.

All three request metrics use the same 90-minute window. Percentages require
at least 20 valid real requests (or the higher configured minimum); P50 also
requires that many latency observations. The independent representative-check
percentage requires six completed checks. Empty slots and ignored request
categories do not enter denominators, and checks never become customer usage.
No-data, insufficient and stale metrics show explicit labels and em dashes.
Refresh errors clear old health. The additive status DTO excludes pricing,
traffic/token counts, account identities and upstream error details.

The owner asked to configure the concrete models later. Only the already
failing Kiro group29 active check was paused through the authenticated admin
UI before release. Its exact model and history remain. The other selected
targets were not changed. Do not automatically substitute or enable models.

## Local verification and independent review

- Go unit tests: service, handler and routes, scoped to ChannelMonitorV3,
  ModelPlaza, ModelStatus and FilterPlaza; pass. A low-sample regression was
  observed failing before the fix, then passed.
- Real PostgreSQL ChannelMonitorV3 repository integration and Go build pass.
- Frontend production build, 99 targeted tests and 12 static-catalog tests pass.
- Owned Chrome: public and ordinary-user cards, admin inline model edit/save
  with dummy local values, search, desktop/mobile, light/dark, no horizontal
  overflow, and homepage inline summaries. Fixture metrics do not establish
  actual model uptime. Failed refresh and deliberately inconsistent
  insufficient-sample payloads have regression tests.
- Independent read-only code/visual reviewer: disposition **ship**, no material
  findings. The unavailable named finish-reviewer role was replaced by an
  available independent code reviewer. Two manual detector warnings were
  confirmed cross-selector false positives. No second detector was run.
- Staged-file secret guard passes. No migration or dependency was added.

## Release preflight

The approved `sub2api-aws-candidate` alias and root-owned release configuration
resolve production to `https://github.com/Turtle-Li/sub2api.git`, branch `main`,
background release mode `activate`. Immediately before dispatch, Caddy selected
`sub2api-blue`, source `63d65d123eff3703dabdb4f5e292140c52fe8a62`, healthy,
zero restarts/OOM; prior green was stopped. Fork main matched the worktree's
baseline before the fast-forward push. Application lifecycle is owned by the
canonical shared-lock blue-green helper, retaining its automatic rollback.

## Separate static publication

Publish only after the application API confirms `window_minutes:90`, using
`deploy/publish-model-group-status-static.sh` under the same maintenance lock.
The script verifies immutable asset digests before atomically switching HTML.

| Artifact | SHA-256 |
| --- | --- |
| Previous live homepage | `704c1955ea56cb6253e09e0962e6899706410b4bbd54e74c9f4c7983c39aeaf8` |
| New homepage | `cd0d103433866adf0f42cf5a35b4b881c3a5c234924a33efcd6e7b5bdc17bc9e` |
| v2 module | `ed16a852b20ea2bc17eac7f868b8d2b286b120b091d0043e56d3f017ffad0c68` |
| v2 stylesheet | `530b959a3a8c1692464ef035760abdec43925141ee3974f23852ca4100bab918` |

Static rollback: restore only
`/data/sub2-web/home/index.before-model-status-cards-20261107.html` atomically
under the shared maintenance lock, checking the saved prior digest. No Caddy
reload or database restore is required. Application rollback follows the
canonical compatible-image path; retain monitor history and additive tables.
Disable representative checks before promoting an older unaware binary.

## Live acceptance

- Caddy selects `sub2api-green`, source
  `e8c77ca5f59d2c4003c47b864a2abb2336685683`, accepting traffic and sole active
  background owner. Container health is healthy, zero restarts/OOM. Prior blue
  is stopped; the compatible prior image/history remain available.
- Anonymous GET `/api/v1/model-status` returns 200, no-store and language Vary.
  Its ten visible groups have only the four allowed group keys. Available
  objects use 90 minutes; non-ready request/check percentages are null. At the
  bounded observation, one group had sufficient request metrics and eight had
  unknown current state. This is not a claim that all models are healthy.
- Live browser `/status` has ten group cards, zero tables and no horizontal
  overflow at desktop or 390px. A real sufficient-sample group displayed cache
  share 75.2%, request success 100.0% and P50 3.0s at this snapshot; these figures
  are observations, not fixed promises. Other groups retain unknown/sample
  explanations. Stopped Kiro retains historical red cells without a current
  failure badge or a synthetic percentage.
- Signed-in `/monitor` exposes Manage checks to the owner. Read-only inspection
  confirms group 6 `gpt-6-luna` and group 10 `claude-haiku-5-5` enabled; group 29
  `claude-haiku-4-5` disabled. Save is disabled because no value was edited.
  Exact model selection remains for the user. Ordinary-user UI and server
  visibility behavior were validated locally; no other person's live session
  was acquired.
- The separate static publication passed its 90-minute API gate, assets-first
  digest checks and atomic switch under the shared maintenance lock. Evidence:
  `/var/log/sub2api-release/model-groups-20261107.BeGSer`. Public homepage
  SHA matches the new digest above. The header's status link stays `#models`;
  live inline summaries show the same cache/TTFT and correct source labels.
  The live model plaza also renders the new inline metrics. No Caddy reload or
  cache deletion was used.
- Local live screenshots: `.omo/status-cards-live-desktop.jpg`,
  `.omo/status-cards-live-mobile.jpg`, `.omo/status-cards-live-admin-config.jpg`
  and `.omo/status-cards-live-home.jpg`. Temporary viewport overrides were
  reset. Task-created local Vite/fixture servers were stopped after QA.

No additional paid test request, model substitution, entitlement or billing
change was performed for this correction. The two existing enabled checks
continue their previously authorized ten-minute schedule. Live observation is
a bounded smoke, not a sustained-availability acceptance claim.
