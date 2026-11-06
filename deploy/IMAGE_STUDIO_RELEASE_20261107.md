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
