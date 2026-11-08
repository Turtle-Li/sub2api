# Image Studio cache and gallery release

## Released source and production evidence

The owner-authorized cache, incremental loading, Image catalogue restoration and
gallery refinement are deployed from
`cfd84b96d797bfdc46ba80d16d542d13d1cc357d` on fork `main`.
The initial implementation is `335b3c127c3b7f730ee26a05cc46e770efc7ffdb`;
the final commit fixes equivalent legacy Gemini aliases whose capability fields
are absent on both entries.

[Final canonical production workflow](https://github.com/Turtle-Li/sub2api/actions/runs/42513515979)
completed successfully at 2026-11-08 19:17:37 UTC. The read-only post-release
audit resolved `sub2api-blue` from the live upstream: exact source revision,
healthy, zero restarts, no OOM, background mode `active`. The former green slot
was exited with background mode `standby`; the legacy slot was absent. Public
www/API health checks both returned `ok`.

The earlier
[initial production workflow](https://github.com/Turtle-Li/sub2api/actions/runs/42511848982)
successfully released `335b3c12`. Browser inspection then exposed the omitted
capability shape, leading to the tested final repair. The initial release alone
did not establish Gemini picker deduplication.

## Result verified on the live page

Read-only authenticated Chrome inspection confirmed the final JavaScript asset
and the following choices:

| Platform | Verified picker result |
| --- | --- |
| OpenAI Image | Restored from zero to three existing configured models |
| Google Vertex / Gemini | Four entries, reduced from six equivalent entries |
| Muse | One existing model retained |
| Wan | Two existing models retained |
| Seedream | Two existing models retained |

The four Gemini choices are `gemini-2.5-flash-image`, `gemini-3-pro-image`,
`gemini-3.1-flash-image` and `gemini-3.1-flash-lite-image`. The raw authorized
catalogue still contains six identities; the picker hides only the two verified
equivalent preview aliases. History and continuation routes retain their IDs.
Different advertised capabilities or quotes remain distinct.

The 1440×900 live gallery and Gemini picker were visually inspected. There was
no horizontal overflow and no automatic original-content request during this
gallery journey. Twelve decoded preview images had a longest side of 480px.
The browser session was stopped after inspection. No generation was submitted.

## Loading and storage boundaries

The display-only model directory is cached per user/key for ten minutes and
revalidated incrementally, with bounded storage and concurrency. Cached display
does not authorize submission; fresh discovery and server admission remain
authoritative. ETags reduce unchanged response bodies while retaining current
authorization and quote checks. A slow channel no longer delays every initial
model or gallery result. These checks establish behavior, not a production
latency benchmark; SSH/admin audit timings include transport overhead.

Gallery metadata continues to page existing `batch_image_jobs` and
`batch_image_items`; it does not reconstruct history by scanning generation
request logs. The existing persistent thumbnail delivery and bounded historical
backfill remain in place, with explicit original retrieval. No new task table,
database migration, provider activation, price configuration, credential,
financial ledger or production cache purge is part of this release.

See [the implementation contract](../features/IMAGE_STUDIO_CATALOGUE_CACHE_20261109.md)
and [persistent thumbnail boundaries](../features/IMAGE_STUDIO_PERSISTENT_THUMBNAILS_20261108.md).

## Validation and limits

- Final frontend suite: 12 files / 79 tests passed, including a red/green
  regression for absent legacy capabilities and preservation of real differences.
- Frontend type, i18n, scoped lint and production build passed.
- The initial implementation passed scoped Go Image Studio/batch unit and race
  checks, Go build and scoped vet. The final repair changes only the picker,
  its tests and its contract documentation.
- An independent code reviewer approved both the integrated implementation and
  the three-file final repair with no actionable blockers. The final browser
  check establishes the previously missing live four-option result.
- Synthetic 1440×900 / 390×844 light/dark captures cover gallery geometry and
  model selection. BrowserSkill intercepted the phone bottom-button pointer;
  keyboard expansion and in-dialog pointer selection passed. That obstructed
  pointer case is not claimed as passed.

The owner-selected official Antigravity `agy` CLI listed Gemini 4 but its single
actual request returned a non-retryable 404: no configured account in the current
group supports `gemini-4-argon`. Calls stopped; this UI is Codex implementation
and visual verification. The unavailable Claude channel was not called again.
Private owner screenshots and raw audit output remain local and uncommitted.

## Recovery

The previously healthy `335b3c12` image is a compatible application fallback,
with the known limitation that the legacy Gemini preview aliases remain visible.
Use only the canonical guarded blue-green recovery path in
[deploy/README.md](../../deploy/README.md), preserving standby/background fences
and any retained connections. Do not restore the database or delete existing
jobs, thumbnails, originals or financial records. Display metadata expires with
its TTL or the browser tab. The documentation-only follow-up commit does not
trigger another deployment; the deployed application source remains `cfd84b96`.
