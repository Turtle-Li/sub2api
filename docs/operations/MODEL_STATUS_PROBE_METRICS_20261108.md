# Scheduled checks fill quiet-group metrics — 2026-11-08

The owner requests ten-minute checks to populate card metrics without real
traffic. Real group requests remain the preferred source independently for
success and first-token latency. Missing metrics use explicitly labelled check
success, native first-token P50 or historical whole-check duration P50. Public
cards, authenticated cards and the embedded homepage share this contract.

Six completed rounds qualify success; six successful valid timings qualify each
P50 independently. All use the current model and three-hour window, with a
completed result within15minutes. Stale, disabled and insufficient probes remain
unknown. At most three attempts produce one final result; retries do not create
extra samples, while every attempt's tokens remain in the private consumption
report. Cache continues to use real dashboard usage and the already documented
best-complete-hour statistic. No extra warm-up requests or owner config changes.

Migration272 adds nullablefirst_token_ms. Native gateway receipts are persisted
only on validated success. Historical/old-writer rows stayNULL; total duration
is never mislabelled first token. The versioned public fallback DTO exposes only
aggregate timings/rates. Homepage immutablev7 preservesv6 for rollback.
Pinned upstream contract, intentional differences and source/test references:
`docs/features/MODEL_AVAILABILITY_GROUPS.md`.

Validation: scoped service/handler/routes unit checks and real PostgreSQL
repository tests pass, including legacyNULL and final-attempt timing.68frontend
feature tests and9static metric tests pass. Frontend production build, Go build,
publisher syntax and diff checks pass. Real Chrome1440/390px dark fixtures
confirm true-request priority, labelled legacy duration/nativeTTFT fallback,
disabled unknowns, search reflow and no horizontal overflow on both surfaces.

Rollback: use the canonical blue-green helper with the previous compatible
image under the shared maintenance lock. Leave additive migration272 in place;
old writers ignore it. Restore the exact pre-v7 homepage backup under the same
lock and retain immutablev6 assets. No paid claim, usage row or owner setting
needs deletion or reversal. Exact production release/live evidence follows
after canonical deployment completes.
