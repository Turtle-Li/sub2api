# Real-request account-pool cache — owner November8

The owner separates inexpensive connectivity checks from cache measurement.
The old selected-group scope missed the actual subscription traffic sharing
the same account pool. The dashboard and status already used the same token
ratio; this change corrects its data scope and publication policy.

## Behavior

- Selected cheap models remain connectivity checks only. No probe, billing,
  account-selection or three-attempt retry behavior changes.
- Cache reads the dashboard's real `usage_logs` buckets for every text model
  served by the selected group's current bound, nondeleted accounts, across
  active groups on the same platform, including subscription traffic. Sum
  cache-read tokens / (uncached input + cache creation + cache read). Include
  real zero-price usage; exclude media, synthetic checks and unrelated accounts.
- Public cards still require explicit selection of active nonexclusive groups.
  Subscription groups contribute only to the aggregate ratio; their identities,
  accounts, models, users and traffic volumes are never published.
- Cache readiness is independent of the group's own traffic and passive
  collector. Require20token-bearing requests or the higher configured minimum.
  Collapse the cache column below90% or without adequate samples; explain this
  display standard without changing persisted statistics or rounding values up.
- Success, first-token P50 and history remain group-only and label that scope.
  Cards label cache as shared-account-pool/all-model traffic. An idle check
  group can therefore show real cache while its own other metrics await data.
- `/status`, all-user `/monitor` and the separate homepage status section agree.
  Model plaza remains pricing-only. Immutable homepage assets move to v5.

Contract, upstream pin and tests: `docs/features/MODEL_AVAILABILITY_GROUPS.md`.

## Verification

The service and UI assertions failed against the old implementation before
repair. All V3 service tests pass (final3.051s). Actual PostgreSQL18/Redis
integration passes12.143s, covering public/subscription traffic, multiple real
models, weighted disjoint buckets, zero-price usage, binding revocation,
unrelated-account/platform/inactive-group/media/probe exclusion and unchanged
probe claim/final-outcome deduplication.48frontend tests and4static tests pass.
Frontend production build/typecheck, Go build, handler/routes and publisher
syntax pass. Chrome desktop and390px mobile, local fixtures and real production
status/homepage verify column collapse, independent readiness and no overflow.
Both production pages have no observed console errors. Temporary viewport and
task-owned local QA servers were restored/stopped.

One independent read-only reviewer approved Standards and Spec with no blockers
on the20-file frozen diff, SHA256
`652f2b0e4da85b564f4767147c931c1e4b59d398e77564784ecff5d539e60bcb`.
Evidence: `.omo/status-cache-qa.md`,
`.omo/evidence/status-cache-code-review.md`, test logs and
`status-cache-live.png`, `status-cache-live-mobile.png`,
`status-cache-live-home.png` (local task artifacts, not shipped page assets).

The same raw SQL measured11.700ms on production using the existing account/time
index. Before release its actual pool ratios were GPT97.46%,Claude97.78%; the
live browser later showed97.6%/97.2%. These are separate rolling observations,
not fixed expected values. Samples below the display standard remain hidden.

## Canonical release and live proof

Implementation: `a81eb3c73efd2278d299ea5788078f4b2a82ac72` on fork main,
including concurrent ACME fixes through parent `b261fbcbf`.
Canonical [run42422097364](https://github.com/Turtle-Li/sub2api/actions/runs/42422097364)
completed **SUCCESS** on2026-11-07~22:56UTC.

| Artifact | Verified identity |
| --- | --- |
| Version |0.2.21|
| Release image |`sub2api:auto-20261107-145534-a81eb3c7`|
| Compressed archive bytes |110010386|
| Archive SHA256 |`26073b0597378f202eca736569fba41156b9c787b529d75c39d45fc91c91baa9`|
| Loaded image |`sha256:aeeedaed746901cb6c12991bb04414acea13c29a2a3078c1ceb6cff64e5f9cd5`|
| Server evidence |`/var/log/sub2api-release/gha-20261107-145534-a81eb3c7-1975978`|

Activegreen is healthy,0restarts/OOM, correct read-only generation mount and
sole background owner. Priorblue d65140f7a entered standby and natural drain
(one remaining connection at post-release preflight); legacy is stopped.
New connections go togreen. No connection termination, raw application lifecycle
command or retained-drain override was used. Canonical rollback remains available;
preserve working automatic API certificates and persistent Caddy data.

Anonymous API and www origin smokes both publish exactly groups6/10/17/18/35/36,
all currently operational. Group success/latency stay null without real group
samples, while GPT/Claude pool cache is ready. No private identity, selected
model or traffic volume is serialized.18ten-minute cells,180minutes and minimum
sample guards remain intact; model plaza contains no availability enrichment.
No production settings were saved. Before/after config version5,publicenabled,
`group_probe` MD5 `ec263ba0fb0fddbde71cc8786192c207` match exactly.

## Homepage publication and rollback

The reviewed publisher acquired the shared production maintenance lock, checked
the previous HTML SHA and new API cache scope, installed immutable v5assets
first, then switched and verified HTML atomically. No Caddy configuration reload.

| File | SHA256 |
| --- | --- |
| HTML |`dfd6c9161ab3e2d788355bdb59c5d0420446d5a332279d14cf84711868996bf5`|
| Module |`afc71c6f89034e36e2aa4455dc36fd0ea45f8667dd93d39ea7815e2b6bbd2038`|
| CSS |`12f1a7f462a2287400aa345995e471ce54653a11751c158815a59aee9e0fb903`|
| Prior HTML |`1be1a057a1038013985b801e68fcdce743a6e9835185a9a49c2b24e7b1e29668`|

Static evidence: `/var/log/sub2api-release/model-groups-20261107.xwWmHI`.
Previous HTML: `/data/sub2-web/home/index.before-status-cache-20261108.html`
inside the existing Caddy container. Restore that reviewed HTML through the
same maintenance-lock, hash-check and atomic switch boundary if needed; keep
immutable assets and the current certificate configuration. No database restore.

The original24h check-use trial remains2026-11-07T20:03:22Z through
2026-11-08T20:03:22Z, with heartbeat24 unchanged. Probe configuration, retained
receipts, hidden Kimi history and the supplemental diagnostic are preserved.
This cache UI release adds no paid diagnostics and does not restart that window.
