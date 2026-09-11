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

Existing payment orders remain the external-payment ledger. Wallet reset-card purchases use a separate minimal audit table linked to subscription/reset grant. SubscriptionService owns eligibility and atomic debit/grant; handlers expose quote/purchase under authenticated subscription routes. A transaction locks subscription/user/price source and serializes duplicate operation keys. No third-party payment or background worker is added.

T1 root: integrate missing UI/banner on current main, retaining newer refund safety. T2 backend worker in isolated reset-card worktree: additive purchase storage, quote and atomic purchase API, focused tests. T3 root (parallel, separate worktree): ResetCardShop and API client, quote confirmation and stable retry key tests. T4 root: cherry-pick T2, integration tests and build. T5 independent QA/review: frozen combined snapshot; failures return to writer. T6 authorized release executor: publish exact main, GitHub build_only archive, digest/metadata checks, existing backup/isolated restore smoke, candidate activate + old-origin preserve-standby via lock-owning receivers. T7 executor: guarded catalog configuration, readback and browser smoke.

Release coordination uses the repository's manually dispatched GitHub workflow concurrency plus the installed receiver upload/release and maintenance locks. No separate queue backend is configured. Do not guess a new coordination service or bypass these existing locks.

## Rollback and verification boundary

Retain previous `2c94f286` image and additive financial/migration evidence. Disable new checkout/config if application rollback is needed; preserve role ownership (candidate active, old origin standby), existing orders and successful reset purchases. Production config writes require expected-old-value checks and a nonsecret before/after manifest. Do not restore old user wallet balances over subsequent legitimate usage.

Source verification includes payment/refund/invoice regressions, unit-tag handler tests, reset purchase concurrency/idempotency/atomic rollback, front-end type/lint/build and responsive rendering. Production smoke checks health, image revision, background roles, displayed products and safe configuration readback. Actual customer payment and tax issuance remain user-operated acceptance tests.

Knowledge candidate: no; project-specific release and catalog policy.

## Configuration limit decision

Fixed recharge allowlisting enforces the six amounts and therefore the 599 ceiling. Set MAX_RECHARGE_AMOUNT=0 because unified method presentation applies that setting to subscriptions too; setting 599 would disable annual/quarterly products above 599 in the UI. Arbitrary balance amounts still fail the fixed-tier check. Do not clear the preset list.

## Source and live acceptance evidence

- PR10 main `a67f390c3960a3256ee71aef1652ec715ed9b026` is tree-identical to QA/review source `e8b6ce161da368eb2c6dd5af4aaba945782d2bad`. Full CI34623139891 passed; security34621489633 passed on the same dependency tree. The old UI branch's never-deployed balance-trigger optimization was omitted after CI caught its conflict with the current auth-cache contract. The existing regression was retained and passed with reset purchases (6.807s).
- Frontend203 tests, typecheck/lint/build and actual component desktop/375px checks passed. Real PostgreSQL reset-purchase race tests cover atomic rollback, available balance, idempotency and concurrency; the concurrent-renewal lock regression passed8.051s. Embedded settings expire after30 seconds; embedded web race suite passed3.392s.
- Independent review/QA verified malformed-preset preservation, expiry overflow guards, post-commit balance-cache invalidation, subscription-before-user lock order and integer-bonus catalog consistency.
- Production backup `/opt/sub2api-db-backups/sub2api-db-backup-20260911-233344.tar.gz`, SHA256 `e77da0da6443975f14ab3ae704271ad681a0274db54beb9528807bc6afd6fcfa`, passed isolated PostgreSQL/Redis restore (schema_count295).
- First build-only34624101437 archive SHA256 `d298dda914d576ccaddbbd38c2783f76f0729e0794eda3619ed9e7677a685470`,83761000bytes; actual manifest-referenced config, metadata and transport identity passed verification. Both a67f receivers completed with app5xx/fatal/Caddy5xx0. Candidate retained active, old origin retained standby; payment/Feishu agent IDs unchanged, role shims removed, old containers drained automatically.
- Guarded `payment-catalog-20260911/apply.sql` committed6 plans and13 settings. All selected settings equal the manifest; group4/6/12 remain rates1/.03/1. Chrome live checkout verified all six recharge amounts/bonuses,599 credited744, both unified payment methods and selectable5280 annual plan. No charge/refund, invoice or notification was created by validation.
- Chrome exposed literal JSON feature descriptions and zero daily-limit display. PR11's four-file display-only correction accepts canonical JSON arrays and legacy lines, omits inactive limits, labels positive limits as credits and uses a generic recommendation. Focused handler,15 card tests,typecheck,ESLint and full build passed; independent QA/review passed. Main `34d2b8d4d3ac747fb50dd092e5021229f619e846` matches reviewed `e00aed0f4` tree. No transaction/schema/catalog change is included.
- Visual hierarchy references: https://cursor.com/pricing and https://claude.com/pricing. The existing product-card identity was retained while credited balance and bonuses gained larger, distinct areas; no provider plan content or partnership claim is copied.

## Final deployment

Final corrective source `34d2b8d4d3ac747fb50dd092e5021229f619e846` is deployed to both nodes. Build-only34627099187 succeeded; archive SHA256 `b72bd4bd34571b03fa34cfd6273891df5ba2f64351f799e3d803ca9927f189a7`,83846702bytes. Metadata, manifest-referenced OCI config, linux/amd64/source/version/revision and transport hash all verified; each receiver loaded image `sha256:522ff516d8e6d036fe95cbe3b3d587e1111384e6ecaeb2ed231665bf1f5f615a`.

- Candidate: healthy blue, restart0/noOOM, accepting/background active; log `/var/log/sub2api-release/gha-20260912-013114-34d2b8d4-911746`.
- Old origin: healthy blue, restart0/noOOM, accepting/background standby; log `/var/log/sub2api-release/gha-20260912-013300-34d2b8d4-3399019`.
- Both receivers completed successfully, real-request checks passed, app5xx/fatal/Caddy5xx0. Existing payment/notification agents stayed unchanged and healthy; one-shot configs removed. Prior green generations remain managed by the automatic drain monitor.
- External Google Chrome final hard refresh verified monthly120/550, quarterly324/1485 and their discount display, corrected separate feature lines, positive quota credit units and absence of zero daily quota. Annual5280 was selectable with both methods. Recharge599 shows credited744 and bonus145. No CF action was needed; browser validation stopped before confirming payment. Existing account has administrator subscriptions, so successful paid-group reset quote/purchase and same-group renewal remain covered by tests, not falsely claimed as live financial execution.
- User-facing purchase URL: https://www.turtleligpt.com/purchase. Existing order/refund/invoice interfaces remain deployed; actual external charge, refund, invoice issuance and notification delivery were not repeated.

Rollback: disable customer checkout if required, then use the retained prior image with the same background roles through the canonical locked release workflow. Retain migration241, reset purchase/grant audit and order records; never restore historical balances over legitimate later activity. This record is documentation-only and does not change the runtime identity above.
