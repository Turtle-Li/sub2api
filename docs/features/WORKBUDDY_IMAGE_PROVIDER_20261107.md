# WorkBuddy Hunyuan image batches

Local adapter; disabled until an explicit readiness mode is selected. No production activation or
paid image generation is part of this change.

## Source contract and reuse

Reference: https://github.com/misakano7545/workbuddy2api-panel at
`a19c78fc143dce790041071c18e1c7705a5a4f1e`.
Read `internal/server/images.go`, `images_test.go`,
`images_openai_spec_test.go`, `internal/upstream/images.go` and its tests.
Reuse the existing WorkBuddy gateway as-is for account/OAuth/realm routing.
Sub2 only wraps its JSON HTTP image endpoints; existing Sub2 sequential
manifest/attempt markers, wallet reservation, result storage and settlement
remain authoritative. The bounded image decoder and URL fetcher are reused.

Public models are exactly `cn:hunyuan-image-alpha`,
`cn:hunyuan-image-alpha-edit`, `global:hy-image-3.5`. The edit model requires
one reference; the others allow none. No automatic model substitution occurs.
Every model requires its own explicit same-name account mapping and positive
image price. Account platform is OpenAI, type API key, base_url is WorkBuddy.
No new account pool or OAuth implementation.

## Explicit readiness modes

Both account-extra flags default to false. Discovery and submission require
at least one literal boolean `true`; missing, non-boolean and false values do
not enable their mode. Credentials, exact same-name model mapping, platform,
price and normal account admission remain required in either mode.

For the owner-approved canary, use the official runtime with its automatic
updates intact and set `workbuddy_image_native_retry_accepted: true` on only
the approved account. This attests acceptance of upstream retry behavior,
not verification of single-attempt behavior. Leave
`workbuddy_image_single_attempt_verified` absent or false for this mode.
Do not pin or patch the official gateway as an activation prerequisite.
The fixed revision above is a reviewed source reference, not a runtime pin.

At that reference, `internal/server/images.go` sets `imageAttempts=3` for
generation/edit and hosted image tools. It may repeat a paid request after
an ambiguous failure; official updates may change this behavior. Thus one
Sub2 POST may consume extra upstream account quota, even if no image reaches
Sub2. Sub2 does not replay an uncertain submission and uses its existing
single settlement ledger for delivered results; it does not bill each hidden
gateway attempt or promise exactly one upstream generation. Responses does
not provide a single-attempt workaround at this reference.

The optional controlled-runtime alternative remains
`workbuddy_image_single_attempt_verified: true`, an operator attestation
requiring actual single-POST verification for network loss/500 on generation,
edit and hosted-tool paths. The reference patch
`docs/references/workbuddy/image-single-attempt.patch` supports that alternative
only; it is not required for official-runtime mode. If a controlled runtime
changes, reverify it or clear its verified flag. Neither flag is a remote
version probe. No updater or deployment is changed by this adapter.
Patch source is MIT; complete notice retained in adjacent LICENSE.

## Intentional differences and limits

The pinned JSON parser has no documented max-reference count or model size
allowlist. This integration conservatively supports one edit reference up to
8 MiB (matching upstream multipart byte bound), and n=1. The 1K selector is a
single billing tier, not a pixel-size promise; size/aspect are not sent upstream.
No upscale or mask UI. Output may be PNG/JPEG/WebP; full decode limits 20 MiB
and 64 Mi pixels. URL fallback uses existing public-host-only fetching with
account proxy and no bearer forwarding; invalid proxy fails before submission.
Shared account slots cover submission plus image download, timeout ten minutes.
Any ambiguous attempt remains non-replayable through the existing marker.
No endpoint idempotency guarantee is assumed.

Validation evidence: `.omo/evidence/workbuddy-image-implementation.md`.
Official-runtime opt-in evidence: `.omo/evidence/workbuddy-official-mode/report.md`.
