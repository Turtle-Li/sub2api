# Channel model pricing and Kimi K3.1 Pro

Owner request: add Kimi K3.1 Pro to the existing Kimi channel and remove repeated
manual entry of catalog prices and group multipliers when configuring models.

Baseline: fork/main `71681c38d`. The related checkout was fast-forwarded without
touching its pre-existing untracked `.omo/`. Branch preflight returned
`CANONICAL_NOT_READY` because `.agent-worktree.toml` has no resolved trunk; reuse
this clean, related checkout. Root owns integration and all source writes.

Fixed upstream reference: Wei-Shaw/sub2api
`5c185c5c5d4f5e409e2139806e0964ae944e03e8` (v0.2.18 synchronization).
Compared `backend/internal/handler/admin/channel_handler.go` and
`frontend/src/components/admin/channel/PricingEntryCard.vue`: upstream supplies
USD defaults and only fills an empty card on its first single-model addition.
Preserve that shared-card safeguard and LGPL notices. The fork intentionally
supports authored CNY cards and group-dependent internal reference units.

| Task | Dependencies | Owner / writer | Acceptance and validation |
| --- | --- | --- | --- |
| T1 | none | root | Verify exact Pass SKU and native official price; inspect existing mapping, allowlist, channel and currency contracts. |
| T2 | T1 | root | Add canonical model and retain short alias in account 221, group 18 and channel 3 through Admin API; account request and public plaza check. Preserve credentials, pins and unrelated settings. |
| T3 | T1 | root | Extend existing defaults endpoint with explicit authored currency; known Kimi native prices avoid FX approximations. Keep omitted-currency USD compatibility. Focused endpoint tests. |
| T4 | T3 | root | Fill a single empty token card, show lookup status, preserve edits during requests; offer catalog model suggestions; preview current linked-group multiplier and base input/output charges. Frontend regression tests and visible QA. |
| T5 | T2,T4 | root + independent review when available | Typecheck, lint, build and scoped backend/frontend tests. Record limitations and release state. |

No schema migration or new price scraping service. Authoring accepts only
deterministically identified catalog, exact fallback or verified native models;
runtime family guesses cannot populate an unknown model's price card. Verified
native Kimi CNY quotes take priority for CNY authoring, independently of the USD
catalog: native CNY and international USD quotes need not be FX equivalents.
USD authoring retains catalog priority. Group rates
remain owned by group management, never copied into channel base prices. Shared
multi-model cards, manual prices, intervals and time periods are not silently
overwritten. Preview covers base token charges, excluding user-specific rates,
long-context tiers and time windows. If any current model matches a group price
card, show that group pricing takes priority instead of displaying channel-based
numbers. Match the backend's exact or suffix-star patterns, case/space handling
and Claude dot normalization. Do not restore DeepSeek peak/off-peak pricing.

Verified sources (2026-10-25):

- [Cline live Pass catalog](https://api.cline.bot/api/v1/ai/cline/recommended-models):
  `cline-pass/kimi-k3.1-pro`.
- [Kimi native pricing](https://platform.kimi.com/docs/pricing/chat): per million
  tokens CNY 13 input, 68 output, 1.3 cache hit, 13 cache write 5m and 26 write 1h.
- [Kimi pricing article](https://www.kimi.com/resources/kimi-k3-1-pro-pricing).

Live inspection: group 18 rate 0.3; channel 3 contains the existing K3 card;
account 221 already has short alias `k3.1-pro`, but lacks canonical
`kimi-k3.1-pro`; the group allowlist and channel card lack both new names.
The existing defaults endpoint reports `found:false` for the canonical name.

Rollback configuration by removing only the added canonical mapping, added
allowlist names and new price card; retain the pre-existing short account alias.
Rollback source by reverting this coherent feature. Stop live changes if the
Pass SKU disappears or a competing edit changes the inspected target fields.
Production release follows deploy/README.md after the concrete result is ready.

T1-T5 implementation and verification completed. Independent read-only review
passed after reproducing and repairing unknown-family authoring, dynamic-catalog
K3.1 alias admission and misleading group-card previews. Native Kimi CNY quotes
are intentionally independent of USD catalog quotes. K3.1 identification accepts
only canonical, short and `cline-pass/kimi-k3.1-pro`; dynamic date/namespace/version
normalization cannot broaden that whitelist.

Validation: 99 channel/view frontend tests, locale completeness (3), typecheck,
changed-file ESLint and production Vite build passed. Focused new service/handler
tests passed with `-race`; broad Billing/Pricing/Channel unit suites, `go build
./...`, scoped `go vet`, gofmt and diff checks passed. Real-component browser QA
covered light/dark, automatic fill, current group-rate changes and manual edit
preservation. Production model/account/plaza API probes passed.

Optional broad `-race` testing exposed existing shared Account memo races in
parallel OpenAI restriction fixtures. Isolated existing tests reproduced them;
their source/call chain is unchanged by this feature. They are recorded in the
task evidence rather than included in this feature's repair scope. Production
browser tab inspection timed out, so production acceptance is API-based.

Source `9a7cf06d0bba68d33a2b08f0f06675195a6913d4` was released successfully by
production workflow `40185841154`; healthy green and post-release API/static
bundle checks passed. Full release and rollback evidence is in
`deploy/KIMI_K31_CHANNEL_20261025.md`.
Knowledge candidate: no; project-specific pricing and provider configuration
belongs in this checkout and private Registry.
