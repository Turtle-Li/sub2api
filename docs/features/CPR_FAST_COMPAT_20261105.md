# Codex OAuth relay Fast compatibility

This account setting lets an administrator declare the response-tier contract
of a verified Codex OAuth relay, such as Codex Proxy RS (CPR), while retaining
the account's OpenAI API-key authentication type. It does not enable Fast or
prove that OpenAI delivered faster execution.

## Configuration and scope

1. Keep the CPR key's group Fast policy at **Default**, so it preserves Sub2's
   final outbound tier.
2. On the OpenAI API-key account, enable **Codex OAuth relay compatibility** only
   when its upstream follows the pinned Codex OAuth response contract below.
3. Use the existing Sub2 group's **Force Fast** switch to request priority. The
   global Fast filter/block policy remains authoritative.
4. The existing group **Free Fast** switch controls customer charging independently.

The account declaration applies to every group using the same credential.
It never injects priority. An ordinary request on that account stays ordinary;
another group's independently requested Fast traffic uses the declared response
interpretation too. A group-specific rollout can use a dedicated relay account.
No account URL, name, header or response automatically enables compatibility.

## Stored account contract

`extra.openai_codex_oauth_relay` is a strict JSON boolean. Absence means false.
Only full administrator account writes can enable it, and only for an OpenAI
API-key account. Restricted administrator keys retain their existing prohibition
on arbitrary Extra writes. Strings, numbers, objects, arrays and null are rejected.
The credential type, tokens, forwarding identity and scheduling rules are unchanged.

Normal create/import and duplicate creation share validation. Ordinary updates,
key-level Extra merges and bulk merges validate the same setting before writing.
Bulk enabling validates every selected account before any update. Full account
snapshots and the existing transactional outbox already include Extra; no second
cache path or database migration is needed.

For a normal map-replacement edit, an omitted declaration preserves the stored
value. An explicit false disables it and is persisted by the create/edit forms.
When an administrator explicitly changes the credential type away from API key,
the inherited declaration is removed. Explicit true on an unsupported final type
is rejected; false permits cleanup of obsolete metadata. A false merge on an
unsupported account is inert and cannot enable compatibility.

CRS synchronization applies the same strict declaration rules before each
account create/update. An eligible OpenAI API-key resync preserves an omitted
stored true or false; an explicit false stays disabled on later resyncs. Moving
to OAuth or another platform removes the inherited declaration, so returning to
API key does not restore it. Invalid explicit values or unsupported explicit true
fail that item without saving it; other items continue. Existing long-context
normalization, managed billing-probe protection and unrelated Extra merges remain
in place. The Console source still has no imported Extra field; its update path
clears an inherited declaration without adding a new source contract.

## Tier settlement and audit

The shared OpenAI billing resolver receives the final outbound tier and separately
observed upstream response tier after resolving the complete credential account.
An opted-in relay reuses only the existing native OAuth exception for **default**.

| Final outbound | Observed response | Account contract | Settled tier |
| --- | --- | --- | --- |
| priority / fast | default | Explicit relay opt-in or native OAuth | Outbound tier |
| priority / fast | default | Ordinary API key / opt-out | default |
| priority | flex | Any | flex |
| default or absent | priority | Any | Outbound tier; never upgraded |
| priority | missing / unknown | Any | priority, as before |

Explicit `standard` and other known cheaper response tiers remain authoritative
for a declared relay. The declaration neither rewrites upstream responses nor
changes HTTP/WebSocket tier capture. Raw response observations remain on the
forward result; usage `service_tier` continues to represent the settled accounting
tier.

At the usage boundary, `billing.service_tier_retained` records `component`,
`account_id`, `platform`, `request_id`, `requested_tier` (final outbound),
`response_tier` (observed upstream), `billed_tier` and `policy_reason`.
The relay reason is `codex_oauth_relay_default_echo`; native OAuth uses
`codex_oauth_default_echo`. Existing downgrades keep the
`billing.service_tier_downgraded` event. Ordinary unchanged decisions add no event.
Only tier metadata and identifiers enter this audit, never credentials or bodies.

Free Fast retains the priority accounting tier and upstream cost basis, while
customer charging uses Standard under the existing group rule. Group rates,
model pricing and consumption adjustments keep their existing behavior.

## Request-list presentation

The shared `UsageServiceTierBadge` renders a compact lightning icon with the
accessible name **Fast** in the existing cost cell when the saved request tier
normalizes to priority/fast. It follows the amount, before the existing
long-context marker and cost-details icon, using the same inline group and gap.
It covers each original row in the admin usage table and the user usage page,
which already reuses that table. Plain, mapped and mapping-chain model layouts
share the same cost indicator; the model column has no Fast icon. It is visible
without opening cost details, including Free Fast rows. No page, route or
statistics feature is added.

The badge never reads current group switches. Default, standard, flex, ultrafast,
unknown and untiered rows receive no Fast badge. The tooltip describes the saved
tier and points to actual charge details without promising provider acceleration
or a surcharge. The public Key Usage page contains only aggregate statistics,
so it has no per-request row to mark and remains unchanged.

Historical rows are not reclassified. A row already settled to default cannot be
reliably reconstructed from that field alone. Rollback is an explicit false on
the relay account; existing history and group switches are retained.

## Fixed upstream reference

Repository: [zyycn/codex-proxy-rs](https://github.com/zyycn/codex-proxy-rs), revision
`e0b7a841ca074f17e11dad73f39a159c75cb2fb1` (CPR 3.38.1), Apache-2.0.
The following source and test files were inspected at that revision before the
local implementation, against Sub2 baseline
`14ed48e89bebe6407537211acabd77788dd4b69e`:

- [`responses.rs`, `CodexResponsesRequest::apply_fast_policy`](https://github.com/zyycn/codex-proxy-rs/blob/e0b7a841ca074f17e11dad73f39a159c75cb2fb1/backend/crates/providers/openai/src/transport/protocol/responses.rs#L711):
  Default preserves the outbound request. Enabled/Disabled are distinct policies.
- [`responses_should_observe_and_bill_the_outbound_service_tier_on_both_transports`](https://github.com/zyycn/codex-proxy-rs/blob/e0b7a841ca074f17e11dad73f39a159c75cb2fb1/backend/crates/providers/openai/tests/provider/contract/mod.rs#L7788):
  priority/default retains priority accounting on HTTP and WebSocket; metadata
  separates requested/upstream tiers; raw SSE/WS response content is preserved.
- [`routing/snapshot.rs`](https://github.com/zyycn/codex-proxy-rs/blob/e0b7a841ca074f17e11dad73f39a159c75cb2fb1/backend/crates/gateway-core/src/routing/snapshot.rs#L537):
  bound group policies contribute the client Fast mode.
- [`observability/presenter.rs`, `format_service_tier`](https://github.com/zyycn/codex-proxy-rs/blob/e0b7a841ca074f17e11dad73f39a159c75cb2fb1/backend/crates/gateway-api/src/admin/observability/presenter.rs#L173):
  priority/fast present as Fast.
- [`UsageBillingCell.vue`, Fast lightning indicator](https://github.com/zyycn/codex-proxy-rs/blob/e0b7a841ca074f17e11dad73f39a159c75cb2fb1/frontend/src/components/usage/UsageBillingCell.vue#L140):
  lines 140–148 render a 12px lightning icon inside a 16px circular indicator,
  with a tooltip and an accessible image name.

Adopted behavior: preserve the final requested tier for the explicitly declared
Codex OAuth default-echo contract, retain the separate observation, and expose
priority/fast as a lightning icon directly beside each original request row's
cost, with an accessible Fast name. This is a response/accounting contract,
not evidence of actual OpenAI acceleration or official charges.

Intentional differences: Sub2 requires an explicit API-key account declaration;
ordinary public API responses still lower charges; the exception covers only
default, while flex/standard downgrades remain; Sub2's group/global policies own
request injection and Free Fast customer billing. The icon uses Sub2's existing
`Icon` bolt and primary light/dark tokens rather than CPR's warning colors;
placement beside the cost follows the reference. CPR stays at Default. No CPR
implementation was copied: the Go resolver and Vue controls extend existing
Sub2 code. Any future code transplant must preserve Apache-2.0 attribution,
license and applicable NOTICE obligations, and recheck this fixed source/tests.

## Local validation ownership

Focused Go tests cover declaration validation and persistence, omission and type
changes, Extra/bulk/scoped-admin boundaries, public API and flex/no-upgrade
protection, structured decision audit, and actual RecordUsage Fast/Free Fast
accounting. Existing group-policy and HTTP/WS observation tests remain in scope.
`crs_sync_codex_oauth_relay_test.go` exercises the actual local HTTP export and
`SyncFromCRS` path for create/update strict types, opt-out/omission, unsupported
targets, all six platform branches, credential/platform round trips and per-item
failure isolation. The targeted race check also retains CRS long-context,
managed-probe, fixed-egress and shadow-parent regressions.
Vue tests cover both account forms, visible cost-cell indicators, model-column
exclusion and coexistence with long-context and cost details; user-page
composition and public aggregate regressions are also checked.
Independent QA owns real rendered desktop/mobile and light/dark inspection.
Local checks do not represent a production rollout or paid-model speed test.
