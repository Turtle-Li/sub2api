# DeepSeek unlimited shop subscription SKU

The owner approved the coordinated merge, release and catalog change on
2026-11-06. The production smoke uses disposable native receipts and unpaid
checkouts; it does not charge a customer or simulate a provider callback.

| SKU | Validity from payment | 2 concurrent | 3 concurrent |
| --- | --- | ---: | ---: |
| Day | 24 hours | CNY 6 | CNY 8 |
| Week | 168 hours | CNY 30 | CNY 40 |

The week costs five times the selected daily rate. The existing product ID,
guide URL, integration version, zero wallet, unlimited token total, IP policy
and concurrency rules are shared by both periods. The existing default is
one day and two concurrent requests. No schema or plan migration is needed.

## Catalog copy for the existing product

Update the existing product through **商城 → 商品配置**, keeping its ID,
base CNY 6 / 1 day, category, status and native binding. Review the existing
copy and keep any other current terms. Change the name to
**DeepSeek API 不限量订阅卡**. Suggested period-neutral summary:
“付款后自动发放独立 API Key。有效期内不限 Token 总量，周期和并发可在购买时选择。”
Remove daily-only price or duration claims from the body, or replace them with
“日卡 24 小时，周卡 7 天；价格以所选并发档位为准。有效期从付款确认时开始计算。”
The admin save creates a new product revision. Existing order titles and
entitlements remain the original snapshots.

## Order and receiver contract

- Buyer POST adds optional integer `duration_days` (1 or 7) alongside
  `concurrency` (2 or 3). Omission preserves the legacy defaults. Other shop
  products do not accept these selections. The server exposes the optional
  `weekly_price` for each available concurrency only on the configured native
  product while weekly sales are enabled.
- Reapply the selection while holding the catalog row and persist the price,
  duration and concurrency in the payment and delivery snapshots. Idempotency
  conflicts on a changed duration or concurrency. Sales switches apply only
  to new checkout. Existing paid/aftersales fences and the 24-hour outbox
  recovery deadline retain their existing semantics.
- Native receiver readiness adds `duration_days: [1, 7]`. Old receivers
  still work for daily checkout; weekly checkout requires advertised 7-day
  support. The original provision/revoke JSON and receipt digest are unchanged.
  The trusted paid amounts 600/800 fen select 86400 seconds, 3000/4000 fen
  select 604800 seconds; the first price of each pair selects two concurrent
  requests. Key, receipt and subscription must have the same absolute expiry.
- Pinned references reviewed: Wei-Shaw/sub2api
  `bdb42e22f81fcb633ff0a060961211dd2bcb515b`, `payment_fulfillment.go` and
  `payment_fulfillment_test.go` (trusted payment and repeated notification).
  QuantumNous/new-api `e835b1bfc43b565d625537830dbfb19328925134`,
  `model/{subscription,token}.go` and native subscription/token tests
  (native validity and unlimited quota). The private receipt and paid-clock
  override remain local extensions to the same transaction. Preserve LGPL
  and AGPL notices and provide complete matching New API source if deployed.

## Ordered release and recovery

1. After owner approval, verify the current native source/image and the active
   Sub2 release. Deploy the compatible receiver first using its own
   `README.deploy.md`, preserving the private Ready/provision/revoke boundary.
   Publish its complete matching source and verify both periods, exact replay,
   expiry and revocation without charging a buyer.
2. Deliver the exact reviewed main revision via the canonical Sub2 release
   with `shop_daypass.weekly_enabled: false`; allow older colors to drain.
   Verify the daily checkout still works. Stage and review the product copy
   change in the existing admin editor.
3. Under the documented maintenance lock, enable weekly sales and load that
   configuration only after every serving or draining binary is compatible.
   Save the reviewed name and copy on the same product in the admin editor.
   Verify the name, both weekly amounts and stored durations with unpaid
   checkout only, and cancel any disposable unpaid orders. Live purchase
   requires its own authorization.
4. To stop selling weekly, set `weekly_enabled: false` through the same
   config/release path. From the first weekly checkout, retain a weekly-aware
   Sub2 release and receiver for recovery. Keep existing paid times, order
   snapshots, receipts and revocation tombstones; never restore an older
   database to undo a release.

`deploy/shop-daypass-weekly-sales.py` stages only this YAML scalar. Run the
reviewed copy as root with Python `-I` and the inherited canonical maintenance
lock descriptor 8, supplying the exact active OCI revision, the expected
boolean and the new boolean. The helper checks the live Caddy target and
all running app revisions before enablement, backs up the previous YAML,
and preserves unrelated settings. Use the canonical prebuilt release helper
to reload the exact already-verified image after staging the switch.

## Local validation

- Sub2 Shop Go race tests and real PostgreSQL claim/fulfillment integration
  passed. All four prices, locked catalog revalidation, changed-duration
  idempotency conflicts, old pending order recovery and delivery after sales
  switches change are covered. HTTP rejects unrecognized periods and prices
  supplied by the buyer. The gateway client rejects a 24-hour grant response
  for a week and allows daily checkout against the prior receiver.
- New API race tests passed on SQLite, MySQL 8.4 and PostgreSQL 16, including
  legacy receipt digest, exact token/subscription expiry, replay at the end
  of the term, changed binding, and revoke.
- 68 Shop frontend tests, touched ESLint and the frontend production build
  passed. Local Chromium checks at 1440px light and 390px dark verified the
  default, both weekly prices, payment method availability, and the CNY 40
  checkout with no horizontal overflow. Browser checkout used fixture data;
  there were no production payments or catalog changes.
