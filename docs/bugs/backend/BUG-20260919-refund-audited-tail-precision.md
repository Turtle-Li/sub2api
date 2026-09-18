# BUG-20260919-refund-audited-tail-precision

Status: IMPLEMENTATION_READY; independent combined QA/review pending. Not deployed.
Baseline: `9c789f0b7082f08e7c95900a79705f5a4429fd8e`.

## Failure and evidence

An audited historical subscription refund returns `SUBSCRIPTION_NOT_TAIL`
despite covering the current subscription tail. The backfill form serialized
the term at whole-second precision, while the subscription retained microseconds.
Read-only production evidence showed a 41,622-microsecond difference; the
backfill audit independently retained the exact subscription start and the
whole-second asserted term. No refund was in flight on the affected order.

`reviewSubscriptionRefund` compares the grant end and subscription end exactly.
The old capture-only precision compatibility did not cover this backfill shape.
The resulting manual-review response contains no quote and cannot be confirmed.

## Repair and boundaries

Use a reviewed-refund loader that recognizes only an audited first-term
serialization loss. The grant start and original end must be whole seconds;
the exact subscription start must belong to the same second as the grant start.
The persisted backfill audit must match the original grant identity and immutable
term and independently prove the exact lifecycle start. Both original boundaries
are shifted by that same fractional offset **in memory**, preserving purchased
duration. An unrefunded original current end uses the same offset; a previously
captured shortened end retains its exact value.

The fractional end anchor also requires evidence: either the audited subscription
expiry shares the precise start's fractional component, or a fully reclaimed
successor grant starts at that exact boundary. The latter covers the observed
order: during backfill, the successor refund still held an older whole-second
expiry; completing it restored the successor's precise start. A correctly
whole-second historical expiry without this evidence remains unchanged.

Exact current-tail equality remains required. Real extensions, other anchors,
missing/malformed audit evidence and other drift are not accepted. Reserve,
capture and failure release share this interpretation, so failure restores the
precise pre-refund expiry. Immutable database provenance and the original audit
remain unchanged. Backfill replay continues using the raw loader.

No migration or production data rewrite is required. Application rollback keeps
all financial records; existing pending-refund rollback gates still apply.

## Verification

The regression first reproduced `SUBSCRIPTION_NOT_TAIL` for both provider success
and failure scenarios. After the loader integration, both passed, including
exact failed-refund expiry restoration and unchanged immutable grant boundaries.
Negative coverage includes absent evidence, malformed evidence, a true extension,
a changed lifecycle anchor and a nonmatching precise current grant end.

Frontend separately preserves untouched RFC3339 backfill suggestions rather than
round-tripping their microseconds through `datetime-local`/JavaScript Date.

Knowledge candidate: no; project-specific audited accounting compatibility.
