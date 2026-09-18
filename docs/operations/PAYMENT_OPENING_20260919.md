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

Completed: source PR27 merged as `04a95a7b23574b5c56efb5460eeaf07729ee9af8`,
tree-identical to reviewed `fdeb9c32a`. CI35384248222 and Security35384252063
passed; production run35385428961 completed the canonical release at03:28:17 CST.
Local admin-page/API suite:37 tests passed after panel removal. No live payment
or refund was submitted. User acceptance is the later real subscription flow.


- Active green is healthy, accepting/background active, restart0/noOOM. Previous
  blue stopped naturally before catalog activation. App/Caddy5xx and fatal counts
  were zero at the canonical release audit. Existing www and credential agents
  were preserved; no old origin was accessed.
- Archive84,379,283 bytes, SHA256
  `e904a61a00481800d08c65fbc558158c407887cee7b6017f2503fe097458bbe6`.
  Runtime image `cb079cf76be2f7475031a5bfc6dc4f94eee40485dc4d7412d1a76c9926988772`.
  Release log `/var/log/sub2api-release/gha-20260919-032752-04a95a7b-1415285`.
- Independent frontend QA passed35 order tests and49 purchase tests, typecheck
  and diff checks. Scoped ESLint and37 order/API tests also passed. Prior refund
  race/recovery evidence remains in the linked change report.
- Configuration script SHA256
  `ce05cd6e9469d5b819fbabb589cc5fb4662430d6269c909ec2bdabb03f78b996`
  passed independent review and actual transaction rollback rehearsal. It checks
  subscription enablement using the service's false-value normalization, normal
  catalog identity, registration/backend mode and active OpenAI subscription
  groups. Canonical maintenance FD8 spans the entire short transaction.
- First activation attempt stopped before SQL because a nested node-state helper
  tried to acquire the already held lock. Receipt absence/public entry=false
  confirmed no write. Removing that redundant nested call allowed activation;
  the normal node-state read had already passed outside the lock.
- Protected before/after catalog snapshots and script live under
  `/var/log/sub2api-release/payment-opening-20260919`. Independent exact readback
  passed: six normal plans on sale, plan7 off sale, six normal recharge tiers,
  payment/entry true, reviewed refunds true; original prices/benefits preserved.
- Native Chrome hard refresh verified visible recharge/subscription navigation,
  all six recharge tiers and correct monthly/quarterly/yearly plan prices,
  Alipay/WeChat presentation, no test panel in admin orders and no refund action
  on the actual partially refunded order. Public settings agree. No order,
  payment, refund or test notification was created during these checks.

The owner's subsequent report of repeated business-callback Feishu alerts is a
separate active investigation. Opening/health verification does not claim that
historical central outbox incidents have recovered.
