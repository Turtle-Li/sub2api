# Image Studio gallery release —2026-11-08

Status: Claude review PASS, Gemini4 refinement complete, merged to main and deployed; final authenticated acceptance passes.

## Scope

The model picker groups exact models by platform and shows configured internal-credit prices for each resolution. A compact gallery toolbar and item-level keyset pagination replace the old batch/item request fan-out. The existing `batch_image_jobs` and `batch_image_items` remain authoritative; no request-log scan or second task/billing ledger is introduced.

Indexing writes480px JPEG covers to the existing private COS store. A cold process reads persisted covers without provider resolution or original-result downloads. Historical missing covers use one bounded, shared batch backfill. Browser private caching and bounded blob/memory caches reduce repeated reads; originals remain explicit.

## Requested external reviews

- Claude Opus5.5 independently reviewed the full working-tree implementation and ran backend/frontend checks. It found noP0/P1, threeP2 fixes and a migration-number collision.
- Gemini4 (`gemini-4-argon`, completed response) reviewed three UI sources and synthetic screenshots. Adopted: smaller model trigger/dialog, resolution labels, inset keyboard focus, inline focused desktop search and a stronger mobile creation bar. Existing mobile touch targets are retained.
- Fixed Claude's release blockers: independent thumbnail/original queues; cancellable offscreen card reads; non-retryable409 failure cooldown versus503 active peer preparation; permit acquisition before cooldown; request-independent shared-backfill deadline;128MiB encoded upscale limit with unchanged40MP/two-decoder bounds; per-item partial failure handling.
- Also fixed blank JSONL handling, legacy unmarked high-resolution backfill, missing-price units and cover refresh after a job completes.
- Final independent Claude review of merged `fb95bfcf60b889f1f815b69f368fa5371c5233c0` returned **PASS**, with noP0/P1/P2 findings. Its completed result is `.omo/reviews/gallery-release/claude-final.json`; it inspected the frozen source/callers and actual recorded checks without editing or rerunning broad suites.
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

Post-merge backend regression and frontend production build also pass. The merge
changes no Image Studio/batch/thumbnail source from the previously tested feature
commit `b179045bb`.

No new paid generation is needed for these local checks. Initial source-read tests prove that stored covers on a fresh service instance add zero original opens/bytes. Browser fixtures prove layout and client behavior; they do not establish live COS readiness.

## Production gates

Before dispatch, verify the owner fork/main revision, healthy Caddy-selected active slot, no overlapping application lifecycle transaction and existing COS runtime configuration. Use only the explicit GitHub Actions blue-green production workflow. Preserve natural draining and its canonical maintenance lock/rollback behavior.

Read-only preflight confirms owner fork/main, healthy Caddy-selected blue revision
`d65140f7a43c76788003d319b484f1fa5723043e`, no pending transaction and stopped
green/legacy slots. The existing delivery store is enabled, its endpoint/region/
bucket/prefix are configured, and its Vault references/agent are present; no
secret value was printed. Host memory is1906MiB total with1027MiB available at the
snapshot, covering the bounded thumbnail decoder/source allowance. The stored
retained-drain override remainsfalse and background mode remainsactivate.

After activation, record the exact image revision/run, startup migration gate,
health and authenticated gallery/thumbnail behavior. Exercise private COS cover
delivery through the existing runtime and retain regression evidence for
destructive cleanup boundaries; do not export machine credentials or browser
sessions. Actual observations and their limits are recorded below.

## First activation and acceptance

The canonical [production workflow](https://github.com/Turtle-Li/sub2api/actions/runs/42423545016)
completed successfully for `b6e6582d9c5f252140d192e70487986bd8c5a9a3`.
Caddy selects healthy `sub2api-blue` at that exact OCI revision, with zero
restarts/OOM events and no pending release transaction. Green and legacy slots
are stopped. Public API health and the www studio route both return HTTP200.

Authenticated Chrome acceptance shows15 rendered historical cards:14 decoded covers with
longest edge480px and one existing failed generation. The platform picker shows
OpenAI/Google Vertex groups and configured resolution-sensitive prices. No new
generation, original download, cookie/token export or destructive cleanup probe
was performed. Historical cover delivery exercises the existing private COS
backfill Put/Get path; Delete/lifecycle remains covered by regressions rather
than deleting an owner's output. Startup logs have no migration failures, but do
not print270/271 filenames; this is not a direct SQL index-presence observation.

Acceptance also found that a failed cover was not retried by the toolbar refresh
when its task ID/count/status stayed unchanged. A mounted regression first failed
with one call instead of two. The follow-up keys the effect to refreshed record
identity and clears the revoked URL state; unchanged records do not trigger new
reads. All10 studio test files/63 cases, changed-source lint and production build
pass. A further independent Claude Opus5.5 review returned **PASS**, checking the
Studio callers, terminal polling identity, cancellation and object-URL cleanup.
Evidence: `.omo/reviews/gallery-release/claude-refresh.json` and
`thumbnail-refresh-before.log` / `frontend-refresh.log`. This small follow-up
requires a second canonical image release; its activation is recorded below.

## Final activation

The follow-up [production workflow](https://github.com/Turtle-Li/sub2api/actions/runs/42425003712)
completed successfully for `5f2fceba80a32dae72bec2ccdebe19d74577e0b3`.
Read-only inspection confirms Caddy selects healthy `sub2api-green` at that exact
OCI revision. There are zero restarts/OOM events, zero migration-error log lines
and no pending release transaction. Blue and legacy slots are stopped; stored
background mode isactivate and the retained-drain override remainsfalse.
Public API health and the www studio route both return HTTP200.

After the process replacement, authenticated Chrome again renders15 historical
cards with14 decoded covers, all with longest edge480px. There are zero page
alerts and no horizontal overflow. Prices/platform selection and server search
were also exercised during acceptance. Browser reload and the prior private COS
backfill establish working persisted cover delivery across the release; the
zero-original-read guarantee on fresh service/cache eviction is additionally
covered by source-read-count regressions. No browser network-cache hit rate or
direct SQL index-presence query is claimed.

Final local visual evidence is `.omo/evidence/gallery-release/QA.md`, including
`live-final-gallery.jpg` and `live-model-prices.jpg`. Real gallery screenshots
remain local. The local fixture/dev processes were stopped after acceptance.
Subsequent completion-record commits contain documentation only; the running
application revision remains the exact image SHA above.

## Bounded residuals

Existing-style transaction/remote-Put timeout ambiguity can leave a private, fenced derivative after cleanup; reads remain denied. Pre-commit eager covers on a subsequently failed job can also require retention reconciliation. These are retention-cost concerns, not permission or generation/billing changes. Full-cover existence reads during rare partial backfills remain bounded; a HEAD optimization and per-item durable derivative journal are deferred. Local browser emulation does not prove physical mobile keyboard behavior.
