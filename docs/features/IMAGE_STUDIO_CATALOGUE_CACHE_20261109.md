# Image Studio catalogue cache and gallery follow-up

Source baseline: `f03597410f48c44da86352e80e52d8c8ae2b7933`.
Owner scope: faster entry, incremental model/works loading, equivalent Gemini
aliases, restored Image models, and gallery presentation informed by the owner's
open https://new.orbitix.site/gallery page.

## Correctness and loading

The OpenAI catalogue had tested the global token-price resolver before applying
the group's per-image quote. That hid valid Image models in a configured group.
`BatchImagePublicService.ListModels` now admits a model using the same group/size
quote template as submission. A missing flat quote still rejects admission; no
token-to-image conversion, price override, provider activation or ledger change
is introduced. Native provider admission and account multipliers are retained.

`cache.ts` holds display-only key/model metadata in sessionStorage, partitioned
by authenticated user and key ID. The TTL is ten minutes, with 128 KiB per entry,
at most 32 catalogues per user, 128 models per catalogue and 256 cached keys.
Only projected directory fields are written. Credentials, task prompts, image
data and generation requests are excluded. Storage failures preserve network
discovery. Key removal, group changes and loss of generation access invalidate
the corresponding stored catalogue; changing user remounts and aborts the old
workspace immediately.

Entry displays valid cached choices immediately and revalidates keys/catalogues
in the background. Model updates arrive per key instead of waiting for the
slowest catalogue. Focus/visibility revalidates after sixty seconds. A version
fence rejects obsolete refresh results. A 403 removes a cached choice; transient
read errors retain a warning and the last display. Submission routes only from
fresh successful discovery and still passes server authorization/pricing.
Cached display never grants access or authorizes a generation POST.

The first gallery page likewise publishes each available history channel as it
returns, retaining independent cursors. Later page replay remains atomic to avoid
shrinking already loaded content on refresh. Assets retain their original key
ownership before a partial page is published.

Catalogue HTTP responses use private revalidation with an ETag and Vary on
Authorization/X-API-Key-ID. Authentication, current admission, prices and allowlists
are checked before returning 304. This reduces repeated response bodies; it does
not bypass those checks or claim to eliminate server quote computation.

## Equivalent Gemini presentation

The picker may collapse only these known pairs within the same provider, and
only when their capabilities and prices are identical:

- `gemini-3-pro-image-preview` → `gemini-3-pro-image`
- `gemini-3.1-flash-image-preview` → `gemini-3.1-flash-image`

Read-only live evidence found both pairs mapped to the same respective actual
model on the active account, with equal quotes and capabilities. Other providers,
different capabilities/prices and a preview without its stable counterpart remain
visible. Raw catalogue IDs, historical task IDs and continuation routing remain
unchanged.

The legacy Gemini catalogue omits capabilities on both members of these pairs.
Equal absence is the same advertised default contract and may collapse when the
verified pair's quotes match. A capability on only one member remains a real
difference. This does not invent size/reference capabilities for legacy models.

## Gallery design and references

The actual reference page was observed and captured on November 9. Its useful
direction is large artwork, compact controls and short metadata. It is a visual
reference, not a new source adoption; no social, public publishing, likes or
community targets are added. The existing left creation rail and host themes
remain the product contract.

Cards now use an uncropped square preview canvas, a compact caption/model/action
footer and available-width columns (minimum 220px, two columns on phones).
The existing measured fixed-row virtual grid, viewport gate, authenticated 480px
previews, explicit original retrieval and MIT gesture/viewer modules are retained.
Pinned source and test references remain 88lin `5523a592af054bfe4cbee76c2ef8d866ce5cd81e`
and VSO `4edef85ab3716fd43989b4a49561a148ba500d93`, as recorded in `UPSTREAM.md`
and the November 8 gallery contract.

The owner requested Gemini 4 through Antigravity. The configured official `agy`
1.5.3 CLI listed the model but the actual request failed non-retryably with 404:
the current account group does not support `gemini-4-argon`. Calls stopped.
These changes are Codex implementation and visual verification, not a delivered
Gemini 4 design. The unavailable Claude channel was not called again.

## Verification scope

Developer evidence includes red/green regressions for configured Image admission,
conditional catalogue authorization/quotes and equivalent aliases; mounted cache
entry and owner switch; per-key incremental updates; permission revocation;
group changes; transient failure; storage bounds; stale refresh fencing; and
independent history cursors/asset ownership. Commands and final results are in the
release evidence, with frontend type/lint/build and scoped Go unit/race/build/vet.

Real Chrome inspection used only synthetic local data plus a read-only live
baseline. Paired 1440×900 and 390×844 screenshots cover light/dark galleries and
mobile model selection. No horizontal overflow or automatic original-content
request appeared in those gallery checks. The local fixture inserted an eight
second delay in one channel; deterministic tests establish incremental rendering.
No paid generation or provider replay is part of this follow-up verification.

BrowserSkill's bottom control overlay intercepted the phone's bottom-button pointer
event (actual target `BROWSER-SKILL-OVERLAY`). Keyboard expansion, explicit close
and pointer selection inside the model dialog worked; this is a bounded manual
QA limitation, not a claimed phone pointer pass. The single UI detector pass found
two retained action-color warnings; both actions now use the existing semantic
muted/accent tokens. No second aesthetic sweep was performed.

Rollback is an application-only rollback to a compatible prior release. The
display cache expires or closes with the browser tab and has no financial or
generation authority. Existing persistent thumbnails/job/item data are retained.

Implementation ceilings are intentional: 32 displayed catalogues per owner and
six concurrent per-key reads. If genuine usage exceeds those limits or remaining
quote computation dominates measured entry time, the next owner-scoped option is
a server directory projection with explicit group/account/price invalidation;
the Image Studio maintainer owns that decision. Do not replace live authorization
with a shared unscoped server cache.
