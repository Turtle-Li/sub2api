# Administrator refund clarification — 2026-09-19

Implementation candidate: `e42b4fbe285e43b9df6b8b69e45aed195bbb9ebf`.
Base: `9c789f0b7082f08e7c95900a79705f5a4429fd8e`.
Status: local implementation and scoped review complete; not pushed or deployed.

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
- Local Docker daemon was unavailable; PostgreSQL integration and real-provider
  partial refund acceptance remain unverified. No production readiness claim.

## Release boundary

Before release, complete the documented exact-candidate CI/artifact and available
PostgreSQL checks, obtain owner release authorization, and use the canonical
blue-green procedure. Preserve refund rollback readiness and all financial
records. There is no new migration. Do not submit order 3's actual refund as a
side effect of deployment; its administrator chooses the amount in the UI.

Official channel references checked for partial-refund capability:

- [WeChat refund rules](https://pay.wechatpay.cn/doc/v3/merchant/4013071001)
- [Alipay trade refund API](https://aipay.alipay.com/docs/vibe-pay/ai-web-app-payment-qianyi/api-list/alipay-trade-refund.html)
