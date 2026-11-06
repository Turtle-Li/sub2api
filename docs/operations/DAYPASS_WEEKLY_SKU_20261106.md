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
3. Save and verify the reviewed name and copy on the same product in the admin
   editor. Under the documented maintenance lock, enable weekly sales and load
   that configuration only after every serving or draining binary is compatible.
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

## Production evidence, 2026-11-06

- Receiver deployed first: New API `v1.0.0-rc.47-daypass-guard.5.2`. Private
  Ready returned both periods and tiers. Four disposable receiver-only
  receipts verified exact 24/168-hour key and subscription deadlines, replay,
  mismatched period rejection and revoke. Public control stayed 404/isolated.
  The public corresponding source matched the receiver manifest.
- Code merged into owner fork `main` at
  `32adb35ba3da318df06db17315232b6e01285c3b`. The canonical GitHub Actions
  [run 42175986610](https://github.com/Turtle-Li/sub2api/actions/runs/42175986610)
  succeeded. Image `sub2api:auto-20261105-225106-32adb35b`,
  ID `sha256:90055a83236698d9087c877358719d62e191cd64ca6721ea20201b6cb7898ec6`.
  Archive: 94190975 bytes, SHA256
  `440dfb53649a6f4394c99dfd3f0a8b50df73a9e0f0c41abd363ed4a3c699cc1d`.
- Phase one `gha-20261105-225106-32adb35b-3279882` kept weekly disabled.
  Daily checkout displayed CNY 6/8. The previous revision exited normally.
  The admin editor saved the period-neutral name and description on product 3;
  its base price, duration and automatic action remained intact.
- Phase two `daypass-weekly-enable-20261106T065703Z` reused the same image
  through the locked prebuilt path with the configured API health URL.
  Config helper SHA256
  `da912f40a5c3c176a20239424c3cf5d7825a9d76ff33cd8935bf86a4b9894d69`,
  backup `/opt/sub2api/staging/daypass-weekly-config-nqta477g`.
  Caddy selected healthy `sub2api-blue` at the exact revision with zero
  restarts; green exited normally. Release audit app_5xx/app_fatal/caddy_5xx
  were zero. Both public health checks passed.
- Real browser: [product 3](https://www.turtleligpt.com/shop/products/3)
  displayed **DeepSeek API 不限量订阅卡**, 24 hours by default, and
  an optional 7 days. Orders 70 and 71 stored 7 days, respectively
  2 concurrent/CNY 30 and 3 concurrent/CNY 40. Both show cancelled,
  unpaid and delivery not started. No payment or callback was simulated.
  Local screenshot: `artifacts/ds-weekly-20261106/live-weekly-picker.jpg`.

The first weekly checkouts now exist. Keep revision 32adb35b or a compatible
later Sub2 binary, plus guard.5.2 or later compatible receiver, when stopping
new weekly sales or recovering. Preserve the live databases and receipts.
