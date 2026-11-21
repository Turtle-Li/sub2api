# Images & Image Studio: Office Mini first, Aliyun on failure

2026-11-21 — implementation and local acceptance, **not production activation**.

## What previously existed

The owner's `video-skill-orchestrator` (VSO) at
`4edef85ab3716fd43989b4a49561a148ba500d93` implements Shanghai ImageEnhan in
`skills/candidates/engineering.6-general-product/product-image-generation/src/hosiery_image_skill/upscale.py`
(blob `0bb36277de80b78b5ee9a86d0bfd6ba4e0b72397`). Its `tests/test_upscale.py`
(blob `bffde0f14ea5b41e5efccd35f1a0780312b96a37`) covers one standard cloud
call, technical validation, normalization, trusted output hosts and no redirect.
The working copy additionally contains the Mini queue-full fallback test and
`docs/development/OFFICE_MINI_UPSCALE_FALLBACK.md`; these local changes are **not**
evidence that this route shipped in VSO or Sub2. Sub2's earlier public Images
and Studio batch shared a Mini-only client; object-storage fallback was never a
second image processor. The Go adapter implements the HTTP contract, not copied
VSO code. Aliyun's Go module `imageenhan-20190930/v3@v3.0.0` is Apache-2.0;
retain the dependency license/notice. Reference methods: `client/client.go`
`MakeSuperResolutionImageAdvance` (staging) and `client/client_context_func.go`
`MakeSuperResolutionImageWithContext` (processing).

## Behavior

- 1K and already-sufficient sources bypass processing. For supported paths the
  same actual-dimension 2×/4× decision, original aspect, requested-tier billing,
  owner authorization, storage and result lifetime remain in force. This does
  not opt native bridge jobs into a new postprocessor.
- Keep normal Mini queue fairness; an admitted short queue is not a cloud call.
  With the flag on, a full local queue or a Mini 429 transfers immediately.
  Transport errors, 5xx, a failed/persistently running accepted job, bad output,
  and result-download failures likewise transfer the **same** source. No replay
  of an ambiguous Mini POST. A late Mini direct upload can only target its own
  scoped object, not the cloud result or a second customer settlement.
- Reserve one third of the remaining per-item deadline (max 3 min) for cloud;
  a prompt Mini rejection leaves all remaining time available. The cloud has
  one independent process-wide slot and at most eight waiting sources shared
  by interactive and batch callers. When both queues are saturated, fail admission
  rather than keeping unlimited image payloads or regenerating the source.
  Cancellation also cancels slot waits, upload, processing and download. Do
  not start a new paid request on caller cancellation or invalid source data.
- Follow the SDK's `AuthorizeFileUpload` flow with a scoped Shanghai OSS form
  upload, then a single synchronous `MakeSuperResolutionImage` call with native
  `UpscaleFactor=2|4`. Explicitly propagate the caller deadline on all three
  steps (the generated all-in-one Advance helper has no Context variant).
  No paid POST retry, including when the reply is lost. The outer async task
  must not retry generation after cloud fallback was started. This is **not**
  an exactly-once guarantee across process death or a new user request.
- [Aliyun API contract](https://help.aliyun.com/zh/viapi/developer-reference/api-px24vm/):
  validate a bounded source first. If necessary fit the working copy to the
  1920-long / 1080-short / 5 MiB envelope; pad only dimensions below 32.
  Validate bytes and native output dimensions before mapping that working
  geometry back to the original × scale. Preserve PNG when supplied; the
  service may return JPEG for large outputs. Download the temporary signed
  result only from exact approved Shanghai output hosts, over HTTPS without
  redirects. Never expose that short-lived URL to the gallery. Use the normal
  durable output/fenced writer, not a second storage or billing pipeline.
- If neither processor succeeds, fail the affected item truthfully: never mark
  a raw 1K or partial output as a delivered 2K/4K. Logs identify the Mini and
  cloud failure stage, not prompts, response bodies, credentials or signed URLs.

## Activation and rollback — separate authorized release required

### AWS host-owned credential profile

The owner authorized using the former VSO image site's exact Shanghai
`HOSIERY_ALIYUN_UPSCALE` credential without a new Vault item. This exception
does not change the existing Mini or COS credentials. The AWS host retains
only `systemd-creds` ciphertext at
`/etc/sub2api-credentials/sub2api-aliyun-image-upscale.cred` (`root:root`, `0600`);
the original VSO runtime remains the recovery source. Re-provision on host
replacement rather than copying the AWS systemd host key. Do not put the
credential in the release `.env`, Docker environment, source, or backups.

1. Validate the exact enabled VSO provider row, endpoint and the two fields on
   the source host. Pipe only that JSON over the verified SSH paths to
   `deploy/sub2api-image-upscale-aliyun-host-credential.sh` as root on AWS;
   the installer rejects a pre-existing credential and duplicate JSON keys.
2. Under the AWS maintenance lock, verify the local driver, exact canonical
   mountpoint and `1000:1000` mode `0700` on `sub2api_image_upscale_vault`.
   Create only its `aliyun` child directory (`1000:1000`, `0700`). Install
   the reviewed `./cmd/sub2api-vault-agent` Linux AMD64 binary and the host
   supervisor at the immutable paths in the dedicated systemd unit. Validate
   and enable that unit after confirming a restart restores readiness from
   the ciphertext. The systemd credential mount and admin socket are not
   shared with Docker. Only the scoped public socket is on the named volume.
3. Use the fixed config helper twice with `IMAGE_UPSCALE_ALIYUN_AGENT_SOCKET=/run/sub2api-upscale-vault/aliyun/public.sock`
   and `IMAGE_UPSCALE_ALIYUN_ENABLED=false`, then `true`. The socket selects
   the independent one-ref host agent and leaves the three-ref Mini/COS
   agent untouched. `ready-aliyun-auto` must prove both through the mounted
   sockets before releasing the matching application image. Test a single
   actual Aliyun upscale and a session-wallet Shanghai delivery before full
   user admission; do not force an outage on the production Mini to test it.
4. For rollback, stop new Shanghai admissions while keeping the controller
   URL and both agents available until all pinned jobs and ACKs reconcile.
   Only then disable/remove Shanghai routing and toggle Aliyun off with the
   same two-field helper and a guarded blue-green release. A blue-green
   reversal validates the retained target's previous admission and Aliyun
   settings instead of imposing the failed release's desired settings.

### Legacy combined Vault profile (not used by this host)

1. **Default off.** Stage the root-owned release/guard/sidecar helpers alongside
   the matching build. Verify a dedicated RAM identity is entitled to Shanghai
   ImageEnhan and the SDK upload flow. Do not use Wan, CAPTCHA or Tencent COS
   credentials. The project has not verified production entitlement/injection.
2. Provision the specific Vault reference
   `vault://secret/data/sub2api/aliyun-image-upscale#credentials`: one single-line
   JSON object with exactly `access_key_id` and `access_key_secret`, max 512
   bytes, read/injected atomically. No keys in Git, Compose, app environment
   or Docker metadata. Register and approve the hash-pinned injector for this
   purpose in the private Registry/Vault **before** enabling.
3. Stage by piping the single line `IMAGE_UPSCALE_ALIYUN_ENABLED=false` to
   the root-owned `sub2api-image-upscale-config.sh`. This installs an independent
   guarded block with `SUB2API_IMAGE_UPSCALE_ALIYUN_VAULT_PROFILE=true`; the
   original 12-line Mini block stays intact. The networkless
   memory agent uses the same volume, socket and four-ref ceiling, adding only
   the one fixed field to the historic Mini+COS profile. The helper refuses to
   replace a running legacy agent. Coordinate any profile migration/reinjection
   under the documented maintenance lock, including existing Mini/COS consumers.
   Use the `aliyun` injector action (secret only through stdin at the remote
   boundary), then the existing `all` action or independent Mini/COS loads.
   Readiness requires all allowed fields. No credential is read on Mini success.
4. On a **separately authorized** cutover, with the standard Mini block enabled,
   pipe `IMAGE_UPSCALE_ALIYUN_ENABLED=true` alone to the same helper, then
   follow `deploy/README.md`. Release and recovery require `ready-aliyun-auto`
   (cannot pass with the three-ref sidecar or an old helper). A live controlled
   2K/4K cloud smoke, one stored output and the expected single ledger entry
   remain release checks; local mocked transport is not proof of live service.
5. To stop cloud spending use `IMAGE_UPSCALE_ALIYUN_ENABLED=false` and the
   normal guarded release. Keep the explicit staged profile, shared Mini/COS
   socket, stored results and ledger intact. Pre-feature containers without a
   cloud flag are compatible only when cloud is off. Do not restore a database,
   delete gallery assets or change DNS to disable this feature.

## Local checks

`TestImage26AliyunFailoverDelivery` runs the real Studio result indexer with
an isolated object store and the synchronous Images response adapter for Mini
success, local saturation, 429, 503, disconnect, accepted failure and deadline.
`TestAliyunUpscaleSignedLifecycle` exercises the real signing SDK on a fake HTTP
transport, including 4×, an ambiguous paid POST, trusted staging and binary
result validation. `TestAsyncImageHandlerDoesNotRegenerateAfterCloudFallback`
checks the async retry loop. Deployment tests cover both exact agent profiles,
raw-secret rejection, readiness and rollback compatibility without contacting a
provider or logging in to any host.

## Release handoff

The mock Image 2.6 paths produced one 2048×1024 delivered output from the
1024×512 fixture per request; on a Mini success cloud submissions remained zero.
On failures the gallery wrote the final bytes once; the sync API returned those
same bytes. No image was generated on either live provider during these checks.
Linux fake-Docker config, memory-agent injection/profile, blue-green and runtime
recovery tests passed. The Studio frontend's 15 suites (122 tests), typecheck
and production build passed; the desktop/mobile preview showed the request tier
without changing the decoded original's aspect ratio. No release was dispatched.
