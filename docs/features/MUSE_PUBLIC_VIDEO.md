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
structured `first_frame` / `last_frame` constraint. Image and video endpoints
still accept text prompts; the Muse group does not serve text conversations.

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

Keep the exclusive Muse group and existing keys. Configure the exact allowlist
and native account identity mappings for `muse-image` and `muse-video` only.
After deploying the media-only gateway, remove `muse-spark` from the allowlist,
account mappings, and the linked channel's model entries and active price cards.
Preserve existing key grants, media prices, bindings and historical usage.

### Media-only gateway policy

The gateway identifies the dedicated OpenAI group from an enabled allowlist
containing only the exact Muse identities. It also recognizes the legacy
`muse-spark` entry during rollout. A request cannot use a media model name on a
text endpoint: Chat, Messages, Responses (including subpaths and WebSocket),
Codex, and all other non-media execution routes return a local 404. Model-list
and usage reads, native images and edits, image batches, video generation, and
owned task status and content still work. The public model list filters out
the retired text model before returning results, even with stale configuration.

Any OpenAI API-key account configured for native Muse identity media is also
ineligible for generic OpenAI dispatch, even if it is attached to a mixed
group. Only the native public media and the existing native image batch
transports can send on that account through the OpenAI gateway. Already-created
tasks retain binding, authorization, idempotency, and settlement behavior.
These checks neither change private Muse2API access nor sandbox the browser
agent that receives media prompts. Use a separate Muse account/runtime if the
public media account would otherwise have access to a personal computer.

For an authorized release, verify the two-model list and native task reads,
and check local text rejections before doing any paid smoke. Do not send a
real text prompt to test upstream acceptance. Older gateway binaries do not
understand the route restriction: removing `muse-spark` alone is insufficient
to roll back safely. Pause public Muse access before returning traffic to such
a binary. No historical charges are recomputed.

### Historical production model split — 2026-11-13

Unrelated image models were moved to one exclusive group per model; an account
can serve several such groups, each with a one-model allowlist and its own
prices. Image Studio aggregates the user's eligible owned keys, so there is no
need for a common umbrella group.

That cutover staged four exclusive groups, preserving each price card and
attaching only the matching provider account. It left them ungranted pending
separate owner authorization.

| Exact model allowlist | Existing image prices (CNY at 1×) |
| --- | --- |
| `wan2.7-image` | 1K/2K ¥0.30 |
| `wan2.7-image-pro` | 1K/2K ¥0.60 |
| `doubao-seedream-5-0-lite-260128` | 2K/4K ¥0.32 |
| `doubao-seedream-5-0-pro-260628` | 1K ¥0.40; 2K ¥0.70 |

The then-current three-model Muse allowlist was applied before the two foreign
account bindings were removed. Existing Muse keys, unrelated memberships and
historical tasks were retained. Restoring foreign access to Muse is a separate
permission decision.

Production configuration verified on 2026-11-13: Muse group 27 retains its two
keys and two user grants, with only account 227. Wan groups 41–42 share account
252; Seedream groups 43–44 share account 253. Each of the four destination
groups has zero grants and zero keys. The public Muse list returned only the
three allowed models; requests for all four foreign models received the local
group-policy 404. The protected pre-change snapshot is under
`/opt/sub2api/backups/muse-model-split-20261113T144751Z/`.

Wallet trial rates at 1×: Muse Image ¥0.10/output and Muse Video ¥0.06/second
(¥0.30 for 5 seconds). The now-retired Spark trial charged ¥0.005 per successful
request because its adapter reported zero tokens on both text endpoints.
The standard OpenAI wallet policy applies the multiplier
directly to the numeric reference price and settles the actual debit in CNY.
The video price is model-specific at 720p. Existing image and video multiplier rules apply.

### Model Plaza presentation

Link an active channel to the existing Muse group and list the exact identities
`muse-image` and `muse-video` (identity mappings suffice; no channel price
overrides are needed). The dedicated group appears under **MUSE**, independently
of the **openai** filter. Model Plaza suppresses stale text entries even when
an old channel mapping or price card is still present. The group's gateway
platform and grants are unchanged; an exclusive Muse group is still visible
only to eligible users. Mixed and prefix-matching groups are not reclassified.
The plaza does not discover or auto-publish account models.

Image quotes use the same per-output wallet price and tier fallback
as native image admission. Video displays the effective per-second quote at the
720p billing tier, including group model-card precedence; it does not invent a
quote from a channel's per-request card when no usable per-second price exists.
Display applies the existing group/personal or independent media rate; no price
or runtime policy is changed by viewing the plaza.

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

## Native media parity production acceptance — 2026-11-14 CST

Media support was activated at `b427226151d13c34910bfff2d54b245b1c1c3058`
([build 43511316864](https://github.com/Turtle-Li/sub2api/actions/runs/43511316864)).
The first live ledger read showed that the generic price resolver divided the
authored Muse image amount by the reference exchange rate. Revision
`f7a95716dee07f7afbe93b4992374dfc744013c0`
([build 43514906192](https://github.com/Turtle-Li/sub2api/actions/runs/43514906192))
fixed the wallet calculation and was activated on green. All three origin
health probes and public health passed. The normalized Caddy configuration and
Muse group membership, account and rates were preserved.

- A synchronous image with a public URL reference returned a 1600×1600 WebP.
  A multipart edit accepted two PNGs plus a WAV and returned a 2096×1152 WebP;
  the Base64 result matched the authenticated download byte for byte.
- A gzipped video request with two ordered PNG references returned a six-second
  H.264/AAC MP4 (1280×704, 144 video frames, full decode passed). Both compositions
  appear in the output, with differences from the supplied frames. The ledger
  recorded exactly one 0.36 wallet debit at 1× (usage row 1017342).
- After the price fix, two submissions with the same image idempotency key
  returned the same receipt. Polling and downloads left exactly one 0.10 wallet
  debit at 1× (usage row 1017484). Earlier smoke images stayed readable without
  repricing (rows 1017318 and 1017328). Anonymous downloads returned 401; an
  unrelated valid Key received 404. A foreign model and a private reference URL
  were rejected before forwarding.
- A local envelope check verified 100 MiB of decoded references over gzip;
  production did not need a maximum-size generation. The gateway and targeted
  race tests passed, as did a local Postgres check against active and archived
  billing receipts.

The preceding blue image can still serve media using the former image pricing
calculation. Preserve all task receipts and wallet rows if it is needed for
emergency rollback; verify the Muse image charge before considering billing
accepted on a rollback image.

## Pinned reference

Current reference: [`czg86389-hub/muse2api` revision
`b60f474977f125210eb7c71b0fc6668b2f1dd700`](https://github.com/czg86389-hub/muse2api/tree/b60f474977f125210eb7c71b0fc6668b2f1dd700),
MIT. Inspect `app.py` (`ImageRequest`, `VideoRequest`, `_reference_list`,
`_queue_image`, `images_edits`, `create_video`, and result endpoints), `store.py`
(task IDs), `engine.py` (reference types, duration, URL validation and media
filenames), and `tests/test_muse_video_delivery.py`,
`tests/test_ref_image_safety.py` and `tests/test_video_late_attachment.py`.
The old one-reference contract was pinned to `8a3dab9db13b23ac694d21af29e65504bc7e69b1`.
The pinned `chat_completions` and `responses_api` handlers route by endpoint,
not the request's model name, and report zero token usage. Sub2 integrates
only the native media HTTP contract; it does not copy the browser engine.
Ownership, pricing and adapter tests are in `grok_media_slots_test.go`,
`muse_media_request_test.go`, `muse_image_test.go` and `muse_video_test.go`.
Media-only admission and catalogue tests are in `gateway_muse_media_only_test.go`,
`muse_media_policy_test.go` and `model_plaza_muse_test.go`.
