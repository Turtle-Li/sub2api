# HiAPI Seedance 2.0 Mini — isolated production pilot

The exact public and upstream model ID is `seedance-2.0-mini`, using HiAPI's
default route. See the HiAPI [model contract](https://hiapi.ai/zh/models/seedance-2.0.seedance-2.0-mini),
[task protocol](https://hiapi.ai/docs/async-api/) and
[balance API](https://hiapi.ai/docs/account-balance/) (baseline 2026-11-20).
The internal reference is the GoodPut durable reservation/settlement flow at
`ad590e8422244444e494df473453658399fcf66e`: `backend/internal/service/goodput_video_jobs.go`,
`backend/internal/repository/goodput_video_jobs.go` and their regression tests.
No external code was copied. HiAPI has its own task table, worker, ID namespace,
strict request schema and status contract. Do not point the native Ark adapter
at HiAPI or reuse H3's group, prices or request schema.

## Setup

- Reuse the existing OpenAI API-key account with `https://api.hiapi.ai[/v1]`;
  its only model mapping is `seedance-2.0-mini` → `seedance-2.0-mini`. Keep its
  saved key in Sub2. Use this account only in the dedicated video group.
- Use an exclusive standard (wallet) OpenAI group, enable media generation and
  an exact allowlist containing only `seedance-2.0-mini`. Configure the explicit
  per-second 480p and 720p prices (and group multiplier) independently of H3.
  Do not publish the group or grant it to other users during the owner test.
- The official no-reference-video rates at the baseline are USD 0.017/s for
  480p and USD 0.036/s for 720p. These are upstream costs, not Sub2 points.
  Verify the live quote and the saved account's balance before an operator
  test. There is no automatic top-up, alternate line, or failover.

## Generate and read

Use the Sub2 group key, not the HiAPI key. Submit once:

```bash
curl -sS "$SUB2_BASE_URL/v1/videos" \
  -H "Authorization: Bearer $SUB2_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "model":"seedance-2.0-mini",
    "prompt":"A single continuous shot connects these two frames.",
    "duration":4,
    "resolution":"480p",
    "aspect_ratio":"16:9",
    "generate_audio":false,
    "first_frame_url":"https://your-public-assets.example/start.jpg",
    "last_frame_url":"https://your-public-assets.example/end.jpg"
  }'
```

Duration is an integer 4–15 seconds; resolution is `480p` or `720p`. Set
`aspect_ratio` (`16:9`, `9:16`, `1:1`, `4:3`, `3:4`, `21:9`, `adaptive`) and
the boolean `generate_audio` explicitly. Text-only and first-frame-only are
supported by the request schema. When supplying both frames, each must be a
public HTTPS image URL. The last frame requires the first. Use JPEG, PNG,
WebP, BMP, TIFF or GIF, each under 30 MB, sides 300–6000 pixels and aspect
ratio 0.4–2.5. No inline data URLs, reference-media mixing, callback, storage
override or alternate upstream routing in this integration.

`202` returns a Sub2 `hav_…` ID. Read `GET /v1/videos/{id}` until `completed`
or `failed`; `review` means do not resubmit. Download `/v1/videos/{id}/content`
with the same key. No native task ID, upstream credential or temporary result
URL is returned. MP4 bytes are fetched without the upstream Authorization
header, under the existing public egress checks with redirects disabled.
Same user, key, group and original account are required; task reads expire
after 24 hours.

## Billing and acceptance

The ledger freezes the explicitly quoted points before dispatch. A verified
success with a video output captures the quote and creates one usage row in
the active background process, even without a client poll. Verified failure
releases the hold. A lost submission receipt is fenced in review, not replayed.
The HiAPI idempotency key identifies that one reservation; an explicit new
Sub2 POST after a completed job is a new generation. Reads do not bill.

One production acceptance: with an isolated ordinary-user Key and sufficient
credits, submit 4s / 480p / 16:9 / audio off with visibly different non-personal
input images. Wait for the Sub2 usage row **before** its first task or content
read, verify ownership and a single charge after reads, and decode the actual
first and last output frames. Check the upstream debit separately. Never use
the admin model-connectivity test or a direct HiAPI generation as a second
trial. If a task is accepted but unconfirmed, investigate the same ID and do
not send another job.

To stop, remove admission to the exclusive group, leaving the provider account
and receipts available to the worker until drained. Do not roll back to a
binary without HiAPI settlement while pending HiAPI holds exist. An older
binary does not claim HiAPI jobs and still understands the GoodPut table;
never restore an older database or drop either ledger.
