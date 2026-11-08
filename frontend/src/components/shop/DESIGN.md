---
name: Sub2API Shop
description: Existing application design applied to product administration and SKU purchasing.
colors:
  primary-50: "#f0fdfa"
  primary-500: "#14b8a6"
  primary-600: "#0d9488"
  primary-700: "#0f766e"
  primary-950: "#042f2e"
  white: "#ffffff"
  gray-100: "#f3f4f6"
  gray-200: "#e5e7eb"
  gray-400: "#9ca3af"
  gray-500: "#6b7280"
  gray-900: "#111827"
  dark-700: "#334155"
  dark-800: "#1e293b"
typography:
  body:
    fontFamily: 'system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", sans-serif'
    fontSize: "14px"
    lineHeight: "20px"
  label:
    fontSize: "12px"
    lineHeight: "16px"
  title:
    fontSize: "18px"
    fontWeight: 600
    lineHeight: "28px"
rounded:
  md: "6px"
  lg: "8px"
  xl: "12px"
  2xl: "16px"
spacing:
  2: "8px"
  3: "12px"
  4: "16px"
  5: "20px"
  6: "24px"
components:
  button-secondary:
    backgroundColor: "{colors.white}"
    rounded: "{rounded.xl}"
    padding: "10px 16px"
  input:
    backgroundColor: "{colors.white}"
    textColor: "{colors.gray-900}"
    rounded: "{rounded.xl}"
    padding: "10px 16px"
  catalog-tab-active:
    backgroundColor: "{colors.white}"
    textColor: "{colors.gray-900}"
    rounded: "{rounded.md}"
    padding: "8px 16px"
  sku-choice-selected:
    backgroundColor: "{colors.primary-50}"
    rounded: "{rounded.lg}"
    padding: "8px 12px"
---

# Design System: Sub2API Shop

## Overview

**Creative North Star: "既有商品工作台"**

This is a scoped record of the existing application world, not a replacement brand direction. Product administration uses compact, clearly labelled controls, restrained neutral surfaces and teal actions. Buyer pages prioritize the selected price, duration and purchase action. Chinese copy uses the application's system font stack.

Scope: this directory, shared catalog tabs and the consuming shop views. The root image-studio PRODUCT.md and other feature design records do not define this surface. Source authority remains `frontend/tailwind.config.js`, `frontend/src/style.css`, the Vue components here and `frontend/src/components/common/CatalogTabs.vue`. The business contract is `docs/features/SHOP_UNIFIED_SKU_20261108.md` (paths in this paragraph are repository-relative).

**Key Characteristics:**
- Existing teal actions and paired light/dark neutral surfaces.
- One SKU editor for single and multiple specifications.
- Explicit manual and automatic delivery choices.
- Responsive field rows with a persistent dialog action area.

## Colors

### Primary

Teal identifies primary actions, selected buyer specifications and focus feedback. Admin primary buttons retain the global primary-500 to primary-600 gradient; buyer purchase actions use their existing solid treatment. Selected SKU choices use primary-50 in light mode and primary-950 in dark mode.

### Neutral

White and gray-100 separate controls and grouped areas in light mode. Admin dark surfaces use dark-800 and dark-700; buyer cards use gray-900 and gray-800 instead. These are existing distinct palettes, not interchangeable aliases. The SKU editor's secondary text pairs gray-500 with gray-400 in dark mode. This pair is not yet universal across every shop label.

## Typography

Use the system stack above for Chinese labels and prose, with no separate display face. Small labels explain attributes and units; body labels carry actions and field names; the title role describes the dialog heading. Buyer product headings are 22px, increasing to 28px at the small breakpoint. Prices and numeric SKU inputs use tabular numerals. Keep currency and duration units adjacent to the values they explain.

## Layout

The admin catalog reuses DataTable and a wrapping search/action toolbar. Subscription, usage and shop catalog navigation share CatalogTabs; the product editor reuses it for basic information, SKU pricing and delivery settings.

SKU rows stack below 640px, use two columns from 640px, and become aligned table-like rows with a visible header from 1024px. Native service rows include concurrency; manual rows omit that column. Attribute generation is a collapsible region above the rows. Field labels remain visible on narrow screens and become visually hidden where the desktop header supplies context.

BaseDialog uses an independently scrolling body and a nonshrinking footer: save/cancel remain visible without positioning them over fields. Its maximum height is 95vh on narrow screens and 90vh from 640px. Do not interpret the footer as a viewport-fixed purchase bar. Buyer detail content has a 1180px maximum width; at 1024px its purchase aside is 360px wide and sticky, while mobile places purchasing before details.

## Elevation & Depth

Borders and tonal blocks organize the table and fields. The shared dialog adds a strong shadow and blurred/dimmed backdrop. Primary admin buttons retain the shared teal-tinted shadow, secondary buttons and active tabs a small shadow. Do not replace these existing treatments with a new shadow system. Motion and preview snippets are recorded in the local `.impeccable/design.json` sidecar.

## Shapes

Inputs and shared buttons use the xl radius; SKU groups and selection labels use lg; active catalog tabs use md; dialogs use 2xl. Status badges are fully rounded. CatalogTabs is a compact rounded segmented control, not the fully rounded payment-segment variant elsewhere in the application.

## Components

### Buttons

Save/new actions use the shared primary gradient. Cancel, refresh and template actions use the bordered secondary variant. Shared buttons have a 200ms transition, focus ring, disabled opacity and slight pressed scale. Buyer purchase buttons preserve their solid teal variant and minimum 44px height.

### Inputs / Fields

Reuse the shared input border, background, padding and focus ring. SKU price and duration remain editable per row; default is a radio group and enabled is a checkbox. Do not simulate either state using color alone.

### Navigation

CatalogTabs uses a neutral track, a raised active segment, explicit tab roles and roving tab index. Arrow keys, Home and End move selection and focus. The track can scroll horizontally at narrow widths; labels stay on one line.

### Cards / Containers

The SKU table is an outlined, clipped group with separators rather than individual floating cards. On mobile each row becomes a readable vertical field group. Buyer cards group price, selectors, payment choice and purchase; their borders and spacing retain the existing buyer view styling.

### Chips and specification choices

Admin badges distinguish automatic/manual delivery and availability using text and color. Buyer specifications use labelled native radio inputs within bordered choices, wrapping as needed. Selected choices add teal border and background; disabled fieldsets remain semantically disabled.

### Product editor

Single/multiple SKU modes share the same fields. DeepSeek template loading remains secondary to editing prices. Delivery settings explicitly distinguish manual top-up/account fulfillment from service-driven automatic delivery. Technical JSON stays within advanced service parameters. Errors appear inside the form/dialog with alert semantics; save progress disables editing and updates the button label.

### Order delivery and usage guide

The order detail follows the owner-selected Gemini 4 composition: a compact summary,
then the delivery controls, then the full existing usage guide on the same page.
Order ID lives in the expandable order-information row. Delivery headings and actions
share a wrapping row; card padding is 16px on phones and 20px from the small breakpoint.
The primary reveal action uses solid primary-700 with white text in light mode and
primary-400 with gray-950 text in dark mode. Copy and refresh retain shared secondary
buttons. Controls stay at least 44px high. Raw delivery wraps long tokens and starts
hidden; it is a single block for API Keys and ordinary manual delivery.

Individual account/password or line copy requires an explicitly enabled option in
manual finished-account delivery settings. It defaults off and follows the purchased
snapshot. No guide choice, delimiter or line break enables this option. The standalone
guide page and its navigation CTA are removed; the shared guide content retains its
sections, local illustrations and FAQ directly below delivery. See the project feature
record `docs/features/SHOP_ORDER_DELIVERY_PRESENTATION_20261108.md` for data and route
compatibility, actual Gemini model receipt and validation scope.

## Do's and Don'ts

### Do:
- **Do** reuse the shared CatalogTabs, BaseDialog, DataTable, buttons and inputs.
- **Do** preserve visible units, labelled selection states and readable dark secondary text.
- **Do** keep mobile fields above the nonshrinking dialog footer.

### Don't:
- **Don't** infer delivery mode or SKU pricing from the selected usage guide.
- **Don't** turn the shop layout into a new global design contract.
- **Don't** treat screenshot sample prices as product defaults.

Recorded against the current source and inspected artifacts `artifacts/shop-sku/sku-desktop-light.jpg`, `sku-desktop-dark-after.jpg`, `sku-mobile-dark-after.jpg` and `buyer-mobile-light.jpg`. These captures document layout; they are not independent proof of payment or fulfillment behavior.
