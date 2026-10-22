# Account monitoring switch

Each ModelTrace account card has an independent monitoring switch. Turning it
off preserves models, latest results, history and alert cooldown state. It does
not change account scheduling or the global automatic-monitoring switch.
Re-enabling keeps the existing configuration; it does not immediately submit a
probe. Existing monitors without an `enabled` field remain enabled.

The existing admin account configuration PUT accepts optional `enabled` and
`models` fields. Omitted models are preserved; an explicit empty array clears
them. Model-only updates preserve the switch. A switch-only request requires an
existing monitor. The state remains in `extra.modeltrace`; no migration is needed.

Disabled accounts are skipped by scheduled cycles and rejected by manual probe
submission and execution. Queued jobs recheck state after acquiring their slot.
An already dispatched upstream request may finish, but subsequent samples check
the stored switch before dispatch. Late results preserve the disabled state and
cannot trigger mismatch notifications or scheduling actions while it is off.

The fixed integration revision and intentional local differences remain those in
`MODELTRACE_UPSTREAM.md`. The pinned service, frontend and service tests were
inspected locally at `3fc21a61ebd18ac3508e3f8f9cfc6d064c4884d2` before this
extension. No classifier, bank, attribution or license changes are included.

Acceptance: pause/resume retains configuration and history; disabled monitors
remain listed; manual, queued and scheduled probes honor the switch; saving a
model list cannot silently re-enable monitoring; failed UI saves retain the
previous displayed state. Local synthetic-account validation never sends a
production probe or notification.

Implementation sequence: service persistence/admission checks, existing Vue/API
controls and translations, targeted service/view tests, then local real-browser
interaction checks. Root owns all changed files and final verification. Rollback
of this unreleased change requires reverting its code; an older binary ignores
the saved switch, so disabling global monitoring is necessary before any future
production rollback that must preserve paused accounts.

## Verification

Status: IMPLEMENTATION_READY. Baseline: `f05263c9f` from `fork/main`.
Branch: `codex/modeltrace-account-toggle`. The owner authorized commit, main
integration and production deployment on 2026-10-22 after local validation.
The release outcome is recorded below once verified.

- ModelTrace service, admin handler and route tests passed with
  `go test -race -tags unit -shuffle=on -count=1`.
- 13 view tests and 3 locale completeness tests passed. Type checking,
  changed-file ESLint, production frontend build and `git diff --check` passed.
- The actual Vue page and Gin configuration handler were exercised against
  synthetic local accounts. Keyboard toggles, disabled manual checks, reload
  persistence and resume passed at 1440px desktop and 390px phone widths in
  light and dark mode, without page overflow after responsive layout settled.
  Screenshots were visually inspected. Evidence:
  `.omo/evidence/toggle/results.json` and paired screenshots in that directory.
- Existing Browserslist age, Node localStorage and mixed-import build warnings
  remain. Live provider probes and production behavior were not exercised.
- Review was performed by the root agent; independent subagent review was not
  completed because the installed role hook rejected the requested worker role.
  No acceptance or release approval is claimed.

Knowledge candidate: no; this is a project-local monitoring contract documented
in `MODELTRACE.md`.

## Production release

Deployed on 2026-10-22 at 18:35 CST through the canonical production workflow.
The implementation commit `7c34b50abb400cb66fc0bb554dc15b7ba1a07879` was
fast-forwarded to `Turtle-Li/sub2api` main. Workflow
https://github.com/Turtle-Li/sub2api/actions/runs/39762963708 succeeded with
`deployment_target=aws-candidate`.

- Target: SSH alias `sub2api-aws-candidate`, application root `/opt/sub2api`.
- Active slot: `sub2api-green`; image `sub2api:auto-20261022-033505-7c34b50a`.
- Runtime image ID: `sha256:8bc24799017e2340232364ce4dc73f5572d2eb9a9d798457558c8afa680068ec`.
- Release evidence: `/var/log/sub2api-release/gha-20261022-033505-7c34b50a-709671`.
- Real-request gate: models HTTP 200 and Responses HTTP 200 for `gpt-5.6-sol`.
- Post-release: exact source revision verified, container healthy, zero restarts
  and no OOM; traffic accepting and background active. Liveness, readiness and
  refund rollback readiness passed; no panic/fatal entries in the inspected logs.
- Public API health returned 200. The signed-in Chrome monitoring page was
  refreshed and displayed all four existing accounts with enabled switches.
  Production account settings were not changed during verification. A separate
  Python HTTP client received 403 from the www edge; Chrome access succeeded.
- Previous blue slot remained running under the controlled drain process at
  verification time; no manual stop was performed. Release transaction markers
  were absent after successful completion.

Rollback must use the documented production workflow and preserve draining.
The previous image predates account switches, so pause global monitoring before
such a rollback if any account-level pauses must remain effective.
