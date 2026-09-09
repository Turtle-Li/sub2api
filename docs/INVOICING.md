# Order visibility and manual invoices

Development source: `codex/order-fulfillment-invoice`, based on `ef6900c2c`.
Requirements: [ORDER_OPERATIONS_20260910.md](ORDER_OPERATIONS_20260910.md).

The existing invoice implementation at local commit `474138269` is the reuse reference:
`backend/internal/service/payment_invoice.go`, its unit/integration tests,
`backend/internal/handler/payment_handler.go`, `frontend/src/components/payment/InvoiceRequestDialog.vue`,
and `frontend/src/components/admin/payment/AdminInvoiceDialog.vue`.
This adaptation preserves its manual invoice lifecycle and PDF email attachment boundary, while
using current refund review evidence, forward migration numbering, durable notification recovery,
current payment DTO sanitizers and independent payment/fulfillment projections.
No third-party billing engine or invoice PDF generator is added.

## Daily workflow

Customers open **My orders** to view their purchases and purchase-time snapshots, payment and
entitlement delivery independently. Original order numbers remain in details with a copy button.
Order history and invoice processing remain available when new checkout is disabled.

Eligible completed, unrefunded orders can request a personal or enterprise invoice. Enterprise
requests require a tax identifier. The customer provides an email for the official PDF. Only the
authenticated owner can read or submit their request. A rejected request can be corrected and
resubmitted; a single order keeps one durable request and increments its revision.

Administrators use **Order management → Invoice requests** and filter by processing/email state.
A new request is `PENDING`; take it into `PROCESSING`, obtain the official invoice outside Sub2,
then enter its number/item and upload the PDF to mark it `ISSUED`, or record a reason to reject it.
The service validates PDF type and size (10 MiB maximum) and retains the document privately.
The persisted invoice result is separate from email transmission. A failed or recoverable stale
delivery is visible and can be retried without issuing another invoice.

Request notifications reuse the configured Feishu bot transport and credential injection. They
carry only safe request/order references and an administration entry point, never buyer tax
identifiers, recipient emails or document bytes. Customer result emails reuse existing SMTP and
the editable `invoice.issued` / `invoice.rejected` templates; an issued result includes the PDF.
No additional finance email recipient configuration is introduced. New delivery claims and
transport starts honor the deployment's active-generation gate; standby requests leave durable
work for the active owner. Already-started transport may finish within its existing timeout.
Recovery runs separately from payment expiry/fulfillment, with failed sends retried from five
minutes up to a one-hour backoff and stale claims recoverable after five minutes.

## Important data behavior

- A paid order whose entitlement grant failed shows **Paid / Delivery failed**.
- A refund does not erase persisted payment or entitlement completion timestamps.
- Purchase snapshots come from the order, not the current plan. Missing historical snapshots
  remain explicitly unavailable. New snapshots may include more purchase evidence; old ones are
  not backfilled from current plan data.
- Invoice amount/currency come from the actual paid order. Internal credit quantities are not
  substituted for the amount charged by the payment channel.
- Invoice processing and refund-start checks serialize under the order lock. Current durable
  refund review evidence blocks new issuance; storage failures fail closed.
- No public document URL is created. Ordinary order-list responses do not contain PDF bytes or
  provider credentials. Public payment verification/resume endpoints do not expose invoice PII.
- Refunds after issuance require the administrator to handle the corresponding official invoice
  correction externally; Sub2 does not automatically issue a red invoice.

## Delivery boundary

This is a local development candidate. Tests with local mocks demonstrate application behavior;
they do not prove real SMTP or Feishu delivery, tax issuance, deployment or human receipt.
Roll back application code while retaining additive invoice records and private documents.
Do not drop invoice evidence or rewrite historical financial orders.

PostgreSQL document storage reuses the existing low-volume manual workflow. Reassess private
object storage if retained PDFs approach 5 GiB, issuance exceeds 100/day, or individual documents
need to exceed 10 MiB. This change does not introduce a new storage service.
