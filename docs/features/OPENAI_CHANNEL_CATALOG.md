# Manual GPT and Claude display model sync

## Pinned upstream and local contract

Baseline: Turtle-Li/sub2api `0cdf5342c`, integrating Wei-Shaw/sub2api v0.2.23
at `5af9afb588c4b014b3df9db7d4073af1f8ae01b6`. Compared upstream
`backend/internal/service/channel_available.go`, `channel_available_test.go`,
`model_plaza_service.go`, `openai_gpt62_test.go`, the Model Plaza
`PlazaGroupSection.vue` / `__tests__/PlazaGroupSection.spec.ts`, and
`backend/internal/handler/admin/channel_handler.go` and
`frontend/src/views/admin/ChannelsView.vue` / `__tests__/ChannelsView.modelSync.spec.ts`.
Keep the upstream union of mapping/price entries, platform filtering, tier
display, existing LGPL notices and direct integration history. Upstream's
pricing-catalog sync still imports pricing model names; this feature previews
group account model names for display only. Neither replaces billing.

- In **Channel pricing → Edit → OpenAI**, click **Sync GPT display models**.
  The administrator sees additions and removals by group before saving the
  channel. The preview is read-only; closing the dialog discards it. Changing
  the draft groups, mappings, prices, or restrictions invalidates the preview.
- Only the button reads active, schedulable OpenAI accounts for the selected
  groups. This reflects local configuration, not an upstream health check or
  a model request. A lookup error aborts the preview rather than treating it as
  an empty group.
- Saving stores just the selected group/model names in
  `features_config.gpt_display_models_by_group`, no schema migration. Model
  Plaza and Available Channels read those saved names; ordinary views and
  subsequent account edits do not refresh them. No background task or polling.
- Only exact GPT account identities are inferred. No wildcard expansion,
  alias/target rewriting, paid-fallback capacity, or unpriced text family
  guesses. Respect group allowlists, the channel's hidden models and image
  admission at preview and at display time. Skip the supplement for restricted
  channels, unlinked groups, and groups with a pinned upstream catalog.
  Manually configured models and their pricing remain unchanged. Edits that
  omit the snapshot preserve it. To undo a sync, restore the previous snapshot
  (save an empty object if there was none).
- GPT image prices appear under a separate heading. For per-image billing the
  plaza uses the effective group quote, with the independent image rate if
  configured. An explicit model-level fixed group card still wins, and a group
  that forces per-image billing on the dedicated endpoint shows that quote even
  if its channel card is token-based. Identical 1K/2K/4K rates are displayed
  together; differing and zero-priced tiers are not dropped. Otherwise,
  token-priced images do not get a fixed-price label, and text models that
  accept image input stay in the text table. Keep the site's `$` wallet-unit
  display symbol even when settlement is CNY.

Discovery performs one local account lookup per eligible group per admin
preview. No account lookup is added to public catalog reads. The earlier
unpublished automatic option has no effect.

## Claude group reconciliation

**Channel pricing → Edit → Anthropic → Sync Claude display models** previews both
additions and removals for the currently selected, active Anthropic groups.
Saving applies the preview; closing it or changing the draft groups, mappings,
pricing, or restrictions does not save it. Nothing is scheduled. Like GPT,
the button is unavailable when channel model restrictions are enabled.

Claude sync reconciles the whole group's display, including hand-entered
aliases, identity mappings, and pricing-only rows. A row stays only when the
group allows the public name and an eligible account supports the effective
channel-mapped name. Exact `claude-*` entries from eligible account mappings
can add new names; unrestricted or wildcard configurations can retain known
names but do not create new ones. This is local configuration, not a provider
health probe; transient overload and rate limits are not removals. A lookup
error aborts the preview, including any partially processed groups.

`features_config.claude_display_models_by_group` is independent of the GPT
supplement. A missing group entry leaves manual rows unchanged; a saved empty
array hides the whole Claude list for that group. No upstream request, account
write, price change, group attachment change, or migration is involved. Model
Plaza applies the saved list per group; Available Channels merges it only from
groups accessible to the viewer. Newly synced rows use existing display prices
when available; sync does not create a price. Ordinary channel edits preserve
both snapshots; run Claude sync again to include newly configured manual rows
in a synced group. To undo a Claude sync, restore the previous snapshot, or save
an empty **object** (not an empty group array) to revert to the manual list.

## Initial production observation and bounded change

On the current production configuration, `OpenAI 广场展示` is a display-only
channel with four linked GPT groups, no channel price overrides and no model
restriction. A currently schedulable account bound to all four groups already
declares `gpt-6.2-sol` and `gpt-6.2-astra`. This is configuration evidence, not
a generation smoke test. The existing public amount-based group (#6) uses
text rate 0.3 and independent image rate 1; the Model Plaza API quotes 0.20 CNY
at 1K/2K/4K for `gpt-image-2`, `gpt-image-2.5-flare`, and
`gpt-image-2.5-sunburst`.

Until the binary is released, the narrow interim change is to add two identity
mappings for GPT-6.2 to that display channel and leave image prices, group
membership, account config and existing manual rows unchanged. Read back the
public catalog after saving. Roll back by removing only those two added rows.
Do not add channel prices or change account routing to publish these models.
