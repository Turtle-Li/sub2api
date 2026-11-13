# Muse public image and video

Send the existing Muse group Sub2 Key to the public API. Sub2 schedules only
accounts explicitly mapping the requested Muse model to itself. Muse2API and
its credential remain private.

| Work | Submit | Result |
| --- | --- | --- |
| Video | `POST /v1/videos` or `/v1/videos/generations`, model `muse-video` | 202; poll `GET /v1/videos/{id}` (also `/v1/videos/generations/{id}`) |
| Image generation | `POST /v1/images/generations`, model `muse-image` | 200 OpenAI-style `data` on completion; `async:true` returns 202 |
| Image edit | `POST /v1/images/edits`, JSON or multipart, model `muse-image` | Same as image generation |
| Image task | `POST /v1/images/tasks`, model `muse-image` | 202; poll `GET /v1/images/tasks/{id}` |

The two image endpoints also support their existing `/async` aliases. Images
support `size`, `aspect_ratio`, `extra`, `timeout`, and
`response_format: "url" | "b64_json"`. Native Muse returns one image even if
`n` is larger than one. To request multiple independent images use the existing
Sub2 batch API, which has its own admission and reference contract. The
synchronous endpoint waits on the same native image task. If the wait expires,
it returns 202 and the existing task ID instead of starting another generation.
`Idempotency-Key` (up to 256 bytes) is isolated by group, user, Key and endpoint
and is routed back to the same upstream account during the receipt lifetime.

Video supports `duration` (default 6), `size`, `aspect_ratio`, `resolution`,
`timeout` and `extra`. Duration and dimensions are requests to Muse; the
generated file may differ. Upstream accepts ordered reference images as
guidance, including an ending composition in the prompt, and has no
structured `first_frame` / `last_frame` constraint. `/v1/responses` is for text.

Both media APIs accept `images` (which can mix types), `videos`, `audios` and
`files`, in that order. Items may be public HTTP(S) URLs, data URLs, raw Base64,
or objects such as `{"image_url":{"url":"https://…"}}`. The legacy single
reference fields remain accepted instead of `images`. For multipart image edits,
repeat `image`, `image[]`, `video`, `audio` and `file`. The upstream limit is
10 files, 40 MiB each and 100 MiB total; reference video must have a valid
duration of at most 120 seconds. Use public URLs or send gzip-compressed JSON
(`Content-Encoding: gzip`) if Base64 JSON would exceed the existing 128 MiB
public wire limit. The larger decompressed ceiling applies only to Muse media
paths on groups with the exact corresponding model in their allowlist.

The gateway fetches public URLs without credentials or a proxy and validates the
actual address at every hop before sending the same bytes to Muse. Files on the
server, private addresses and unsupported content types are denied. The current
security audit sees the prompt and each image separately. Muse still validates
the duration of input videos. No task metadata containing upstream account
details, internal prompts or upstream URLs is returned.

Download a returned URL or append `/content` to the status path with the same
Key. Sub2 verifies the bound account and reads only its native media path;
redirects are disabled and the native media request carries no API key. A valid
bound Key can still read after wallet/quota depletion. Expiry, revocation and
the ownership check still apply. The task lookup is retained at least 24 hours.

A positive configured media price is required to submit. Images use one
configured image unit and the existing image rate; completion settles before
returning image bytes or a result URL. A positive Muse CNY-authored tier or
default card takes precedence over the flat price at the same size. If the
requested size has no price, use the first positive configured tier in 1K, 2K,
4K order. The authored number is a wallet-unit price, as in Image Studio, and
is not divided by the exchange rate. The same quote is used for admission
and settlement. After a pricing change, the durable settlement record keeps
previously paid tasks readable without a new charge. Video retains the configured 720p rate
and bills the requested seconds within the supported billing range of 5–15,
independent of the actual output dimensions. First completed status/content
observation schedules one video charge. Failed or unobserved tasks do not
produce a debit under this policy. POST is not automatically replayed.

## Configuration and verification

Keep the exclusive Muse group and existing keys. Its exact allowlist is
`muse-spark`, `muse-image`, and `muse-video`. Move unrelated image models to
one exclusive group per model; an account supporting multiple variants may
serve multiple such groups, each with its own one-model allowlist and price
card. Image Studio aggregates only the user's eligible owned keys, so these
groups do not require a common umbrella group.

For the production configuration cutover, stage these four exclusive groups,
preserving each existing price card and attaching only the matching provider
account. Leave them ungranted until the owner authorizes access separately.

| Exact model allowlist | Existing image prices (CNY at 1×) |
| --- | --- |
| `wan2.7-image` | 1K/2K ¥0.30 |
| `wan2.7-image-pro` | 1K/2K ¥0.60 |
| `doubao-seedream-5-0-lite-260128` | 2K/4K ¥0.32 |
| `doubao-seedream-5-0-pro-260628` | 1K ¥0.40; 2K ¥0.70 |

Apply the three-model Muse allowlist before removing the two foreign account
bindings. Preserve the current Muse keys, unrelated memberships and historical
tasks. Verify both the public model list and cross-model submission admission;
an old Muse Key must get a model denial on a foreign request. In a partial
failure, keep the restrictive Muse allowlist and finish or disable ungranted
destination groups; restoring foreign access to Muse is a separate permission
decision.

Production configuration verified on 2026-11-13: Muse group 27 retains its two
keys and two user grants, with only account 227. Wan groups 41–42 share account
252; Seedream groups 43–44 share account 253. Each of the four destination
groups has zero grants and zero keys. The public Muse list returned only the
three allowed models; requests for all four foreign models received the local
group-policy 404. The protected pre-change snapshot is under
`/opt/sub2api/backups/muse-model-split-20261113T144751Z/`.

Wallet trial rates at 1×: Spark ¥0.005/successful request, Muse Image ¥0.10/output
(existing), Muse Video ¥0.06/second (¥0.30 for 5 seconds). Spark's adapter
reports zero tokens on both text endpoints, so the group uses a per-request
reference card. The standard OpenAI wallet policy applies the multiplier
directly to the numeric reference price and settles the actual debit in CNY.
The video price is model-specific at 720p. Existing image and video multiplier
rules apply. No historic charges are recomputed.

## Initial text to video production acceptance — 2026-11-13

Exact app revision `651dd4d6c732743d58e338041442c4b92eecfb62` is healthy on blue.
The application stage of [run 43449262546](https://github.com/Turtle-Li/sub2api/actions/runs/43449262546)
passed the real candidate probe and committed the blue-green switch. Its
separate Caddy-template sync rejected the existing independent proxy target
before mutation. The existing Caddy configuration serves the new app and
the video endpoint. A future template sync must preserve the independent route.

At the public API, the former text route returned 400 with the video endpoint
hint. One owner Key submitted a video (202), polled to completion (200), and
downloaded an H.264/AAC MP4 (200, 1280×720, 5 seconds, 762187 bytes, full decode
passed). Requests without a Key received 401; a different valid OpenAI Key
received 404. After the download and another poll, usage row 1016348 and one
durable dedup record showed a single 0.30 wallet debit at 1×, with a CNY
settlement marker. The four foreign models were all denied on the Muse Key.
Monitor unpolled native work before expanding beyond the initial group.

## Reference image production acceptance — 2026-11-14 CST

Revision `185fe30b4ad00d2b7bbe0c91f259c72dc7afb037` was built in
[run 43488941641](https://github.com/Turtle-Li/sub2api/actions/runs/43488941641)
and activated on green by the canonical image receiver. The three origin
health checks returned 200. The existing Caddy configuration matched after
normalizing the active application slot; the independent route was preserved.

The public API rejected a remote reference with 400, accepted one inline PNG
with 202, and served the completed H.264/AAC MP4 with 200. Full decode passed:
5 seconds, 1280×704, 24 fps, 504952 bytes. Early and middle frames showed
the uploaded illustration's distinctive shapes and their movement. Anonymous
and different-key downloads were denied with 401 and 404. Repeated reads
left one dedup entry and one 0.30 wallet debit at 1× (usage row 1016941).

## Pinned reference

Current reference: [`czg86389-hub/muse2api` revision
`b60f474977f125210eb7c71b0fc6668b2f1dd700`](https://github.com/czg86389-hub/muse2api/tree/b60f474977f125210eb7c71b0fc6668b2f1dd700),
MIT. Inspect `app.py` (`ImageRequest`, `VideoRequest`, `_reference_list`,
`_queue_image`, `images_edits`, `create_video`, and result endpoints), `store.py`
(task IDs), `engine.py` (reference types, duration, URL validation and media
filenames), and `tests/test_muse_video_delivery.py`,
`tests/test_ref_image_safety.py` and `tests/test_video_late_attachment.py`.
The old one-reference contract was pinned to `8a3dab9db13b23ac694d21af29e65504bc7e69b1`.
Sub2 integrates the HTTP contract and does not copy the browser engine.
Ownership, pricing and adapter tests are in `grok_media_slots_test.go`,
`muse_media_request_test.go`, `muse_image_test.go` and `muse_video_test.go`.
