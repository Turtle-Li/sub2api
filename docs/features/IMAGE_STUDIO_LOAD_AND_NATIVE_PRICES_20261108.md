# Image Studio load and native channel prices

## Scope and owner decisions

Follow-up to the November 8 persistent-thumbnail release. Integration baseline:
`012e65a7f6c21ff22c59a5773c02b2c8c04f6dc5`. The owner authorized fixing slow
entry/model/gallery loads, correcting Gemini prices, enabling Muse/Wan/Seedream,
reusing the existing oneclick VSO accounts, and committing/releasing to production.
The owner explicitly chose server-side protected runtime/account configuration
instead of creating new Vault entries for those reused provider credentials.

No new task table, provider retry loop, or billing ledger is introduced. Existing
batch jobs/items, ownership checks, immutable price snapshots, persistent JPEGs
and explicit original downloads remain authoritative.

## Loading contract

- The session key directory is a dedicated credential-free SQL projection. It
  includes `can_generate` and `has_history`, without secret material, last-used
  IP lookups or live concurrency enrichment. History checks match both owner
  and original key and exclude user-deleted jobs.
- New clients only discover models for eligible image keys and only query
  history-bearing keys for gallery pages. After a successful submission, its
  original key joins that workspace's history set. Missing metadata falls back
  to the old client behavior during rolling upgrades.
- Model and gallery reads are independent, bounded to six concurrent requests
  each. One catalogue request reuses its group, user rate and per-platform
  account reads. Nothing is cached across requests that could preserve revoked
  access or stale pricing; submission still reads current configuration.
- Browser covers use three independent slots, abort handling and the existing
  64-entry/16 MiB Blob limit. Bounded 429/503 retries affect previews only.
  Opening/downloading an original remains explicit and uses a separate queue.

## Pricing contract

Token-billed Gemini image outputs convert the authored output-token price into
one image before existing group/user/account multipliers. Configured image/tier
quotes still take precedence. Supported output counts are pinned in
`batch_image_token_count.go`; unknown conversions fail closed. Existing captured
job prices are never rewritten.

Native models on standard OpenAI wallet groups can use the existing per-model
group pricing cards with `currency: CNY`, image/per-request billing and resolution
`tier_label`. Their numeric CNY-authored amount already has the same internal
unit basis as the existing group image price; do not apply another FX conversion.
Exact model cards override the group default. Other platforms and billing paths
retain their existing semantics. Native batch discount/hold stay at 1 for these
formal tariffs; account/image rates are 1.

| Model | Size | Official output CNY/image | Configured internal units/image |
| --- | --- | --- | --- |
| Muse `muse-image` | 1K | owner price | 0.10 |
| Wan `wan2.7-image` | 1K / 2K | 0.20 | 0.30 |
| Wan `wan2.7-image-pro` | 1K / 2K | 0.50 | 0.60 |
| Seedream `doubao-seedream-5-0-lite-260128` | 2K / 4K | 0.22 | 0.32 |
| Seedream `doubao-seedream-5-0-pro-260628` | 1K | 0.30 | 0.40 |
| Seedream `doubao-seedream-5-0-pro-260628` | 2K | 0.60 | 0.70 |

Checked November 8 against the corresponding Beijing official products:
[Wan model pricing](https://help.aliyun.com/zh/model-studio/model-pricing),
[Wan Pro](https://help.aliyun.com/zh/model-studio/wan2-7-image-pro),
[Ark model pricing](https://docs.volcengine.com/docs/ark/model-pricing?lang=zh&redirect=1),
[Seedream pricing table](https://docs.volcengine.com/docs/82379/1544106?lang=zh).
Pro's 2.61M-pixel boundary places all pinned 1K sizes in the lower band and all
pinned 2K sizes in the higher band. Seedream Pro also has additional reference
input charges (first reference free, later references CNY0.02 each). These fixed
output tariffs absorb that input cost at platform level; they do not claim a
constant CNY0.10 margin for multi-reference requests or add a hidden surcharge.

Google conversion references:
[Gemini API pricing](https://ai.google.dev/gemini-api/docs/pricing?hl=en),
[Vertex pricing](https://cloud.google.com/model-garden/pricing),
[Flash image specifications](https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/gemini/3-1-flash-image).

## Native activation and rollback

Retain bridge/VSO source pin `4edef85ab3716fd43989b4a49561a148ba500d93`, exact
model identities, workspace/Ark endpoint policies, original HTTP cores, shared
account concurrency and no replay after an uncertain submit. The native Python
runtime is already packaged; Go invokes its Python with `-m sub2_image_bridge`.
The executable setting defaults empty in code. Explicit host activation sets
`batch_image.python_bridge_executable` to
`/opt/sub2-image-bridge/.venv/bin/python-native-tariffs-v1` in the protected data-volume
`config.yaml`, preserving every other YAML value and its owner/mode.

Only the four active, exact VSO provider configurations are read. Their secrets
are decrypted in source-host memory, captured privately through verified SSH,
and sent through the existing authenticated TLS admin API to Sub2 native
accounts. No source account changes, local credential files, plaintext output,
new Vault entries or generation calls occur during provisioning. The same
credential/endpoint is combined into one Sub2 account to share its admission
limit. Accounts are prepared without group bindings, then only bound to group27
after the exact reviewed revision is healthy and the formal model prices saved.
Muse keeps its existing account227; canary group40/account250 is not activated.

Before mutation, save protected host backups below
`/opt/sub2api/backups/image-studio-native-20261108/`. Config writes hold the
canonical maintenance lock. Application lifecycle changes remain in the manually
dispatched exact-main GitHub workflow and canonical blue-green helper. Retain
old connection drains and compatible current rollback images. Rollback can
unbind only the newly added accounts and restore only changed group/image-card
settings; retain jobs, provider receipts, financial rows and all other channels.
Clear the explicit bridge activation before intentionally disabling the runtime.

The tariff-aware Python entry point exists only in images containing this
pricing contract. An older rollback image cannot execute the configured path,
so Wan/Seedream fail closed instead of falling back to Muse's group default.
Muse's legacy group default remains0.10 and compatible. Do not replace this
versioned opt-in with the unversioned Python path while native CNY cards are live.
Both native capability operations run through this exact entry point in the
Docker build, as UID1000 with networking disabled.

## Verification before release

- Go: scoped batch-image/studio/native tariff tests pass across service,
  repository, routes, handler and middleware; focused service/repository race
  tests pass.
- Frontend: 90 tests across 15 studio/admin files pass; Vue/TypeScript and
  production build pass. Existing large-chunk warning remains informational.
- Correctly configured Gemini regression tests fail the old token-as-image
  implementation and pass the fix. Cover concurrency likewise has red/green
  evidence.
- Real Chrome against the loopback synthetic fixture: 16 keys result in five
  model requests and five gallery requests, with different eligible sets and
  zero original-content requests. With model responses delayed 8s, the first
  gallery cover is visible in 1022ms while model selection is still disabled;
  normal reload first cover is visible in 681ms. These are fixture timings,
  not a claim about production RTT. Platform tabs and model prices were viewed.

Evidence lives under this task's `.omo/` logs and
`.omo/evidence/gallery-optimization/followup-*.png`. Production activation,
real-provider acceptance, active SHA, workflow, rollback and observed timings
must be recorded in the release report after execution.

Independent Claude review is unavailable: the official CLI returned HTTP503
`No available accounts`. Its follow-up process was stopped when the owner
updated the unavailable-model rule; no further Claude calls or substitute model
API calls are made. This is not recorded as a passed independent review. Codex
continues the remaining checks and release under the owner's explicit instruction.
