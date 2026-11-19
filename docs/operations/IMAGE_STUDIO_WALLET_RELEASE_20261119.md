# Image Studio wallet and unified pricing group

## Verified existing production routes

The earlier workbench used the site login plus an existing **Sub2API user
Key ID**. It did not require the user to provide a provider's API secret.
Wan and Seedream upstream accounts #252 and #253 had already been installed
from the previous image site. No new upstream credentials are needed for this
rollout. A production read-only check matched the original report:

| Created (Asia/Shanghai) | Model / tier | Batch | Images / internal debit |
| --- | --- | --- | ---: |
| 2026-11-09 00:07:07 | Wan 2.7 / 2K | `imgbatch_cd719b8d3eae6471cbc235aaaeccf355` | 0 / 0 |
| 2026-11-09 00:07:08 | Wan 2.7 Pro / 2K | `imgbatch_581fc9a2b57f64e7e7daecdf3cdfaf74` | 1 / 0.60 |
| 2026-11-09 00:07:09 | Seedream 5 Lite / 2K | `imgbatch_aa00977dc87f28148cbcf9d4b3f4496e` | 1 / 0.32 |
| 2026-11-09 00:07:10 | Seedream 5 Pro / 2K | `imgbatch_933500f9daae62b1e33f1faa68ba0a34` | 0 / 0 |

Their old output lifecycle has ended. That is distinct from the original
failure. The two zero-debit attempts have incomplete write/receipt evidence;
neither proves expired credentials or that upstream never accepted anything.
See [the original release](IMAGE_STUDIO_LOAD_NATIVE_RELEASE_20261108.md).
The November 19 release also verified exact Image 2.6 on account #249 and
one real Vertex archive after delivery repair; use the
[models and history notes](../features/IMAGE_STUDIO_MODELS_AND_HISTORY_20261119.md)
for their boundaries. Gemini stages in managed GCS and delivers to private
Tencent COS, not Claude Files. The existing 360-hour app-level output deadline
must not be confused with the COS bucket's independent safety lifecycle.

## Activation boundary

1. Deploy the default-off source via the [AWS release runbook](../../deploy/README.md)
   without replacing Caddy. Keep the original keys, group output defaults,
   entitlements, account bindings, prices, objects and persisted deadlines.
2. Reuse existing standard exclusive group #45 (`生图`) for workbench admission
   and pricing. Preserve its existing user and account #251. Ordinary API
   account bindings must stay unchanged: #249 can serve text, #227 can serve
   text and video, and #3 can serve Gemini text. Select #249, #3, #227, #252
   and #253 only in the new Studio-specific account list after checking current
   capability, scheduling and non-reserve status.
3. Add only verified exact model/tier cards. Copy the native tariffs from the
   existing cards: Muse 1K 0.10; Wan 1K/2K 0.30, Pro 1K/2K 0.60; Seedream
   Lite 2K/4K 0.32, Pro 1K 0.40 and 2K 0.70. Match current effective quotes
   for Gemini and exact GPT variants before admitting them. Do not use the
   historical Gemini 2K debit or the shared 0.20 default as a general tariff.
   Bare relay aliases are not proof of official variants, nor a reason to alter
   existing gateway compatibility.
4. Verify active and recovery images both understand the wallet owner slot and
   migration 277 before enabling shared work; draining generations must stay
   standby. Before incompatible recovery, pause the wallet setting, drain
   in-flight submits, and verify no unsettled wallet job or reservation. Keep
   compatible recovery for continued access to new history. Do not delete the
   migration, jobs, ledger or objects to return to the older generation.
5. Use the CAS in **生图管理** to select the group and Studio accounts. Require an
   owner-authorized test login admitted to the existing exclusive group; do
   not impersonate its existing customer or create a Sub2API Key as a workaround.
   Test one request per chosen model from the page. Read back the task, exact
   debit or refund, preview/original and 360-hour deadline. A missing response
   is not permission to submit the same generation again.

The [session contract](../features/IMAGE_STUDIO_SESSION_API_20261107.md#session-only-wallet-generation)
defines request auth and isolation. Go unit, route and PostgreSQL tests cover
admission, old history, keyless charge/release and idempotency. A Chromium
fixture exercises login-only submission, Wan/Seedream-specific prices, history,
and image decoding with no request using a key. This evidence is synthetic and
does not certify a paid generation on the new production path.
