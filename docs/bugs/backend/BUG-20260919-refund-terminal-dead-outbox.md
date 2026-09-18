# BUG-20260919-refund-terminal-dead-outbox

Status: RECOVERED — verified operational recovery, no application patch required.

Bug:
现象: Owner continues receiving hourly central business-callback failure alerts,
even though the corresponding Sub2 subscription refund is complete.

复现条件: September16 refund-success event exhausted12 callback attempts with
HTTP503. Central outbox remainsDEAD; Sub2 inbox remainsRETRYABLE_FAILED12 and
its per-order cursor remainssequence1. Later authoritative query/manual recovery
settled Sub2 order4 and its attempt without replaying the central event.

调用链: Central refund success -> immutable outboxsequence2 -> Sub2 webhook503
-> retry budget exhausted -> OUTBOX_UNDELIVERED OPEN -> hourly reminders.
Local refund recovery is a separate state path; it cannot acknowledge an unseen
webhook to the central service. Central incident recheck correctly requires
actual DELIVERED before resolving.

影响范围: Read-only live inspection found one undelivered Sub2 event and one
OPEN incident, both tied to the same historical order4 refund. New partial
Alipay refund is not the failed event. No evidence of a current blanket callback
outage was found.

疑似根因: Confirmed state mismatch at the delivery/acknowledgement boundary,
not a stale frontend or failed money refund. Central payment isREFUNDED10fen;
Sub2 order4 REFUNDED0.10, attemptSUCCEEDED/MANUAL_EXTERNAL_CONFIRMED,
needs_manual_review=false, entitlement_reserved=false. Twelve503 responses ended
on2026-09-16 08:50UTC. The current running Sub2 is04a95a7b2; central runs the
September16 refund-manual-recovery image. The exact original503 internal cause
is historical and is not claimed from the generic processing_failed code.

推荐修改方案: Validate current duplicate-success processing, then use the existing
WorkerPool.ReplayDeadOutbox method for the single immutable event with an
explicit operator/reason/request audit. Extend its attempt budget, preserve
identity/body/hash/sequence and let the normal signed worker deliver it. Confirm
Sub2 inboxPROCESSED/cursor2, centralDELIVERED/HTTP200, incidentRESOLVED and no
new unresolved reminders. Never setDELIVERED or close the incident directly.
A narrow disposable operation adapter may invoke the existing method; do not
create a new financial API, payment, refund request or credential.

风险: Replaying an at-least-once event relies on receiver idempotency. Current
applyUnifiedRefundObservation's alreadySUCCEEDED/unreserved path returns success
without reapplying deductions; independent review and existing focused tests
must pass before actual replay. Preserve all previous deliveries/audits and
compare financial state before/after. Any lost response requires readback before
retry. Recovery messages are sent by the existing incident lifecycle, not a
manual Feishu smoke.

Evidence: protected per-service read-only queries in the September19 task;
centralinternal/store/postgres/outbox.go, migrations/sql/000018_feishu_payment_incidents.sql;
Sub2payment_unified_webhook.go andpayment_unified_refund.go. Existing
BUG-20260910-outbox-replay-limit was checked: migration17 already raises the
lifetime budget to300; this event stopped at its ordinary12-attempt budget,
so the old30-attempt constraint defect does not explain this incident.

## Verified recovery — September 19, 2026 (CST)

The independently reviewed fixed-event helper used the existing live worker Vault
agent in memory and `ReplayDeadOutbox`, under the central release maintenance lock.
Read-only preflight passed before one apply. No existing service was restarted,
no credential was exported and no financial state or incident was edited directly.

- Event `c31af565-d103-4388-91bc-13a287c3803c` delivered on attempt 13 with HTTP 200
  at 03:59:53; immutable body SHA remained
  `7b584c6ddc6d220b831cc73ee707d2f3aa5fd12530e5c090e9097a36040e7301`.
- Sub2 inbox became PROCESSED at 03:59:52.982758; cursor advanced to sequence 2
  with active event/sequence cleared. It was not merely a permanent-rejection ACK.
- Before/after assertions passed for both services' order/refund amounts, refund
  status, central fund/transaction counts, Sub2 grant expiry/refunded seconds and
  money, zero reservations and zero conflicts. REFUND_SUCCESS audit count stayed 1.
- Incident `dab3ad91-62a3-4d34-8384-79614f2aa631` automatically became RESOLVED
  at 04:00:14.374961. The RESOLVED notification is DELIVERED. All previous 60
  reminders remain historical DELIVERED records; no pending reminders remain.
  Central undelivered outbox count for app.sub2.live is 0.
- Helper focused tests, vet and independent review passed. Artifact SHA256:
  `ffd42ebce2ccf1636f651d1756a8f1c7988ed0bcac60f0c6048d702a4c794b70`.
  Central project retains the narrowly scoped command source/tests at
  `cmd/payment-outbox-replay-ops-20260919/`; protected host artifact is retained at
  `/var/backups/totools-pay-live/releases/outbox-replay-20260919/replay` for audit.

The exhausted historical delivery needed an audited replay after receiver
recovery. Refund completion alone must not manufacture delivery acknowledgement.
Do not undo delivered financial events or replay this already-processed event.
