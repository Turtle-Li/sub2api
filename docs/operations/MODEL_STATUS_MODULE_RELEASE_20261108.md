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

Exact runtime source, workflow, static hashes and live smoke evidence are
recorded after the canonical deployment completes.
