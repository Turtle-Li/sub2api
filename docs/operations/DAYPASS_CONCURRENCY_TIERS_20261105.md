# Shop day-pass concurrency tiers

Owner scope: two concurrent requests for CNY6/24h (default), three for CNY8/24h;
maximum three in the shop. Manual New API issuance remains separately managed.
Same-IP protection, model policy, original trusted paid_at+86400, buyer-only
encrypted delivery and automatic native cleanup remain the established contracts.

## Design and acceptance

1. The native day-pass catalog exposes server-derived concurrency/price choices.
   The buyer submits only concurrency (omitted means the existing two). Reject
   other integers and reject concurrency on unrelated products. No buyer price.
2. Resolve the selected price and native action parameters before checkout,
   re-resolve against the locked catalog revision in the payment transaction,
   and persist that exact product/action snapshot. Catalog remains6/default2;
   the purchased snapshot uses price8/default_concurrency3 for tier three.
   In an order, the historical default_concurrency parameter means purchased
   concurrency. The shared native parser owns that meaning. No generic hook
   wire fields change and old action bytes are never rewritten.
   A repeated idempotency key
   with a different tier conflicts; old omitted selections remain two.
3. Native action execution validates the immutable amount/concurrency pair and
   displays the purchased concurrency in delivery and order details. Current
   catalog changes must not change retries, revocations or payment recovery.
4. Extend the existing private New API contract without changing its request
   JSON: price_fen600 identifies two,800 identifies three; all other amounts
   reject. This preserves old receipt digests. Create the concurrency option in
   the same transaction as the native grant. A replay never resets an admin
   override or extends expiry. Ready advertises supported concurrency tiers;
   new Sub2 checkout requires both before accepting new purchases.
5. Existing generic payment-action/outbox/integration architecture remains the
   extension point. Tier selection belongs to this native SKU, not arbitrary
   buyer-supplied hook parameters or arbitrary webhook execution.

## Execution DAG (owner: Codex)

- A: inspect pinned references/current contracts; obtain Claude design review.
- B after A: receiver validation/grant/tests and source offer; no DB migration.
- C after A: server checkout/snapshots/replay and focused integration tests.
- D after C: existing Vue checkout radio choices, total, payment limits and
  order summary; frontend component tests and desktop/mobile visual inspection.
- E after B/C/D: independent Claude diff review, scoped checks and deploy.

## Validation and release boundaries

Cover both tiers, invalid/tampered selection, unrelated products, revision race,
same-key different-tier conflict, same-tier replay after catalog edits, old
600-fen receipts and800-fen provision/replay/revoke on SQLite/MySQL/PostgreSQL.
No actual payment or fabricated PAID financial rows for acceptance.

Receiver must be deployed first, with public control paths still isolated and
same-host WorkBuddy/Tunnel/siblings untouched. Sub2 uses exact-main CI images
and its locked blue-green release process. Retain concurrency-aware code for
rollback from the first three-concurrency checkout (including unpaid orders);
never roll back a database or
an unaware native receipt processor. Record exact artifacts and acceptance.

Loaded logic-index blocks: none (this checkout has no logic index). Authority:
docs/SHOP_PAYMENT_ACTIONS.md and its fixed references, payment_shop.go,
shop_action_daypass.go, shop_action_bindings.go, and New API daypass_shop.go.

## Claude design review

Actual claude_code reviewer via CAO,2026-11-05, terminal e0d460a6,
session cao-daypass-tiers-20261105: approved the refined snapshot approach.
Required and incorporated: exact price/concurrency/duration pairing at enqueue
and Execute; re-apply selection after locked base-row load; same-key tier
conflict including the transaction-race replay; no native request JSON changes;
both native grant/cache writes use resolved tier; delivery states purchased
concurrency; Ready advertises exact2/3; concurrency-aware rollback floor.
shop_daypass.max_concurrency defaults3, may reduce2 to stop new3 orders.
Already issued checkout orders keep their original tier, server recovery and
cashier access; their trusted callbacks and original fulfillment remain valid.
Manual keys and paid-order cleanup/revoke never read this sales switch.

Claude implementation review: APPROVE after resolving two findings (MySQL KEY
predicate quoting in the receiver test and consistent existing-checkout recovery
under the sales switch). Deployment review also fixed source-footer verification,
missing-image/container recovery and retained compatible-image recovery floor.

Activation procedure: stage YAML max_concurrency2 under the canonical lock,
release exact main once, let the unaware old color drain naturally, verify every
running application revision is tier-aware, stage3 under the same lock, then
release the same exact main again to load static configuration. A tier-aware
max2 color may continue draining during phase two. The config helper preserves
uid/gid1000, mode0600 and all unrelated YAML bytes/semantics.

## Validation evidence

- Sub2 Go -race unit suites scoped to shop/payment-shop across service,
  handler and config: pass. Real PostgreSQL16 authored migrations/outbox,
  competing claims/lost replies for both tiers: pass.
- Frontend checkout selection, retry keys and changing payment limits, previous
  shop/order/feedback behavior, typecheck/build and touched ESLint: pass.
- Native SQLite/MySQL8.4/PostgreSQL16 matrix with -race/-shuffle: pass;
  pinned historical600fen JSON digest and all four focused Go packages: pass.
- Actual Claude design/implementation/deployment reviews: APPROVE, CAO
  session cao-daypass-tiers-20261105, terminal e0d460a6. Reviewer read only.
- Receiver live guard.5.1 image
  `sha256:79b7f759296fcf408d6f09772ee2373bc325a7273b601e097efe8521045d325b`;
  binary SHA256 `f4a9a5fd8a58a7ff7878b08cddc0cfdc1439535deff3f101161c82cae49d2f04`.
  Backup `/opt/new-api-daypass/backups/pre-tiers-guard5.1-20261105T115054Z`.
- Public complete source6997567bytes, SHA256
  `a87db1e1b40d0eaf96dc9866ddf58b223bc338b707ab78de8d77dd76c5d84a41`;
  public automation Ready remains404 with isolated boundary header.
- Live private Ready [2,3]; native synthetic receipts9017938794932/4933,
  token25/26, exact replay/original expiry/changed-price409 passed.
  Real overlapping SSE statuses2→[200,200,429],3→[200,200,200,429];
  second network403 for each. Exact revoke and subsequent issue410 passed.
  Both keys revoked, tombstones retained, zero Sub2 financial rows written.

## Production activation and checkout acceptance

Both phases completed on the sole production owner, SSH alias
`sub2api-aws-candidate`, under `/opt/sub2api` and the canonical maintenance lock.
GitHub Actions [42023347430](https://github.com/Turtle-Li/sub2api/actions/runs/42023347430)
succeeded for main `2248dd7f89c87ddc33e2b98dbc07c8e8a2b5cc71`.
Both phases reused that exact image; there was no server source build.

- Image `sub2api:auto-20261105-040956-2248dd7f`, Docker ID
  `sha256:c0c2243bcf17c31c48ed582873f07db6d3a01e0c25e20dbe91a26ab99e74dc10`.
  CI archive94140187bytes, SHA256
  `1772b572307fa7408a431fb8d1788ab8a84349b4c6b06dc1c8f5a53985694c0e`.
- Phase one retained max2; config backup
  `/opt/sub2api/staging/daypass-limit-20261105T115825Z`, release record
  `/var/log/sub2api-release/gha-20261105-040956-2248dd7f-1982537/`.
  The old unaware color exited after its natural drain before activation3.
- Phase two set max3 after checking every running application revision.
  Config backup `/opt/sub2api/staging/daypass-limit-20261105T121434Z`;
  canonical release `daypass-tiers-enable-20261105T121523Z` succeeded.
  Final Caddy selection is `sub2api-green:8080`, healthy, revision2248dd7f,
  zero restarts; blue exited. Public API health returned200.
  Config remains uid/gid1000:1000, mode0600, max_concurrency3.
- Reviewed config helper `/opt/sub2api/staging/shop-daypass-concurrency-limit.py`
  SHA256 `d4d8a18a1c3a0dc262e7687485cc84aab824eca02b40d1df9e9b4050a65b3a0c`;
  wrapper `.sh` SHA256
  `c7fe2f9a9d973bce055fe17eb394fb2bad581e772b18ede750b3208c6bd7e225`.

Real browser acceptance at `https://www.turtleligpt.com/shop/products/3`:
default2 displays CNY6; selecting3 displays CNY8 and the matching usage notice.
Checkout69 displayed CNY8 and its order detail persisted
`1 day / 3 concurrent / CNY8`. The test order was cancelled and remains unpaid;
no payment callback or PAID state was fabricated. Desktop and390x844 mobile
checks passed, with no horizontal overflow. Viewport override was reset and
the deliverable purchase page was restored to default2. Local evidence set
`daypass-concurrency-20261105` contains desktop/mobile captures; the cropped
`desktop-purchase.jpg` excludes private account information.

The first3-concurrency checkout now exists, so2248dd7f or later compatible
code is the Sub2 recovery floor even if new sales are reduced to max2. Use the
reviewed configuration helper and the canonical release process to load that
sales switch. Keep existing orders, payment/action snapshots and receiver
receipts; never restore an earlier database or an unaware binary. Receiver
recovery remains pinned to guard.5.1 as documented above.

## Owner-requested fresh unlimited-key live test

On2026-11-05T14:47:34Z, a fresh native-only synthetic receipt9201793890054
issued token27/internal user20/subscription19 with3 concurrent and15minutes
remaining validity. No Sub2 order, payment callback or PAID fact was created.
Four overlapping public HTTPS calls gave three complete200 SSE responses and
one429/`account_concurrency_exceeded`. Another public network returned
403/`account_ip_in_use` both while a stream was active and after completion.
Original-network continued calls succeeded: ten generations in52.24seconds,
565 prompt/1970 completion tokens,2535 recorded quota units. Wallet remained0,
key unlimited flag remained true, and native plan/subscription totals remained0
(native unlimited mode), subscription-only/no overflow. Used amount rose0→2535.
Thus usage is recorded without a finite key/subscription total cap.

Exact cleanup disabled token27, cancelled subscription19 and retained the
revoked receipt; replay410. No application/configuration change or deployment.
Advisory Level1 QA_PASS; native project report
`docs/live-unlimited-key-acceptance-20261105.md`, harness
`ops/unlimited-key-live-smoke.py`, privacy-safe log SHA256
`c35c8ac92f1d3044d2971806d042453056c7847bcba1846814cd5a72782b42a1`.
Expiry/15-minute network-release waits retain their separately recorded scope.
