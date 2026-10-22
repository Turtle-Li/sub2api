# ModelTrace account checks

Task: MODELTRACE-20261013 (feature). Source baseline: Turtle-Li/sub2api
`57d741cfaa61bc270e25e71fcd9c887ad6eea30d`.

## Requirements and acceptance

- MT-1: An administrator can add individual OpenAI GPT accounts and select
  models, manually run fingerprint checks, and compare attribution probabilities
  and bounded recent history across accounts.
- MT-2: Every challenge uses the selected account's authentication, model
  mapping, proxy, outbound identity and TLS profile. No scheduler fallback to
  another account and no credentials sent to a ModelTrace website.
- MT-3: Errors and insufficient samples are reported separately from model
  attribution. Optional mismatch notification and scheduling pause are governed
  by `MODELTRACE_ALERTS_20261021.md`; both default to off. Probes do not repair
  turn states, change provider availability, or debit customer balances.
- MT-4: Periodic checks are opt-in (disabled by default). Manual tasks survive
  the submitting browser request and have bounded concurrency and execution time.
- MT-5: Only administrators can use the endpoints/page. Scores describe
  statistical fingerprint similarity, not verified model identity or a direct
  capability/quality measurement.

## Fixed upstream references

- Integration: https://github.com/ZvonimirSun/sub2api, branch `custom`, revision
  `3fc21a61ebd18ac3508e3f8f9cfc6d064c4884d2`.
  Sources: `backend/internal/pkg/modeltrace/`,
  `backend/internal/service/modeltrace_service.go`,
  `backend/internal/service/modeltrace_usage.go`,
  `backend/internal/handler/admin/modeltrace_handler.go`,
  `frontend/src/views/admin/ModelTraceView.vue`.
  Tests: package `modeltrace_test.go` and `testdata/python_parity.json`,
  `modeltrace_service_test.go`, `account_test_modeltrace_test.go`,
  `ModelTraceView.spec.ts`.
- Original algorithm: https://github.com/xqy2006/ModelTrace, revision
  `6cd4a8ff0f3f4e49d6d6969dd2fbadfa1569fde2` (integration's parity reference).
  Verify fingerprint implementation, embedded bank and license before edits.
  Adopt the challenge generation, integer parsing, robust statistical classifier
  and fixed fingerprint bank; do not replace them with invented quality scores.

## Design and task DAG

No usable project logic index was present: Loaded blocks: none;
Index health: fallback-search. Authority: existing AccountTestService,
HTTPUpstream transport profiles, AccountRepository.UpdateExtra and admin route
middleware. Existing turn-state probes remain a separate path.

1. MT-T1 (root, read-only): inspect pinned integration and original algorithm,
   tests and licensing; decide reuse seams and local differences.
2. MT-T2 (root; depends T1): port classifier, service, account-test observation
   context, per-account transport and admin endpoints. Use existing account
   `extra.modeltrace` and settings storage; no database migration.
3. MT-T3 (root; depends T2): port existing Vue administration screen, API,
   navigation and both locales. Reuse existing UI primitives.
4. MT-T4 (independent QA/Review; depends T2/T3): classifier parity, exact-account
   requests, mapping/prompt/effort, error isolation, cancellation, task limits,
   defaults, history/config preservation, admin routing, UI and build checks.

The root owns integration and final verification. Stop implementation when
MT-1..5 and scoped checks pass; route defects back to the writer. Rollback:
remove this additive feature from an unreleased source candidate; retained
`extra.modeltrace`/settings are inert. No production operations are authorized
by local implementation.

## Intentional local differences

- Limit new monitoring to ordinary OpenAI OAuth/API-key accounts. Other
  platforms, synthetic accounts, credential shadows and Agent Identity are
  outside this request.
- Require an account-supported text model and reject mappings to common
  non-GPT provider families before saving a monitor. Manual probe model IDs
  travel in a query parameter so compatible IDs containing `/` remain
  addressable.
- Keep request token/timing details in ModelTrace history rather than adding
  upstream's system-probe usage-log schema and changing customer usage queries.
- Bound concurrent probes and task duration; incomplete probe sets are errors.
  A probe stops at the first 400/401/402/403/404/422/429 instead of spending
  its retry budget, and scheduled checks skip accounts inside a rate-limit,
  temporary-unschedulable or overload window.
- `extra.modeltrace` is server-owned: account edits keep the stored value,
  account DTOs omit it, and duplicates drop it. Saving monitoring data does not
  rebuild the scheduler; the optional mismatch pause uses the existing
  `SetSchedulable(false)` path. GPT-4-era mapped models are probed without a
  reasoning effort.
- Do not reproduce upstream's removal of probe deadlines.
- Observational probes bypass the optional OpenAI plugin transport because its
  current protocol has no TLS-profile or probe-pool fields. They use the
  account-aware HTTPUpstream path so configured proxy/TLS isolation is
  preserved; a plugin-only OAuth transport may surface a probe error instead
  of producing an attribution.

## Use and limitations

Each account card also has an independent monitoring switch. Turning it off
retains models and history, blocks manual/queued checks and skips scheduled
checks. Requests already dispatched may finish, but subsequent samples stop;
late results cannot trigger mismatch actions while paused. Re-enable the same
account to reuse its configuration. Existing monitors default to enabled. This
switch is stored in `extra.modeltrace.enabled` and does not change business-call
scheduling. Config updates preserve omitted fields; explicit `models: []`
still clears the model list. No database migration is required.

Admin navigation → ModelTrace → add an account → configure GPT model names →
run a check. Each default check requests three valid challenge samples; the
upstream retry ceiling is three additional attempts. These requests consume
the selected upstream account's quota. The periodic switch affects scheduled
checks only. History retains the latest check plus five previous checks per
configured account/model.

Use the same requested model and reasoning effort when comparing accounts.
An attribution to another model is a signal to repeat and inspect; low sample
counts, unsupported bank candidates and closely related model fingerprints
limit interpretation. Global parameters can enable Feishu notification and/or
pause new business calls through the anomalous account. Exact matches and
configured allowed relations pass; `gpt-6.1-sol` → `gpt-6-astra` is allowed by
default. Failed probes, unsupported detected fingerprints and expected models
without bank coverage or an explicit allowed relation do not trigger actions.
Pause applies to the entire account, keeps monitoring available, and requires
manual recovery in Accounts; a later matching probe does not resume traffic.
See `MODELTRACE_ALERTS_20261021.md` for configuration and delivery semantics.

Submission and deployment are separate from implementation. No live account
probe or production deployment is part of local validation.
