# Currency cutover validation and pending gates

Application candidate: `c6e125abb9ec1dddd223a1b470e01d1d64ce8ec9`, based on fork main `cd8932c5c`. This record is preparation evidence, **not a production migration completion record**.

## Completed validation

- Backend impacted unit suites: service, repository, handler tree, server tree and migrations; all 10 packages passed, no failed tests. Evidence: local `evidence/currency/backend-release-check.jsonl` (not committed).
- Frontend changed test files: 12 files, 140 tests passed. Production build, Vue type checking and i18n validation passed.
- Currency regression coverage includes USD/CNY authored cards, partial price overrides, token/cache/media prices, conversion exactly once, subscription restoration, subscription-group wallet fallback key quotas, billing idempotency fingerprints, display normalization and traffic admission fencing.
- Local PostgreSQL fixtures passed both explicit recharge policies (`recharge_factor=1` and `6.75`), conversion, preservation of subscription/discount/history fields, repeat rejection and rollback.
- Real backup `sub2api-db-backup-20260912-033913.tar.gz` passed archive/dump/Redis checksums, the standard isolated restore smoke, and the updated migration script's isolated restore/dry-run/apply/repeat-rejection/exact-wallet-rollback rehearsal. Rehearsal containers have no network or published ports. The real-backup rehearsal uses factor 6.75 solely as a test case.
- A protected retained backup copy and checksum are on `sub2api-db` under `/opt/sub2api-migration/currency-20260912/backups/`. Financial row manifests and secrets must not be committed.

## Review and execution gates still open

Claude completed two review rounds. The subscription-key fallback conversion defect was fixed; recharge multiplier and preset bonus handling are explicit in SQL; the runbook requires platform-quota dirty snapshots to be flushed before conversion. User-approved rough analytics remain outside wallet reconciliation evidence. The final frozen-code review exhausted its retries and terminated with upstream HTTP 503 (no available accounts); no final approval has been received. The CAO status was corroborated against the actual terminal error, not treated as a successful completed review.

The owner has confirmed **existing wallet balances ×6.75 into CNY**. Future recharge retail policy remains pending: a ¥100 payment can grant ¥675 credits to preserve old recharge purchasing power, or ¥100 credits for nominal CNY retail. The migration refuses an unspecified factor. If preserving old retail purchasing power, update any preset descriptions embedding old bonus amounts consistently with their converted numeric bonus.

No currency code release, production wallet/settings conversion, production cache deletion, admission pause or live debit canary has been performed by this task. Before execution, recheck current main and both deployed revisions, complete final Claude review, resolve retail policy, deploy one verified image through the documented role-preserving receivers, and follow `deploy/currency-migration/README.md`. Take a fresh backup while all writers are fenced; the earlier rehearsal backup is not the final recovery point.

Acceptance requires actual wallet, subscription and platform-quota debit checks against dedicated test fixtures before reopening traffic. Local tests and a database rehearsal do not prove live upstream debit behavior.
