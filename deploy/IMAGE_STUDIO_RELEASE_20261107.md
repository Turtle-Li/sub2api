# Image Studio production release

Owner authorization on2026-11-07: commit to the owner's main branch, deploy through the existing production path and test actual image generation. Subsequent owner clarification requires preserving WorkBuddy official automatic updates; do not pin its runtime version or deploy the local single-attempt patch as part of this release.

## Source and release boundary

Base is `ce5f17142704f0fbd269b76b7b9d2a266a4a6f20`; fetch confirmed fork/main unchanged before preparing the commit. All92integration files passed the earlier source/QA gates. Additional Dockerfile/.dockerignore/bridge README packaging changes passed independent review and native plus linux/amd64 real image-layer checks. Installed Wan/Seedream modules run as UID1000 with network disabled and no credentials.

Target: existing `sub2api-aws-candidate` SSH alias, Sub2-owned `/opt/sub2api`, source `https://github.com/Turtle-Li/sub2api.git` main. The existing GitHub workflow `sub2api-production-deploy.yml` and its `sub2api-production-deploy` concurrency group serialize release runs. The host receiver and canonical `/run/sub2api-maintenance/sub2api-maintenance.lock` serialize upload/activation. No separate local queue or invented coordination backend is introduced.

Read-only preflight: Caddy selects healthy `sub2api-green` at the base revision; zero restarts/OOM. Previous blue is stopped at `32adb35ba`. Background mode `activate`, real request probe enabled, pre-existing draining-container override false. Production performs no source build. Preserve canonical drain/readiness/rollback and existing payment/Feishu/day-pass/upscale agents.

## Runtime and canary plan

The immutable image now contains Python3.12 and locked native bridge dependencies under `/opt/sub2-image-bridge`. Backend default remains disabled until the exact interpreter is intentionally configured; no new mount or privilege is required.

Use minimal single-image requests through Sub2's existing task and ledger paths, with stable submission IDs and no automatic replay after unknown outcomes. Discover actual configured models first. Read existing protected machine injection only inside bounded consumers; never collect a human website password, print keys, or commit runtime configuration.

WorkBuddy currently exposes `cn:hunyuan-image-alpha` and its edit model on official revision `a19c78fc143dce790041071c18e1c7705a5a4f1e`. This is an inspected contract reference, not a runtime pin. Preserve official automatic updates and native retries. The separate default-false `workbuddy_image_native_retry_accepted` enables that behavior explicitly for the canary; never mislabel it as single-attempt verified. Sub2 retains one client POST after uncertain outcomes and one ledger settlement, while native gateway retries can consume additional upstream quota. Nine admission cases, both-mode recovery tests and shared race regressions passed; independent review approved the five-file change.

No schema migration was added by Image Studio. Keep existing financial history; provider refusal/quota does not by itself authorize DNS or application rollback. Actual commit/run/image, live task and billing results are appended after execution.

## Execution checkpoint

Fetched concurrent main `0c699b5dd9892608443f436c3ec68f806f693b71` (passive monitor), preserved both AGENTS sections and automatically merged the independent router additions. Combined frontend production build, image/monitor route middleware tests and server/repository compilation passed. The monitor release run42297682332 completed successfully before the Image Studio push.

Feature commit: `4649ae0d2a320ea2c9744c0a86ee0787e8c83d40`, pushed fast-forward to `Turtle-Li/sub2api:main`. Explicit production run: https://github.com/Turtle-Li/sub2api/actions/runs/42299363458 . No WorkBuddy runtime/update change and no production bridge activation were made. The optional bridge is packaged but Wan/Seedream have no verified Sub2 credentials yet.

Temporary live canary uses exclusive group40, existing owner1 key127 (original group27), existing Muse227/Image249 accounts and a separate OpenAI-compatible WorkBuddy routing account250. Native retry acceptance is true only on that routing account. Its positive0.01 test image tariff is an isolated settlement probe, not customer/provider pricing. Restore key/group memberships and disable the temporary group/account after acceptance; retain task and billing history. Runtime/API secrets remain in memory-only consumers under local ignored `.omo/release/`.

## First activation and live findings

Run42299363458 succeeded. Caddy selects healthy `sub2api-green`, revision4649ae0d2, image `sha256:ff3f9315b0761e3a20be37a654b2ef3542402ec1d63ac6ed6154ebb1a2a52950`, zero restarts. Installed bridge capabilities execute successfully with two models per provider; activation remains off.

Image2.5 Sunburst task `imgbatch_d60ab240b3cd825ffae582f024032636` completed with one1536x1024 PNG (SHA256 `86fbe1c65ea19795548fc52d11fecad56d655c725f382620f6cb38f4ba93b2fc`). Exactly one hold and one capture, actual debit0.01 internal units, no duplicate ledger entry. Owner's existing Chrome session displays its real image in the new gallery; screenshot retained locally under `.omo/release/live-gallery-image25.png`.

Native Muse/WorkBuddy admission exposed two missing production-boundary regressions: public service wiring still passed nil gateway, and catalog pricing ignored the group image tier override. Both fail before upstream generation; the rejected native attempt IDs have zero persisted jobs. Fixes inject the existing shared gateway and reuse the existing submission pricing snapshot, with constructor and real-pricing-resolver failing-to-passing regressions. See `docs/bugs/backend/BUG-20261107-image-public-native-runtime.md` and `docs/bugs/backend/BUG-20261107-image-catalog-group-price.md`. Key127 is returned to group27 while the correction is reviewed/released.

Separate pre-cutover Gemini baseline `imgbatch_e6c1f53be7b90340f3790d267d2d35b4`, submitted on0c699b5, failed in the unchanged delivery pump boundary (`RESULT_DELIVERY_FAILED`). One hold was fully released, zero capture. Read-only signed pump GET using Go's actual User-Agent returns application404 for the run; Python's default UA instead triggers Cloudflare1010 and is not evidence of the production cause. Original failure detail was not retained, so exact delivery failure cause remains unproven. No generation retry or authentication/pump configuration change was made.

## Corrected release and completed acceptance

Correction `4cce05dd203d5989939156b3d9124b2344877717` includes the two production-boundary fixes. Native race regression:61 scenarios, zero failures/skips; real resolver catalog regression:35 top-level tests, zero failures/skips. Independent review: CLEAR/APPROVE. Concurrent monitor documentation was fast-forwarded before the correction; no unrelated source change was introduced.

[Production run42302982486](https://github.com/Turtle-Li/sub2api/actions/runs/42302982486) succeeded. Active `sub2api-blue` reports that exact revision, healthy with zero restarts; image `sha256:b7ed6a3c67a67fdfeeb2e05027737e728d67c22bd4367fab95a486a25973a227`. Native catalog now advertises Muse, both observed CN Hunyuan identities and existing Image2.5 models using the canary group's configured image price. No WorkBuddy image/updater pin or patch was applied.

| Real task | Model / outcome | Result | Ledger |
| --- | --- | --- | --- |
| `imgbatch_d60ab240b3cd825ffae582f024032636` | Image2.5 Sunburst text generation; completed | PNG1536x1024, one red boat | One hold, one capture,0.01 |
| `imgbatch_ae405b8d04e4aa00dfcbf2403946c80a` | Image2.5 Flare edit through owner's Chrome session; completed | PNG1536x1024, yellow boat with source reference; persisted parent is preceding task | One hold, one capture,0.01 |
| `imgbatch_acb384bfa312ccd2caf704b115b4113f` | Muse native persisted task; completed | WebP1920x1280, one image | One hold, one capture,0.01 |
| `imgbatch_9cb2b2957d3c9d55f22ac35ca0c8dff1` | Official WorkBuddy CN Hunyuan; completed | PNG1248x1872, one image | One hold, one capture,0.01 |

All amounts above are isolated test internal units, not customer/provider tariffs. Four captures total0.04; no duplicate ledger entries. Image bytes were decoded/validated locally. Muse's first concurrent GET hit the existing user download limiter429; a subsequent sequential GET succeeded. No generation was repeated for that download response.

Owner's existing authenticated Chrome session verified model selection, server-owned gallery/history, original-image reference loading, editing submission, persisted parent linkage, result preview and billing display. Local evidence: `.omo/release/live-gallery-all-providers.png`, `live-edit-preview.png`, and per-task `results/*/{image,audit,status,resume}.json`. Images and machine consumer scripts stay ignored; no session or API key was exported to the browser or repository.

Scope limits remain explicit: Gemini baseline failed in the pre-existing delivery boundary with its hold released; this release does not claim a Gemini success. Wan and Seedream native cores/runtime passed source and packaging tests, but production activation remains off because no verified reusable native credential reference was available (Vault was locked; no unlock or VSO secret extraction performed). The temporary test prices do not establish public pricing or enable all providers for ordinary users.

Cleanup verified from database: key127 restored to group27; account227 groups `[27]`; account249 groups `[4,6,8,12,13,16]`; canary group40 and routing account250 inactive; zero unfinished canary jobs. Historical task access still succeeds after restoration. Keep task/ledger records and generated files; do not erase financial evidence. Formal native platform exposure/prices remain administrator configuration decisions, separate from this isolated acceptance.

## Group image administration

[Production run 42313044021](https://github.com/Turtle-Li/sub2api/actions/runs/42313044021)
activated source `9e18ead867895f5935ab667e246cbd4d7c180ebe` on
`sub2api-blue`. The active image was
`sha256:68354866ee88b566634961b3dc196b4b5e69102d7545b5f704b5f84d3707a81b`,
healthy with zero restarts. Public health returned OK; the anonymous admin
catalogue request returned HTTP 401. The previous healthy revision was
`58b71f4f1e215dd2462f75c59377b14ada7c7875`.

In the authenticated admin browser, the new sidebar link and page loaded.
Switching to Muse showed its saved batch-disabled state and the Muse candidate;
switching to Gemini showed separate batch discount and hold controls plus six
models available by configuration. The save button remained disabled because
no configuration was edited. This release adds no migration and activates no
provider. See `docs/features/IMAGE_STUDIO_ADMIN_20261107.md` for ownership and
rollback details.
