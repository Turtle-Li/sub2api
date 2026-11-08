# Image Studio Gemini 4 UI release — 2026-11-09

Status: released successfully; read-only runtime and live browser smoke passed.

## Source and release identity

- Source: `e57b617dcf7818d993b639af2a49265f1ca437a9` on Turtle-Li/sub2api main.
- Canonical production workflow:
  https://github.com/Turtle-Li/sub2api/actions/runs/42521329456
- Inputs: `build_only=false`, `deployment_target=aws-candidate`.
- The preceding main release `1a6b2ef27` completed successfully in run
  `42520235112` before this task pushed main. Its healthy blue runtime was
  selected from Caddy; restart count 0, no OOM, background active. Green was
  exited/standby and legacy absent. Root-owned configuration verified the exact
  fork/main, activate mode and stored retained-drain override=false.

The reviewed UI commit was rebased onto the seven concurrent main commits with
no conflict. Its two runtime blobs are identical to the frozen visual-QA and
independent-review snapshot. Concurrent account, navigation and status changes
were preserved. Only the normal explicit blue-green workflow was dispatched;
no raw container lifecycle, Caddy replacement or runtime configuration change
was performed by this task. Do not advance main while the workflow builds.

## Behavior and validation

The gallery uses taller uncropped previews, compact uniform metadata, semantic
status badges and neutral cancellation. The phone model picker uses a horizontal
platform bar and full-width price rows. Existing cache, equivalent Gemini
aliases, restored Image admission, thumbnail lifecycle, task ownership and
billing remain unchanged by this UI increment.

The official native Antigravity Gemini 4 returned the applied patch body after
same-conversation recovery. Its JSON retained an earlier TLS error; this is not
recorded as clean CLI SUCCESS. Exact model IDs, environment correction and
accessibility adaptations are in
[the feature contract](../features/IMAGE_STUDIO_GEMINI4_UI_20261109.md).

Final integrated source passed 12 Image Studio test files / 79 tests and a full
frontend build (locale completeness, TypeScript and Vite). Scoped ESLint,
diff checks and one scoped design detector passed. Chrome synthetic light/dark
captures at desktop and phone sizes verified card geometry, status/error text,
44px phone targets, model price bounds, keyboard/focus behavior, no page overflow
and zero original-content requests. Independent Standards and Spec review both
returned CLEAR / APPROVE, no blockers. The phone bottom-button pointer path was
obstructed by BrowserSkill; keyboard expansion passed and the obstructed case
is not claimed as passed. No paid image generation was performed for this change.

## Production activation and smoke

Run `42521329456` completed with `conclusion=success` for exact source
`e57b617dcf7818d993b639af2a49265f1ca437a9`. Read-only Caddy/runtime resolution
selected healthy `sub2api-green` at that exact OCI revision, restart count 0,
no OOM and background active. Both public www/API health endpoints reported ok.
Final slot inspection found blue exited/standby and legacy absent. The stored
retained-drain override remains false and the background release mode activate.

Authenticated real Chrome at 1440×900 loaded the new 4:4.5 preview canvas and
84px metadata footer, with no alerts or horizontal overflow. The phone at
390×844 used the 96px footer, 44×44px card actions and 44px platform controls.
Real error text remained inside its caption. The horizontal bar reached the
last platform, and long Seedream names/prices fit. Screenshot inspection covered
the actual desktop, phone OpenAI/Gemini and final Seedream picker states.

| Live platform | Visible picker entries |
| --- | --- |
| OpenAI / Image | 3 |
| Google Vertex / Gemini | 4 |
| Muse | 1 |
| 通义万相 | 2 |
| 豆包 Seedream | 2 |

The four Gemini entries are `gemini-2.5-flash-image`, `gemini-3-pro-image`,
`gemini-3.1-flash-image` and `gemini-3.1-flash-lite-image`. The backend raw six
entries remain intact; only equivalent preview aliases are hidden by the
existing picker contract. This confirms presentation, not a new provider probe.

The original page and a subsequent warm reload showed zero original-content
requests and six decoded preview samples with longest edge 480px. Warm reload
restored the Image selection and gallery; sampled model metadata requests took
335–1900ms in this browser/network session. That is a bounded sample, not a WAN
benchmark or HTTP cache-hit-rate measurement. The browser did not download an
original image; no paid image generation was submitted for this UI acceptance.

Owned BrowserSkill sessions are closed and ports 3018/3048 have no listener.
Local fixture/Vite processes were stopped; the pre-existing node_modules symlink
and ignored local evidence were preserved.

## Recovery

The previously healthy `1a6b2ef27` application image is the compatible fallback.
Use only the canonical lock-owning recovery path in
[deploy/README.md](../../deploy/README.md), preserving any draining connections,
single background ownership and all current runtime-mount checks. Retain the
account-quota/idle-plan compatibility already delivered in that version. Do not
restore a database or delete jobs, originals, stored previews or financial data.
This increment has no schema, provider-activation or price change.

Private live screenshots, model output and raw operational evidence remain local
and uncommitted. The final completion-record commit may follow this runtime
release with documentation only.
