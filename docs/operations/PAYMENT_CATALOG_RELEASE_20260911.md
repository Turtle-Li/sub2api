# Payment catalog and release — 2026-09-11

Task PAYMENT-CATALOG-20260911. Owner authorized publishing tested payment/order work and configuring real products. Baseline fork/main `2c94f286b97469ea77f1aecb6452b3286ae30126`, verified on both production nodes before work.

## Requirements and acceptance

- CAT-01: Publish the missing payment UI, currency/entitlement claim corrections, mobile method picker and Banner/recommendation configuration. Retain current unified payment, order snapshots, refund/recovery, invoice and Feishu behavior. Do not reintroduce obsolete invoice or sandbox drafts.
- CAT-02: Plus uses existing group 4, monthly CNY 120. 5X Pro uses group 12, monthly CNY 550. Quarterly prices are 324/1485 (10% off three months), annual 1152/5280 (20% off twelve months). Use existing 30/90/365-day period contracts. Existing same-group subscriptions can renew; preserve remaining duration and existing usage on renewal.
- CAT-03: Only an owned active, unexpired subscription in an active subscription group can buy one reset card. Current same-group for-sale monthly plan is the authoritative price source; reject absent/ambiguous sources. Price is monthly price / 3 rounded to two decimals: 40 and 183.33 credits. Purchase uses wallet balance, never extends membership, and grants expire with the current subscription. Existing use-card operation resets usage windows separately.
- CAT-04: Wallet debit, one-card grant and append-only purchase record commit atomically. A user-scoped UUID identifies a logical purchase; replay returns the same result, conflicting reuse fails. Validate expected plan/price before debit. Insufficient balance, expired ownership and closed payment reject without mutation.
- CAT-05: Recharge tiers 5/49/99/199/399/599 CNY grant base credits 1:1 plus bonuses 0/0/4/19/75/145. Mark 599 recommended. Quote exact credited totals; no concurrency bonus or invented token forecast.
- CAT-06: Owner will adjust metered rates later. This task must not modify existing group/account/model multipliers or user balances. The future 0.25-rate scenario gives 599/(599+145)*0.25≈0.20128 (owner subsequently rounded bonuses down to integers); do not advertise this as a live multiplier before that change.
- CAT-07: Configure actual products and enable customer checkout only after source gates, verified build, backups and both role-preserving deployments succeed. No synthetic paid orders, actual channel charge/refund, invoice issuance or bot/email message is authorized by the validation task.

## Architecture and task graph

Existing payment orders remain the external-payment ledger. Wallet reset-card purchases use a separate minimal audit table linked to subscription/reset grant. SubscriptionService owns eligibility and atomic debit/grant; handlers expose quote/purchase under authenticated subscription routes. A transaction locks user/subscription/price source and serializes duplicate operation keys. No third-party payment or background worker is added.

T1 root: integrate missing UI/banner on current main, retaining newer refund safety. T2 backend worker in isolated reset-card worktree: additive purchase storage, quote and atomic purchase API, focused tests. T3 root (parallel, separate worktree): ResetCardShop and API client, quote confirmation and stable retry key tests. T4 root: cherry-pick T2, integration tests and build. T5 independent QA/review: frozen combined snapshot; failures return to writer. T6 authorized release executor: publish exact main, GitHub build_only archive, digest/metadata checks, existing backup/isolated restore smoke, candidate activate + old-origin preserve-standby via lock-owning receivers. T7 executor: guarded catalog configuration, readback and browser smoke.

Release coordination uses the repository's manually dispatched GitHub workflow concurrency plus the installed receiver upload/release and maintenance locks. No separate queue backend is configured. Do not guess a new coordination service or bypass these existing locks.

## Rollback and verification boundary

Retain previous `2c94f286` image and additive financial/migration evidence. Disable new checkout/config if application rollback is needed; preserve role ownership (candidate active, old origin standby), existing orders and successful reset purchases. Production config writes require expected-old-value checks and a nonsecret before/after manifest. Do not restore old user wallet balances over subsequent legitimate usage.

Source verification includes payment/refund/invoice regressions, unit-tag handler tests, reset purchase concurrency/idempotency/atomic rollback, front-end type/lint/build and responsive rendering. Production smoke checks health, image revision, background roles, displayed products and safe configuration readback. Actual customer payment and tax issuance remain user-operated acceptance tests.

Knowledge candidate: no; project-specific release and catalog policy.

## Configuration limit decision

Fixed recharge allowlisting enforces the six amounts and therefore the 599 ceiling. Set MAX_RECHARGE_AMOUNT=0 because unified method presentation applies that setting to subscriptions too; setting 599 would disable annual/quarterly products above 599 in the UI. Arbitrary balance amounts still fail the fixed-tier check. Do not clear the preset list.

## Verification checkpoint (before final source freeze)

- Missing payment UI commits integrated on current production baseline; existing order, invoice, durable refund/recovery and Feishu tree retained.
- Root focused service/handler/admin payment tests passed. Initial frontend payment/admin regression 195 tests passed; later Settings preservation regression 43 tests and ResetCardShop retries/remount/quote-change 5 tests passed. Typecheck, lint and build passed before the final visual emphasis change.
- CUA actual components at desktop and 375px: correct paid prices/renewal/quote confirmation and no horizontal overflow. Latest integer bonuses replace the earlier fractional preview; final emphasis view requires recheck.
- Production backup `sub2api-db-backup-20260911-233344.tar.gz` completed; isolated restore smoke passed PostgreSQL schema_count=295 and Redis restore. No live wallet/order mutation was used for testing.
- Independent review found stored malformed preset preservation and extreme reset validity overflow; both are being corrected before release. Root also required post-commit billing-cache invalidation for wallet reset purchases.
- Owner subsequently requested stronger actual-credit/bonus emphasis using the existing card identity. Reference information hierarchy: https://cursor.com/pricing and https://claude.com/pricing. No provider plan content or implied partnership is copied.
