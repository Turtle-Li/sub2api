# ModelTrace mismatch actions

Date: 2026-10-21. Source baseline: `0251c1c198a61af0740c5a1a465a1f4c0ae8b357`.
Local implementation; submission, production deployment and live provider or
Feishu tests are outside this validation.

## Behavior

Every completed probe records its mapped expected model, a verdict and action
outcomes alongside the existing bounded history. Matching model IDs pass.
Explicit allowed relations also pass; `gpt-6.1-sol` → `gpt-6-astra` is the
default relation. A `gpt-5.6-luna` result for that expected model is a mismatch.
An actual request for `gpt-5.6-luna` that detects itself passes.

Probe failures, incomplete samples, unknown detected fingerprints, unsupported
accounts, changed model mapping and expected models missing both bank coverage
and an allowed relation cannot trigger actions. The expected model is frozen
before probing and compared with the current account mapping before acting.
Removed monitors/models discard late results. No fuzzy aliases or invented
capability scores are used.

An opted-in mismatch can send a Feishu message, pause account scheduling, or
both. A complete successful probe is enough; there is no extra confirmation
count or confidence threshold. Operators should choose policy with the bank's
closed-set statistical limits in mind. Manual probes use the same policy even
when automatic monitoring is off.

Pause uses `AccountRepository.SetSchedulable(false)`, which updates the existing
scheduler snapshot/outbox and database recheck boundary. It stops new business
calls for every model on that account; it does not cancel active streams or
stop ModelTrace rechecks. Restore scheduling manually in Accounts after review.
A matching probe clears notification deduplication state, but never resumes
business traffic automatically.

## Global parameters

Stored in the existing ModelTrace settings JSON; no migration or credential
field is added. Existing probe count, interval, effort and cron settings retain
their behavior.

| Field | Default | Contract |
| --- | --- | --- |
| `notify_on_mismatch` | `false` | Send through the existing Vault-backed Feishu text transport. |
| `pause_on_mismatch` | `false` | Pause the anomalous account's new business calls. |
| `notification_cooldown_minutes` | `60` | 1–1440 minutes for the same account/model and expected/detected pair; omitted/zero uses 60. |
| `allowed_detections` | `{"gpt-6.1-sol":["gpt-6-astra"]}` | At most 50 exact expected-model rules; each has 1–10 known bank fingerprints. Trimmed and case-normalized; no wildcards. An explicit empty object clears all extra relations. |

The panel edits allowed relations as one `expected = detected, detected` line
per model. Duplicate or malformed lines block saving. Missing bank coverage is
shown as “缺少参考关系”; configure a justified relation before enabling actions
for an expected model absent from the bank. The original bank and parity
fixture remain pinned as documented in `MODELTRACE_UPSTREAM.md`.

## Persistence and notifications

The result is saved before any external action. If this save fails, actions
do not run. Action outcomes are saved separately, including pause success,
notification sent/failed/cooldown and a bounded generic action error.
Messages contain numeric account ID, expected/detected model, attribution
probability, time and action outcome; no account name, credentials, prompt,
provider response or webhook URL is included.

Cooldown state is persisted per monitored model only after successful delivery.
Failed delivery retries on the next mismatch probe; a repeated mismatch during
cooldown still enforces the pause. A matching result clears that state, so a
later incident can notify immediately. An inconclusive probe does not clear it.
No retry timer is introduced: another probe is required.

The transport and outcome write are bounded independently of the browser
request. There is no distributed delivery transaction or new outbox: a process
failure after sending but before persisting the outcome can produce a duplicate
notification. The existing ModelTrace account locks are process-local; this
extension retains the project's single scheduler process assumption.

## Validation

- Pinned original/integration sources, bank hash, classifier parity and license
  obligations inspected before extension; upstream check recorded separately.
- Service tests cover the Sol/Astra exception, Luna mismatch, exact Luna match,
  unknown/error cases, each action combination, persisted cooldown, failed-send
  retry, recovery, pause failure, save failure, removed monitors and mapping
  changes. A unit-tagged full probe uses synthetic SSE through the actual
  account test path and classifier.
- Real Gin HTTP global-configuration PUT/GET tests cover persistence and invalid
  input. Existing ModelTrace admin route authorization tests are included.
- Targeted service/admin/routes tests pass with `-race -tags unit -shuffle=on`;
  `go vet`, classifier tests and backend server build pass.
- Frontend view/locale tests (14), type check, changed-file ESLint and production
  build pass. Existing bundler size and mixed-import warnings remain.

- Browser validation passed against the actual Vue page and Gin settings
  handler with synthetic localhost accounts: 1440×900, 1920×1080 and iPhone
  390×844 (DPR 3, mobile UA and touch), each in light/dark mode. List,
  settings, history, horizontal scrolling and expanded request details were
  captured. No page overflow or captured runtime exceptions occurred.
- English at 1024 pixels fits the header after shortening its description;
  the measured gap between title and controls is 8 pixels. New history status
  cells retain readable width: rows measure 45 pixels on desktop and 59 on
  phone. Global parameters round-trip through the real HTTP handler; duplicate
  rules show an error without closing the dialog. Escape closes dialogs in
  normal and reduced-motion modes.
- Independent final code review: CLEAR / APPROVE, no remaining findings.
  The reviewer independently ran policy/probe/HTTP tests with race detection
  and shuffled order, and reviewed the final presentation changes.

Task-local evidence: `.omo/evidence/modeltrace/results.json`, paired PNGs and
accessibility snapshots in that directory, and
`.omo/evidence/modeltrace-alerts-code-review.md`. Preview fixtures and captures
use only synthetic localhost accounts and never load production credentials or
send a webhook. Existing bundler/browser-data warnings are not expanded into
unrelated dependency updates. Production deployment and live delivery remain
unverified.
