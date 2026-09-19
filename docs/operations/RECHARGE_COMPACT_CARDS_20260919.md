# Recharge card refinement — 2026-09-19

Status: IMPLEMENTATION_READY, local preview only. No production catalog changes,
orders, payments, pushes or deployments were performed.

Owner direction: make the balance cards materially smaller, keep the existing
subscription visual language, put month first and preselect the eligible monthly
Plus, and keep the product panel in place while toggling purchase type. Subscription
usage limits now display the original numeric values with USD formatting. This is
an owner-requested presentation correction, not a wallet denomination change or
billing conversion. Bonus balances continue to use internal credits.

The recharge body now uses 16px padding, a 30px price, compact credited/bonus rows
and only configured benefits. The standalone large colored credited panel is gone.
The grid uses three columns when its own content width reaches 48rem, two at
20rem, and one below that. Default concurrency is three and is omitted; only
higher concurrency is advertised. Bonus copy is shortened to “赠送”.
The shared toolbar reserves the period control's layout space while recharge is
active; the hidden control is inert and hidden from accessibility. Small screens
omit toolbar discount badges; discounts remain on the plan cards. Explicit plan
links and payment recovery override the initial eligible monthly Plus choice.

Reuse: fixed upstream bdb42e22f81fcb633ff0a060961211dd2bcb515b AmountInput and
SubscriptionPlanCard were inspected. Local catalog/eligibility and entitlement
contracts remain authoritative; no upstream custom-amount input was reintroduced.
LGPL notices remain intact.

Reset-card delivery already supports immediate and monthly configuration in
PlanEditDialog and the backend. See frontend/previews/recharge/README.md and its
editable catalog.json for quarter=2 immediate and year=1/month examples. Old live
feature prose comes from stored catalog features; removing it on the live site
requires the later catalog edit as well as this frontend change. Historical
catalog SQL and snapshots remain unchanged. No fictional grants are injected into
production display.

Validation: component, validity and PaymentView regression suites; locale
completeness, ESLint, TypeScript and production build. Browser uses the actual
PaymentView with a mock API and preview layout. At 1280px the recharge cards are
178–211px high in three columns (two rows for the six tiers); subscription and recharge product tops both measure 239.5px. At
390px the common toolbar is 42px high and both product tops measure 310.5px, with
no horizontal overflow. Light/dark, default monthly Plus, quarter immediate gifts
and annual monthly gifts were inspected. No browser console errors observed.
Production AppLayout and real checkout are not part of this local visual check.

Knowledge candidate: project-only display contract and editable grant examples,
recorded here with an AGENTS routing pointer. Next gate is owner visual review;
this local implementation is not a release or production acceptance claim.

## Follow-up: live group facts and simpler subscription copy

The checkout API already queries the database for every request: each plan has
one group_id, and GetGroupInfoMap supplies that group's multiplier and quota
windows. Plus and 5X Pro refer to their respective groups; this does not add a
new two-group entitlement mapping. No payment-catalog L1/Redis cache exists to
invalidate. Do not invalidate unrelated billing usage caches for this display.

PaymentView now refreshes its ephemeral checkout data on window focus, visibility
return and a switch to subscription. It preserves the selected plan by ID,
removes a disappeared selection, deduplicates requests, ignores unmounted results,
and avoids changing an active payment confirmation. Applied coupons are
invalidated on catalog refresh and require a fresh server quote. Group and peak
multipliers are no longer printed on cards or the active-subscription summary.

Preview quotas are maintained once per group in catalog.json.groups rather than
copied into every monthly/quarterly/yearly plan. The configurable feature examples
include “同步官方赠送重置卡”; existing descriptions and configured gift schedules
remain editable. This is copy/configuration support, not a new external official
reset-card synchronization service. Live product text is not changed by the
local preview fixture.

The follow-up retains the owner's prior raw USD-reference quota display until a
separate conversion choice is confirmed: values come directly from the associated
group, with no new division or FX formula. Effective subscription usage can also
have user-specific and peak multipliers, so hiding the multiplier is not permission
to redefine billing or promise a single unadjusted-model purchasing quantity.
The existing billing multiplier resolver's bounded cache is outside this checkout
presentation change; no billing cache deletion or quota migration is performed.

Follow-up validation: 89 customer payment/card/reset-shop tests and 27 route/sidebar
tests passed. A unit-tagged service regression edits Plus group multiplier/week/
month limits and verifies a fresh group map updates all associated periods without
changing the Pro group. Production build passed. The actual coupon panel was
inspected at 1280px and 390px: period labels distinguish repeated plan names, an
empty explicit plan selection disables saving, selecting a plan enables it, and
the mobile dialog scrolls with its actions visible. Preview writes were not sent.
The temporary browser viewport override was reset. Production navigation now
places payment coupons under Order Management; the preview uses the same view
component but its neutral layout does not certify the production sidebar visually.
