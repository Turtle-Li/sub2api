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
