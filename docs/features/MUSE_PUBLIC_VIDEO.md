# Muse public video and model isolation

Sub2 uses the existing OpenAI-compatible Muse account with an explicit
`muse-video → muse-video` mapping. The customer sends their existing Sub2 Key.
Muse2API remains private; its upstream base URL and credential stay server-side.

- Submit: `POST /v1/videos` with `{"model":"muse-video","prompt":"...","duration":5,"size":"16:9"}`.
  The first supported version is text to video, 5 seconds, landscape or portrait,
  on an OpenAI wallet group with media generation enabled.
  The 202 receipt is issued only after the existing Redis task owner and pending
  billing record are saved. An ambiguous create is never automatically replayed.
- Poll: `GET /v1/videos/{id}`. Use the same Key. Only a completed task advertises
  its Sub2 content path. Task metadata returned by Muse is not passed through.
- Download: `GET /v1/videos/{id}/content` with the same Key. The server reads only
  the native MP4 path at the configured account origin; upstream-provided URLs
  are not followed. Redirects are disabled and native media requests carry no
  API key. A valid bound Key can read its existing task after wallet or key
  quota depletion; expiry, revocation and group access restrictions still apply.
- `/v1/responses` is for text and rejects `muse-video` with the submit path.

The existing group permission, model allowlist, balance check, moderation,
account concurrency and billing dedup remain in force. A positive configured
720p group price is required. The tariff uses 5 requested seconds and the
existing 720p price tier, independent of the provider's actual output dimensions.
The first authenticated observation of completion (status or content) records
usage under one stable task ID. A task never observed by the customer does not
produce a charge under this existing video billing policy.

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

## Production acceptance — 2026-11-13

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

## Pinned reference

`czg86389-hub/muse2api`, revision
`8a3dab9db13b23ac694d21af29e65504bc7e69b1`, MIT. Inspected `app.py`
(`VideoRequest`, `create_video`, `get_video`, `get_media`, `responses_api`),
`store.py` (native task IDs), `engine.py` (media filenames), and
`tests/test_muse_video_delivery.py`. This integration calls the native task
contract. It intentionally restricts request fields, replaces the private media
URL with a Key-bound Sub2 path, and does not copy upstream implementation.
Redis ownership, price and adapter coverage live in `grok_media_slots_test.go`
and `muse_video_test.go`.
