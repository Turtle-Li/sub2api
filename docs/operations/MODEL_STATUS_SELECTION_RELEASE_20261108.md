# Explicit status group selection — 2026-11-08

Owner correction: only selected groups belong on the user status dashboard.
The public catalog is the admin candidate list, not the publication list.
Product authority: `docs/features/MODEL_AVAILABILITY_GROUPS.md`.

The inline editor now adds/removes public groups. Removing hides the public
card and pauses checks, retaining the owner's exact request model/display name.
Readding restores both with checks off. The additive `listed` JSON flag owns
publication; missing legacy flags inherit the old enabled selection. No
migration or config rewrite is needed; unchecked legacy rows disappear on
upgrade. Global/per-group scheduled-check switches control spending separately.

The request-model dropdown uses the existing group candidate endpoint's opt-in
available-only mode: bound schedulable accounts, exact group platform, existing
model support/allowlist, no wildcard/media or mapped-media IDs. Empty/error
discovery cannot fall back to platform defaults. It never probes providers.

Verification: scoped Go service/handler/admin/routes tests, Go build,103frontend
tests including scoped API forwarding, frontend production build with i18n and
TypeScript, and real PostgreSQL config round-trip passed. Real Chrome verified
add/select/rename/save, remove→card hidden, readd→choices retained/check off,
ordinary-user scope, desktop1440/mobile390 and no horizontal overflow. Detector
reported no findings. A fresh independent code reviewer substituted for the
unavailable named finish-reviewer, inspected screenshots/tests and returned
APPROVE without material findings. Local evidence: `.omo/status-editor-qa.md`
and `.omo/evidence/status-editor-code-review.md`; screenshots are not shipped assets.

Production preflight: fork/main/activate, healthy active legacy `sub2api`
revisionf3addd923, read-only generation mounts, one active background owner.
Blue340630184 is fenced standby; green is stopped. Existing connections drain
naturally. Preserve the existing root retained-drain default; if needed, use
only the documented audited one-process option with the canonical coordinator.
No production model/config selections are changed by this release, and static
homepage assets need no publication because they consume the same filtered API.

Release acceptance is recorded below from the exact-main canonical image
release and live origin/browser checks. Rollback uses the existing coordinated
image path and retains all configuration/history; never restore the database.
An older image without explicit selection would restore the earlier unwanted
catalog behavior, so prefer a compatible forward repair.

## Released and verified

Application source `2928991b64fdb1778382552ad868a8d4ea4383ad`, version0.2.21,
GitHub workflow [42403963771](https://github.com/Turtle-Li/sub2api/actions/runs/42403963771)
completed successfully at2026-11-07 19:10:41UTC. Exact loaded image:
`sub2api:auto-20261107-110940-2928991b`, compressed110029376bytes, archive
`sha256:abeb23704fc6a19f9d1c729d09fd997b8bcd715c02e61044ea2182815f207726`.
Canonical release evidence:
`/var/log/sub2api-release/gha-20261107-110940-2928991b-1686420`.
Audit counters app5xx/appfatal/Caddy5xx were all0. No manual paid QA request.

Before dispatch reached the host, the already-drained blue generation had0
connections. Its existing canonical monitor was sleeping in its retry window;
the operator verified its exact script, environment, active legacy expectation
and healthy source, then restarted only that monitor. Its own route, health,
zero-connection and shared-maintenance-lock checks retired blue normally.
No raw application stop, connection termination or retained-drain override was
used. The normal workflow then selected green, switched only new traffic, and
retained the legacy f3 generation in read-only standby with1established
connection at acceptance. New green is healthy with0restarts/OOM and is the
only active background owner. Official drain unit:
`sub2api-drain-sub2api-gha-20261107-110940-2928991b-1686420`.

Digest-bound Caddy projection succeeded19:10:27UTC with active_slot=green,
template8469457ee548ed85c3cb88f0a61359d32ee2902a6ef3919ce87acf2752857257,
final Caddyfilebb81db5a19648a277a0288c3f51abb31c0c81104bb94046e1fc31c979eae5baa.

Origin smoke and real Chrome agree: exactly groups6/10are published,29and all
other unselected groups are absent. Homepage has2status cards separate from
prices. Admin editor has2added rows, all10remaining public candidates including
domestic groups, and working scoped model discovery (11GPT options,0load
errors). No admin save was performed; save stayed disabled and checks6/10
remained on. Public DTO omits model identity, preserves180min/sample guards,
and returns no-store. Model plaza has no availability enrichment. Live mobile
viewport390×1000 renders2cards without horizontal overflow. Evidence:
`.omo/status-editor-live-admin.png`, `.omo/status-editor-live-public-mobile.png`
and `.omo/status-editor-live-runtime.log`.
