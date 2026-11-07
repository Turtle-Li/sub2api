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

Release acceptance is recorded below after the exact-main canonical image
release and live origin/browser checks. Rollback uses the existing coordinated
image path and retains all configuration/history; never restore the database.
An older image without explicit selection would restore the earlier unwanted
catalog behavior, so prefer a compatible forward repair.
