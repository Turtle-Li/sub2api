# Image Studio Gemini 4 refinement — 2026-11-09

The owner requested Gemini 4 through the configured official Antigravity CLI to
refine the existing gallery and model selector. This follows the cache/model
release in `IMAGE_STUDIO_CATALOGUE_CACHE_20261109.md`, retaining its authorization,
incremental loading, canonical Gemini aliases, Image admission and price rules.

## Actual model invocation

The verified executable is `/opt/homebrew/bin/agy` 1.5.3. Its model catalogue
provides `gemini-4-argon-high`, `gemini-4-argon-medium` and `gemini-4-argon-low`.
`high` is a real client reasoning tier. Do not combine a tiered model ID with a
conflicting `--effort`; select the listed model ID directly.

Existing successful CATI and 小白学 AI calls established the native invocation.
For this owner-selected native route, the child process removed inherited
`GEMINI_API_KEY`, `GOOGLE_GEMINI_BASE_URL` and `_AGY_PROFILES` overrides and used
`--settings '{"modelProvider":""}'`. It reused the existing default login;
no account, global configuration or system proxy was changed. Proxy variables
were inherited from the canonical adaptive-relay configuration rather than a
project-specific Clash port. A confirmed Go TLS streaming error was handled with
the existing process-only `GODEBUG=http2client=0` compatibility setting.

The High tier read the scoped UI sources after the owner restored a working
network node. After its streaming failure, the same conversation was resumed
with the Low tier to return the already-prepared patch without further tools.
The resumed process exited 0 and returned a 13,973-character patch response.
Its JSON still retained the earlier TLS error and `status: ERROR`; this is
evidence of a returned patch body, **not** a clean CLI `SUCCESS`. Codex applied
the patch and verified the resulting implementation independently. No further
model request was needed. The unavailable Claude channel was not called again.

The read-only brief contained UI source, synthetic screenshots and the public
reference. Production credentials, owner artwork and private browser captures
were not sent. Raw model output and browser evidence remain local, uncommitted.
The reusable personal entry is `auto_work/docs/AntigravityCLI调用约定.md`.

## Applied UI behavior

- Each card preview frame adapts to its decoded thumbnail aspect ratio (clamped
  to `[9:16, 16:9]` with a `4:3` placeholder before decode) inside responsive
  waterfall columns (`studio-gallery-column`) so landscape, square and portrait
  images fill their frames without letterbox/pillarbox blank areas or row-stretch
  gaps. Scrolling toward the bottom sentinel triggers incremental downward
  rendering and page continuation while retaining explicit load-more controls.
- Card footers render a platform mark badge (`G`, `O`, `S`, `W`, `M`, `H`) paired
  with a concise model short name (e.g., `G` + `3.1 Pro`, `G` + `3.1 Lite`,
  `O` + `GPT 2.5`, `S` + `5.0 Pro`, `W` + `2.7 Pro`, `H` + `Alpha Edit`) while
  keeping the full platform and model ID in the `title` tooltip.
- The left creation rail (`.studio-composer`) and model picker dialog
  (`.studio-model-dialog`) maintain fixed-height slots across model and platform
  changes so neither the rail controls nor the modal window jump vertically:
  - `.studio-model-trigger` is locked to `52px` height with single-line ellipsis,
    platform mark badge, concise model short name (`strong`), and `platform · id`
    subtitle (`small`) plus full `title` tooltip.
  - `.studio-parameters` is grouped directly beneath the model picker inside
    `.studio-model-group` and keeps two fixed `40px` slots (`画面比例` and `分辨率`),
    rendering `.studio-field-static` (`模型默认`) when a selected model omits
    capability options.
  - `.studio-model-dialog` uses a fixed dialog height (`min(540px, calc(100dvh - 32px))`
    on desktop and `min(80dvh, 640px)` bottom sheet on mobile) with a flex-filling
    `.studio-model-browser` so switching between platforms with different option
    counts never shifts the modal window position.
- The status badge uses host theme tokens. Running tasks use the accent color,
  errors use the danger color, and cancelled/interrupted tasks remain neutral.
  Error captions clamp the prompt to one line so the failure message fits.
- On phones, platform tabs scroll horizontally above full-width model options.
  Model prices remain aligned and visible. Search, keyboard selection, Escape
  and focus return keep their existing behavior.

Codex adapted the proposal to preserve 44px phone action/platform targets, an
opaque neutral completion badge, a named status class and room for error text.
The proposed smaller touch targets and translucent low-contrast label were not
adopted. Existing desktop actions remain 32px. No provider, billing, task,
thumbnail, cache, schema, dependency or generation code changed.

## Reference and validation

The pinned 88lin TaskCard source at
`5523a592af054bfe4cbee76c2ef8d866ce5cd81e` was rechecked before editing. Existing
MIT attribution and intentional host adapters remain in `UPSTREAM.md`; this is
a presentation refinement, not a new upstream source adoption.

Validation of the final source:

- `pnpm exec vitest run src/features/image-studio`: 12 files, 79 tests passed.
- Scoped TaskCard ESLint, `pnpm build` and `git diff --check` passed.
- One scoped Impeccable detector pass returned no findings for the two UI files.
- Real Chrome synthetic QA at 1440×900 and 390×844 covered light/dark gallery
  and phone picker screenshots, equal-height cards, uncropped images, no page
  overflow, visible error text and neutral cancelled badges/icons.
- Phone action/platform targets measured 44px; OpenAI and Seedream prices fit.
  Gemini search and arrow-key selection passed; Escape returned focus to the
  trigger. Fixture aliases reduced to three canonical options in that fixture;
  production's separate four-option catalogue remains the release smoke target.
- Browser resource entries recorded zero original-content requests. No paid
  generation or provider probe was performed for this presentation change.
- Independent Standards and Spec review both returned CLEAR / APPROVE with no
  blockers, including the final unobstructed error/cancellation evidence.

The BrowserSkill overlay obstructed the phone bottom-control pointer path;
keyboard expansion passed. The obstructed pointer case is not claimed as a
pass. Local fixtures validate layout and browser request behavior, not WAN speed
or production provider availability. Production release/smoke evidence is kept
in [the operations report](../operations/IMAGE_STUDIO_GEMINI4_UI_RELEASE_20261109.md).
