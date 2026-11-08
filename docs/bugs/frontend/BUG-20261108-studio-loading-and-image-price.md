# Image Studio load fan-out and image price units

Baseline: `544a7415fd605403b1e227fd75a3edb59fcd822d` (2026-11-08).
Owner report: slow model/gallery entry, implausible Gemini image prices, and
missing Muse/Wan/native models after the first gallery release.

## Bug analysis

Confirmed by read-only production inspection:

- Owner has 16 active keys; only 5 belong to batch-image-enabled groups and 5
  have image history. The client discovers models and history for every key in
  separate three-request waves and publishes only after the entire loop.
- App request samples: keys p50 75 ms/max 81 ms; gallery p50 83 ms/max 151 ms;
  model catalogue p50 87 ms/max 490 ms, including 47 expected 403s out of 71
  requests; thumbnails p50 100 ms/max 2116 ms. These are server durations, not
  measured browser load times. Client RTT, sequential waves and serial covers
  add delay beyond those numbers.
- The key directory uses the generic key listing, including last-used IP and
  live concurrency enrichment that this UI never renders.
- `BatchImageModelPricingResolver.BatchImageUnitPrice` returns an image-output
  **token** price as the whole-image price for token-billed Gemini models.
  Resolution conversion is absent. This affects future holds/quotes as well as
  display. Existing jobs retain their immutable captured price snapshots.
- Catalogue generation repeatedly loads the same group/user rate and, on
  OpenAI groups, the same candidate accounts for multiple native providers.
- Covers share one serial promise queue, so a slow cold preview/retry blocks
  all subsequent visible covers even when their JPEGs are already stored.
- Muse account227/group27 exists but batch generation is disabled and all
  image price tiers are unset. Wan/Seedream have no current native Sub2 account.
  Previous canary group40/account250 and price0.01 are test-only configuration.

## Why the previous fixes were insufficient

Item-level paging and persistent thumbnails removed full-history scans and
repeated original downloads, but left the workspace-wide key fan-out and the
serial browser cover queue. Neither change corrected the independent pricing
unit error or converted test-only native adapters into priced production routes.

## Implementation plan (owner tariff and credential decisions received)

One writer: Codex in the existing managed worktree. No schema migration or new
generation ledger is needed. Loaded logic-index blocks: none; fallback search.

| Task | Depends on | Owned output | Acceptance and evidence | Rollback / stop |
| --- | --- | --- | --- | --- |
| T1 | baseline | Dedicated safe key projection; frontend model/history eligibility | Only owned, live keys; generation and history metadata queried without secrets/IP/concurrency; historical disabled-group works remain accessible; regression tests | Revert code; stop on ownership regression |
| T2 | baseline | Gemini per-image conversion and request-local catalogue reuse | Official output-token counts by supported model/resolution; configured tier precedence; bounded group/rate/account reads; immutable old snapshots; failing-then-passing tests | Revert new quotes only; never rewrite settled jobs |
| T3 | T1 | Bounded parallel preview reads | A cold cover cannot block other JPEGs; abort/cache bounds and explicit-original isolation preserved; tests and browser evidence | Restore queue; stop on original-download replay |
| T4 | T2; owner tariff/credential choice | Native account/group readiness configuration | Reuse exact pinned mappings and existing ledger; positive formal price and credential provenance; no fake availability or ambiguous POST retry | Restore only changed group/account settings; retain jobs/receipts |
| T5 | T1–T4 as applicable | Review, QA, canonical release record | Scoped Go/TS checks, actual browser catalogue/gallery, authorized main push and exact-SHA blue-green release, health/smoke and timing evidence | Canonical rollback with maintenance lock; preserve drains |

All tasks use repository contracts in the Image Studio session, gallery and
persistent-thumbnail documents. Native transport remains pinned to VSO
`4edef85ab3716fd43989b4a49561a148ba500d93`; its model catalogue separates usable
routes from unavailable placeholders. Pricing points there are a separate
product denomination, not automatically Sub2 internal-credit prices.

Google output conversion references (checked 2026-11-08):
[Gemini API pricing](https://ai.google.dev/gemini-api/docs/pricing?hl=en),
[Vertex pricing](https://cloud.google.com/model-garden/pricing),
[3.1 Flash image specifications](https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/gemini/3-1-flash-image).
Pro:1120 tokens at1K/2K,2000 at4K. Flash3.1:1120/1680/2520.
Flash-Lite3.1:1120 at1K. Flash2.5:1290 at1K.

Knowledge candidate: no (project-specific pricing and ownership contracts).

Owner decisions: Muse0.10/image; Wan/Seedream official output price plus about
CNY0.10. Reuse active oneclick accounts in protected server configuration without
new Vault entries. Final contracts and pre-release validation are recorded in
`docs/features/IMAGE_STUDIO_LOAD_AND_NATIVE_PRICES_20261108.md`.
