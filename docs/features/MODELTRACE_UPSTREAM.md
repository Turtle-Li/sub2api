# ModelTrace upstream evidence

This document records the fixed upstream sources inspected for the ModelTrace
integration. It is research for the target port; it does not authorize copying
the upstream account, usage, or platform behavior without the differences in
`MODELTRACE.md`.

## Fixed sources

- Integration branch: `ZvonimirSun/sub2api`, remote branch `custom`, revision
  `3fc21a61ebd18ac3508e3f8f9cfc6d064c4884d2`.
  [Branch](https://github.com/ZvonimirSun/sub2api/tree/custom)
- Feature commit: `735f0ae51a5634a5c2f4203cb8f797f0b111ee28`,
  `feat(modeltrace): integrate and harden monitoring`.
- Hardening commit: `4744a514355a6359539b86639613b0e751529b63`,
  `fix(modeltrace): remove probe deadline and correct dark theme colors`.
- Latest UI follow-up: `3fc21a61ebd18ac3508e3f8f9cfc6d064c4884d2`,
  `fix(modeltrace): expand request details below history rows`.
- Classifier source: `xqy2006/ModelTrace`, `main` at
  `6cd4a8ff0f3f4e49d6d6969dd2fbadfa1569fde2`. The SHA was checked both from
  the cloned repository and `git ls-remote`.
  [Repository](https://github.com/xqy2006/ModelTrace/tree/6cd4a8ff0f3f4e49d6d6969dd2fbadfa1569fde2)
- Classifier license: MIT, Copyright (c) 2026 xqy2006, from the upstream
  `LICENSE` file. The integration embeds the upstream bank and should retain
  this attribution and license notice.
- Embedded bank integrity: SHA-256
  `f267d0513aa06981d024ec5394746fceddf44e4e8a75fa6bf8304255f4b32d6d`.
  The hash matches the integration bank and both upstream copies under
  `data/unified_bank.json` and
  `codex-plugin/modeltrace-guard/assets/unified_bank.json`.

The bank was built at `2026-10-09T06:24:27.469312+00:00` and contains 18
listed GPT and Claude models, with `recommended_queries=3`, a minimum of 80
valid numbers, and calibration entries for one, two, and three valid samples.

## Classifier contract

The Go port is in
`backend/internal/pkg/modeltrace/modeltrace.go`; `unified_bank.json` is
embedded beside it. The parity fixture and test are
`backend/internal/pkg/modeltrace/testdata/python_parity.json` and
`modeltrace_test.go`.

- `GenerateChallenges(count)` creates three challenges by default. Challenge
  lengths are sampled without replacement from 292 through 332, using a
  cryptographic random source. Each prompt asks the model to emit integers in
  the inclusive range 1 through 355 with clear separators and includes a
  unique probe ID. The prompts prohibit tools and counting/rule shortcuts.
- `ParseNumbers` extracts Unicode decimal digit runs, splits on alphabetic
  separators, selects the longest run, and retains only values from 1 through
  355. Values outside that range are ignored.
- Per completion, the classifier combines a smoothed nuisance-adjusted
  Hellinger feature score with an ordered-block score. The ordered block has
  weight `0.25`; the remaining marginal score has weight `0.75`. Scores are
  averaged across valid completions.
- A completion is valid when it has at least
  `max(80, ceil(expected_count * 0.55))` retained numbers. If no completion
  meets that threshold, analysis returns
  `modeltrace: no valid completion outputs met number threshold`.
- The averaged model scores are calibrated with the bank beta for one, two, or
  three valid completions (fallback beta 12), then converted to a uniform-prior
  softmax. Family probabilities are sums of member probabilities. Results are
  sorted by probability and include the top model, family, score, and
  probability.
- This is a closed-set statistical attribution result. A probability is not a
  capability score, a proof of identity, or a quality guarantee. An unlisted
  model is assigned to the nearest listed candidate. System prompts, wrappers,
  transport changes, and model drift can change the distribution.

The parity test passes at the pinned integration revision with:

```text
cd backend && GOCACHE=/tmp/sub2api-modeltrace-gocache go test ./internal/pkg/modeltrace
ok   .../backend/internal/pkg/modeltrace
```

The fixture assertion includes `claude-opus-5-5` as the top result with a
probability near `0.880595` and score near `1.724617`; the test also checks
parser output and all model scores/probabilities to `1e-10`.

## Upstream integration behavior

The main service is
`backend/internal/service/modeltrace_service.go`. The account binding is
through `AccountTestService.RunModelTraceProbe`, implemented in
`backend/internal/service/account_test_service.go`:

- The probe sets an observation-only context flag, calls the normal per-account
  test pipeline, and parses captured SSE `content` events into the completion
  text. It therefore reuses account-specific authorization, mapped models,
  headers, proxy, TLS/client fingerprint, and provider endpoint selection.
- OAuth and API-key OpenAI paths use the account's mapped model and configured
  endpoint. The upstream branch also routes other providers, credential
  shadows, and Agent Identity; those are broader than the target requirement.
- Observation mode suppresses availability side effects such as setting an
  account error, marking rate limiting, clearing credentials, or updating
  provider-specific account extras. The corresponding tests are in
  `backend/internal/service/account_test_modeltrace_test.go`.
- A configured `(account ID, model ID)` has one in-memory account lock and one
  queued/running task. Accepted asynchronous tasks survive the initiating HTTP
  request cancellation and stop on service shutdown.
- The scheduler scans explicitly monitored active accounts, runs models
  sequentially within an account, and runs different accounts concurrently.
  The account configuration and the latest record are stored as JSON under
  `account.Extra["modeltrace"]`; history is capped at five records per model.

The admin HTTP surface is
`backend/internal/handler/admin/modeltrace_handler.go`, registered in
`backend/internal/server/routes/admin.go`. It exposes monitor listing, global
configuration, per-account model configuration, removal, and an asynchronous
manual run. The frontend API and view are
`frontend/src/api/admin/modeltrace.ts` and
`frontend/src/views/admin/ModelTraceView.vue`. Error fields are interpolated
by Vue (escaped), including request-level endpoint, timing, token, and error
details.

## Error and security behavior

The upstream service records every attempted request in the model's history,
including failed requests and invalid samples. It records the valid sample
count, endpoint path, latency, first-token latency, input/output token counts,
and error text. A probe with at least one valid completion can still be
classified even when it did not reach the requested number of valid
completions. The upstream hardening commit removed the probe deadline.

The request error can include a raw upstream response body (for example,
`API returned %d: %s`) before being persisted in
`Extra["modeltrace"]` and returned to the admin UI. A port should sanitize,
redact, and bound stored/displayed errors so response bodies cannot carry
tokens, cookies, authorization material, or oversized provider diagnostics.
Do not persist prompt text or raw SSE payloads.

The upstream branch also adds `account_probe_usage.go` and migration
`backend/migrations/243_system_probe_usage.sql`. That migration relaxes
`usage_logs.user_id` and `api_key_id` nullability and records internal probe
usage against the account/quota/outbox. This is a separate accounting contract;
the target intentionally does not port it unless product requirements change.

## Port guidance and known differences

The smallest target seam is the existing direct per-account test path: expose
an observation-only OpenAI OAuth/API-key call that returns the captured
completion text, then keep the classifier and monitor orchestration behind a
dedicated service. Preserve the classifier bank, parity fixture, and MIT
notice as one pinned unit.

The target plan in `MODELTRACE.md` intentionally differs from the upstream
branch in these ways:

- accept ordinary OpenAI OAuth and API-key accounts only;
- enforce bounded concurrency and a probe deadline (the upstream hardening
  commit removed its deadline);
- treat an incomplete valid set as an error rather than publishing a partial
  attribution;
- keep request/token details in feature history without introducing the
  system-probe usage migration.

Additional validation is advisable at the target boundary: ensure a monitored
model is an allowed OpenAI/GPT mapping, map service errors to appropriate
client status codes instead of returning every failure as an internal error,
and make the bank revision/hash visible in diagnostics so stale calibration
data can be identified. The upstream bank is a closed-set artifact and should
be refreshed only with a new pinned source and parity evidence.
