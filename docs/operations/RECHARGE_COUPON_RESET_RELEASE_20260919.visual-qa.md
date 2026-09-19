# Reset-card quick-entry visual QA

Date: 2026-09-19 (Asia/Shanghai)
Preview: `http://127.0.0.1:5199/previews/recharge/index.html?view=subscriptions`
Data: local preview fixture only. The adapter rejects every non-GET request except the existing local coupon quote path; no production endpoint or order creation was used.

## Evidence

- Desktop Safari: subscription cards render adjacent `续费` and `购买重置卡` actions for both active OpenAI subscriptions. CTA labels remain within their header row.
- Desktop reset-card deep link: clicking Plus `购买重置卡` navigates to `/purchase?tab=subscription&purchase=reset_card&subscription_id=9101`, focuses and highlights the Plus reset-card offer, and does not show a renewal dialog or create an order.
- Desktop renewal: `续费` still navigates to the existing group flow and opens its `选择套餐` modal.
- Mobile Safari Responsive Design Mode, 393 x 852: subscription CTAs remain visible without overlap; the payment page's compact `购买重置卡` shortcut appears above the plan cards.
- Mobile deep link: targets and highlights the Plus reset-card offer without a dialog or checkout.
- Mobile top shortcut: scrolls and focuses the reset-card shop (the first eligible offer when no subscription id is supplied) without checkout.

## Result

PASS. No visual or interaction regression found in the requested journeys.

CUA screenshots were captured during the corresponding desktop and 393 x 852 responsive checks. The CUA screenshot stream has no filesystem export API, so this report records the exact preview URL, viewport, and observed UI state.
