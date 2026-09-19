# Administrator refund clarification — 2026-09-19

Reviewed source: `e4913ec52a3645e5c81e213ff81f7c3ab5f5fac8`.
Deployed main: `3ad4a97a6ea2eba0748f3b3b21e824160c8a9051` (identical tree).
Base: `9c789f0b7082f08e7c95900a79705f5a4429fd8e`.
Status: owner-authorized release completed at 2026-09-19 02:00:26 CST.

## Accepted behavior

Administrators may select a subscription cash refund within the server's
unused-time ceiling. The server previews and revalidates the selected entitlement
effect; no browser-calculated duration is accepted. Omitted amounts retain the
existing maximum quote. Balance refunds retain their current full-quote rule.
Pending/paused attempts keep their original amount and refund identity.

The admin list shows current payment/refund and entitlement outcomes without
duplicated historical issuance panels. Details retain history and expose the
appropriate refund/retry action. The refund dialog preserves selected amount
and quote through MFA and rejects stale or superseded previews.

Historical first-term backfills with proven whole-second serialization loss use
audited precise boundaries for financial interpretation. Original provenance is
not rewritten. See `../bugs/backend/BUG-20260919-refund-audited-tail-precision.md`.

## Evidence

- Read-only live inspection: order 3 was completed with no refund attempt; its
  audited grant end differed from the subscription end by 41,622 microseconds.
  Order 4 was already refunded via recorded external confirmation. No live
  refund or financial record was changed by this task.
- Independent backend QA/review: full service/admin refund tests on `163076454`,
  corrective representation/tail tests on `c6375b2da`; scoped QA_PASS/REVIEW_PASS.
- Frontend: four focused files, 27 tests; TypeScript and i18n checks passed.
  Independent frontend QA reran all 27 tests successfully against `b4cb4d2ba`;
  its frontend source matches the integrated candidate exactly.
  Independent frontend review found and closed stacked-dialog focus ownership;
  final reviewed diff SHA-256
  `2fe641b4333ccc4db842a9b4ccb0df231fda60348b750dba2e22fd5875b1033b`.
- Local real-component browser fixture: amount edit updates displayed impact;
  over-maximum entry shows an error and disables confirmation. Narrow viewport
  DOM width was 390px with no document overflow. Later browser debugger failure
  prevented a final full-list screenshot. Fixture data was synthetic.
- Central payment service's existing Alipay/WeChat refund adapter tests passed.
  Both transmit a per-request cash amount separately from original payment.
- Local Docker daemon was unavailable; final GitHub CI ran and passed the full
  backend unit and real PostgreSQL integration suites. Real-provider partial
  refund acceptance remains for the owner; no actual refund was submitted.

## Release boundary

Before release, complete the documented exact-candidate CI/artifact and available
PostgreSQL checks, obtain owner release authorization, and use the canonical
blue-green procedure. Preserve refund rollback readiness and all financial
records. There is no new migration. Do not submit order 3's actual refund as a
side effect of deployment; its administrator chooses the amount in the UI.

Official channel references checked for partial-refund capability:

- [WeChat refund rules](https://pay.wechatpay.cn/doc/v3/merchant/4013071001)
- [Alipay trade refund API](https://aipay.alipay.com/docs/vibe-pay/ai-web-app-payment-qianyi/api-list/alipay-trade-refund.html)

## Completed production release

Owner explicitly authorized deployment after tests pass. PR26 merged the reviewed
source without tree differences. Full CI35375387418 and Security35375391188 passed;
CI includes backend unit/integration tests, frontend/embedded route checks, lint,
shell and Docker bind contracts. Initial CI found one errcheck cleanup issue;
it was fixed and independently rechecked before the final full successful run.

GitHub production run35376694505 built and deployed exact main3ad4a97a6 through
the installed restricted receiver and maintenance-lock-owning blue-green wrapper.
Only sub2api-candidate was used; no obsolete origin or credentials were changed.
Existing installed batch-image override support, www routes/certificates,
payment/Feishu agents and private refund settings were preserved.

- Archive: 84,366,224 bytes, SHA256
  `c8e60c4027c772ac246b426ddb602768018db012e7040b4008ce3579af041b50`.
- Loaded image: `19e735dfd60c1abc348fc7a44e7852083b1caa6cfbabfdee4841b5f610997df6`.
- Server record: `/var/log/sub2api-release/gha-20260919-020003-3ad4a97a-1269036`.
- Active blue: exact revision, healthy, accepting/background active, restart0/noOOM.
  Canonical checks reported app5xx/fatal/Caddy5xx all zero. The canonical drain
  monitor confirmed old green stopped at 02:02:27 CST; no forced stop was used.
- Public www/API health returned ok; `/admin/orders` and its new
  `AdminOrdersView-CjUD2Lb8.js` asset loaded successfully, including selected-amount
  API support. Browser automation was unavailable, so no authenticated live
  visual acceptance or money-moving verification is claimed.
- Backup `sub2api-db-backup-20260919-001714.tar.gz` passed canonical isolated
  PostgreSQL/Redis restore; schema309/hash3b4bc5bcdcdde3981769463d10d36187.

Rollback uses the retained compatible9c789f0b image via the canonical receiver and
wrapper, including refund readiness/drain gates. Preserve all financial records.
No database migration or actual order3 refund was part of deployment.
