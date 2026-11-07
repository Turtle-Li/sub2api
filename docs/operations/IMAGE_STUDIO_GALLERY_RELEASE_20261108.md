# Image Studio gallery release —2026-11-08

Status: local validation complete; final Claude review and production activation pending.

## Scope

The model picker groups exact models by platform and shows configured internal-credit prices for each resolution. A compact gallery toolbar and item-level keyset pagination replace the old batch/item request fan-out. The existing `batch_image_jobs` and `batch_image_items` remain authoritative; no request-log scan or second task/billing ledger is introduced.

Indexing writes480px JPEG covers to the existing private COS store. A cold process reads persisted covers without provider resolution or original-result downloads. Historical missing covers use one bounded, shared batch backfill. Browser private caching and bounded blob/memory caches reduce repeated reads; originals remain explicit.

## Requested external reviews

- Claude Opus5.5 independently reviewed the full working-tree implementation and ran backend/frontend checks. It found noP0/P1, threeP2 fixes and a migration-number collision.
- Gemini4 (`gemini-4-argon`, completed response) reviewed three UI sources and synthetic screenshots. Adopted: smaller model trigger/dialog, resolution labels, inset keyboard focus, inline focused desktop search and a stronger mobile creation bar. Existing mobile touch targets are retained.
- Fixed Claude's release blockers: independent thumbnail/original queues; cancellable offscreen card reads; non-retryable409 failure cooldown versus503 active peer preparation; permit acquisition before cooldown; request-independent shared-backfill deadline;128MiB encoded upscale limit with unchanged40MP/two-decoder bounds; per-item partial failure handling.
- Also fixed blank JSONL handling, legacy unmarked high-resolution backfill, missing-price units and cover refresh after a job completes.
- Migrations are270 (concurrent gallery indexes) and271 (nullable thumbnail lifecycle fields), after main's269 monitor migration. Rollback retains the additive schema and private derivatives.

## Local evidence

Local ignored evidence: `.omo/reviews/gallery-release/` and `.omo/evidence/gallery-release/QA.md`.

| Gate | Result |
| --- | --- |
| Go targeted regression | Six packages,209 top-level tests/434 cases pass with `-tags unit` |
| Server wiring | Full `cmd/server` tests pass |
| Backend build/vet | `go build ./...` and changed-package `go vet -tags unit` pass |
| Race | Focused thumbnail service/repository/handler tests pass |
| Frontend | Nine studio test files/62 cases pass |
| Frontend build/lint | Changed-source ESLint, locale checks, type checking and production build pass |
| UI |1440×900 desktop and390×844 mobile; light/dark; picker keyboard; search focus; failure/recovery; no horizontal overflow |
| Impeccable detector | Zero findings on the changed UI sources |

No new paid generation is needed for these local checks. Initial source-read tests prove that stored covers on a fresh service instance add zero original opens/bytes. Browser fixtures prove layout and client behavior; they do not establish live COS readiness.

## Production gates

Before dispatch, verify the owner fork/main revision, healthy Caddy-selected active slot, no overlapping application lifecycle transaction and existing COS runtime configuration. Use only the explicit GitHub Actions blue-green production workflow. Preserve natural draining and its canonical maintenance lock/rollback behavior.

After activation, record the exact image revision/run, migrations/index presence, health and authenticated gallery/thumbnail cache behavior. Verify COS derivative Get/Put/Delete behavior through the existing runtime; do not export machine credentials or browser sessions. Live acceptance and final review results will be appended below.

## Bounded residuals

Existing-style transaction/remote-Put timeout ambiguity can leave a private, fenced derivative after cleanup; reads remain denied. Pre-commit eager covers on a subsequently failed job can also require retention reconciliation. These are retention-cost concerns, not permission or generation/billing changes. Full-cover existence reads during rare partial backfills remain bounded; a HEAD optimization and per-item durable derivative journal are deferred. Local browser emulation does not prove physical mobile keyboard behavior.
