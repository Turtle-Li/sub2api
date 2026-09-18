# Normal payment opening — 2026-09-19

Owner request: expose normal payment purchasing, remove the administrator payment
test panel from order management, then the owner will register an account and
exercise a real subscription. This supersedes the previous closed-discovery
restriction. Actual payment and account creation remain owner-operated.

## Source change and compatibility

Remove the order-page test panel, its unused Vue component, frontend API wrapper
and component/API-only tests. Keep financial history, owner-test backend recovery,
callback and refund contracts unchanged. Replace the obsolete panel-created
refresh test with a panel-absence check. Existing ordinary order/refund controls
remain. The same release includes the previously verified single-success refund
policy and targeted row refresh in `../changes/REFUND_SINGLE_USE_REFRESH_20260919.md`.
Fixed upstream `bdb42e22f81fcb633ff0a060961211dd2bcb515b` order view was checked:
the removed panel is fork-only; ordinary upstream order controls are retained.
No dependency or schema migration.

## Catalog plan and guards

Initial readback: production blue3ad4a97a healthy; master payment true, discovery
false, registration true. Normal plans1–6 are off sale; owner-only test plan7 is
on sale. Six normal recharge tiers are disabled and a restricted0.10 test tier
is enabled. Keep the existing prices, terms, entitlements and limits.

After the verified application release, use the canonical maintenance lock and
short expected-old-value transaction to restore normal plans1–6, retire test
plan7 from sale, restore the six fixed recharge tiers and remove the test tier
from the active catalog. Enable discovery last in the same atomic transaction;
no committed state may expose an unrestricted cheap test product or an empty
recharge tier list. Preserve the reviewed-refund gate and legacy external-link
subscription settings. Abort on any concurrent configuration change.

Normal prices: Plus120/324/1152 and5X Pro550/1485/5280 for month/quarter/year.
Recharge5/49/99/199/399/599, bonuses0/0/5/20/75/145. Rollback closes discovery
and payment first; financial records and completed purchases must be preserved.
The compatible previous image is3ad4a97a; an application rollback must keep
checkout closed until the single-success refund policy is restored.

## Validation and release state

Pending final CI, independent check, canonical release and configuration readback.
Local admin-page/API suite:37 tests passed after panel removal. No live payment
or refund was submitted. User acceptance is the later real subscription flow.
