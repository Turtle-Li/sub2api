# Independent group status module — 2026-11-08

Owner-approved correction to the November 7 card release. Product/statistics
authority: `docs/features/MODEL_AVAILABILITY_GROUPS.md` and its pinned upstream.

## Result

- Dedicated `/status` and signed-in `/monitor`, visible to every user, with
  header/sidebar discovery. Administrators configure the same module inline.
- Homepage `#status` embeds cards separately from price catalog `#models`.
  Model plaza no longer renders or requests availability enrichment.
- All active public groups, including domestic platforms, share a sanitized
  catalog independent of plaza settings, user binding or subscription eligibility.
  Private and inactive groups are excluded from publication and configuration rows.
- Per-group display names are independent of internal names, routing and billing.
  Hidden historical targets retain exact model/name configuration with checks paused.
- Group history is 18 ten-minute cells / 180 minutes, aligned with paid checks.
  Sparse observations have explicit success/error labels without percentages;
  empty history remains unknown and the empty current partial slot is collecting.
  Whole-group request metrics require at least 20 valid requests. Ignored outcomes
  and empty slots never become failures or percentage denominators.
- Public cards and JSON do not disclose request-model identity. Admin request
  settings are collapsed. Independent checks never enter customer request facts.
  Four domestic text providers reuse their existing native Chat forwarding;
  unreviewed media/other protocols remain passive-only.

## Verification and publication boundary

Scoped Go service/handler/routes regression, Go build, PostgreSQL integration,
100 frontend tests, frontend production build and 14 static tests passed. Real
Chrome checked public/admin/ordinary-user views, custom name save/refresh,
domestic and private scope, price/status separation, sample guards and 390/320px
layouts. Two detector warnings were unrelated-selector false positives; a fresh
independent code reviewer substituted for the unavailable named finish-reviewer
role and approved the change after the hidden-target preservation fix.
Local evidence is in `.omo/status-module-qa.md` and
`.omo/evidence/status-module-code-review.md`, excluded from shipped assets.

No migration, new credential or additional paid QA request. Existing owner
checks are preserved: groups 6 and 10 enabled, group 29 paused; no model
substitution or new domestic check is enabled by this release.

Before push, fast-forwarded main's concurrent image-workspace commits through
`8a51ec443b27959970b8b6afad31286fca827113` without overlap. Wait for its canonical
deployment to complete before pushing, since builds abort if main advances.
Production preflight confirmed the configured fork/main/activate boundary and
the healthy active application. Prior homepage HTML SHA256:
`cd0d103433866adf0f42cf5a35b4b881c3a5c234924a33efcd6e7b5bdc17bc9e`.

## Rollback

Use the canonical compatible blue-green release under its shared maintenance
lock. Keep probe results/configuration and existing additive tables. Never
restore a database to roll back this UI change. Static publication uses
`deploy/publish-model-group-status-static.sh` after the matching API is live,
with immutable `turtleroute-status-20261108-v3` assets, digest checks and atomic
HTML replacement. Restore the helper's exact prior HTML backup under the same
lock if necessary; do not clear caches or overwrite the live Caddyfile.

## Released runtime and static evidence

Application source: `f3addd923b8af8610a7cbbd405b9512300e1882c`. GitHub run
`42396611435` built and validated its linux/amd64 image and transport archive
`sha256:b59aa0ce482fb8dc3cb6579c21a4883771d3f8f939c8bcff923419283716a1b5`.
Its initial switch was rejected before mutation because inactive blue retained
long connections. The owner explicitly required preserving those connections.
Both host and in-container generation files proved blue standby through exact
read-only mounts; green was healthy/active. Independent operations review was GO.
The existing canonical `--prebuilt` coordinator resumed that same verified image
with the documented retention option scoped to one process, without a rebuild
or persistent configuration override. It selected the absent legacy `sub2api`
slot, preserved old connections and completed at2026-11-07 17:54:07UTC. Healthy
active image is `sub2api:auto-20261107-094041-f3addd92`; audits were0app5xx,
0appfatal and0Caddy5xx. Logs: `/var/log/sub2api-release/status-module-f3addd92-retained-drain`.
The original GitHub run remains failed at its initial guard; do not report it
as a successful workflow. The successful canonical recovery is recorded above.

The follow-up digest-bound Caddy receiver exposed an existing deployment bug:
it accepted only blue/green even though the coordinator supports legacy as a
third slot. It failed before mutation. This follow-up fixes explicit legacy
mapping in detection, projection, exact generated-path normalization and runtime
container checks. A legacy projection test failed before the fix; the full
receiver suite then passed, including mixed legacy/blue rejection. A separate
independent review approved the receiver fix and locked hash-verified installation.
The blue-only template,128MiB HTTP/WS limits, OCI revision, protected bind inode,
three-view consistency, maintenance lock and rollback checks remain strict.

Homepage publication succeeded under the shared maintenance lock with API180min
gate, immutable assets and atomic HTML replacement. Evidence directory:
`/var/log/sub2api-release/model-groups-20261107.GMEbns`.

| Artifact | SHA256 |
| --- | --- |
| HTML | `c2027456c4e99c495db8e2c3936c12e651f909b768640701f2d44120826b022e` |
| Module | `33da744cfff78291efef3b39cc704bae2343ef59c1097350349716df9a24db73` |
| CSS | `3eb06d27a42fb2efedcb9ebcc58e96b91446175c5e1a9b9324460ba988478178` |

Live origin smoke passes:12public groups across8platforms,18cells at10minutes,
180min, no selected-model field, no-store, insufficient metrics suppressed and
no availability enrichment in model plaza. Admin UI verifies12editable display
name fields and domestic rows. Groups6/10 remain enabled and29 disabled; no
production model selection or display name was changed. Release verification
used the existing canonical deployment canary; no extra manual paid test.

Receiver source is main commit `e162112235e4d27252852ddcc94ce555ee627cc8`,
SHA256 `172b12aa10a605e9b2f7e2021d8a6495445cf60bff7eabebaebf485842b45fe4`.
It was installed atomically under the shared lock after checking the exact
previous hash, preserving root:root0750, with a root-owned rollback copy in the
release log directory. The normal digest-bound Caddy receiver then completed
at2026-11-07 18:08:52UTC, active_slot=legacy, requested template digest
`8469457ee548ed85c3cb88f0a61359d32ee2902a6ef3919ce87acf2752857257`, final Caddyfile
SHA256 `2f397a4bc1c9538a321089e1105e97f0894258b3bd167e835ae8a3a891c7e536`.
Backup/record prefix:
`/opt/sub2api/backups/Caddyfile.before-caddy-config-release-20261107T180851Z-f3addd923b8a.N6IbHh`.

Retained blue now has the official drain monitor attached to active `sub2api`:
`sub2api-drain-sub2api-blue-status-module-f3addd92-retained-drain`.
It keeps existing connections and stops blue only after its normal zero-
connection and locked traffic/configuration checks. The regular monitor handles
old green. Root's stored retention option remains false. Final API smoke passes
after Caddy projection; real live Chrome confirms12cards,12admin display-name
inputs, model-plaza price/status separation, independent homepage `#status`, and
no320/390px horizontal overflow. Temporary viewport and task servers/uploads
were cleaned up. No production model/name configuration was written.
