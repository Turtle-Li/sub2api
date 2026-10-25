# Kimi K3.1 Pro channel configuration

Owner-authorized Admin API change, 2026-10-25. Production remains the AWS
`sub2api-aws-candidate` origin, `/opt/sub2api`, fork/main. Public SSH endpoints
were unavailable during this session; existing Registry Tailnet endpoints were
reachable. SSH used the existing aliases, device-local identities and pinned
public-endpoint host-key aliases; no host-key checking was disabled.

- Account 221: retained pre-existing `k3.1-pro` mapping; added canonical
  `kimi-k3.1-pro` → `cline-pass/kimi-k3.1-pro`. Credentials, group membership and
  `cline_upstream_pins` preserved. No duplicate account created.
- Group 18 KIMI: appended both names to the enabled model allowlist; retained
  rate 0.3. No change to other groups, keys or peak settings.
- Channel 3: added one CNY token card for both names: per million tokens input
  13, output 68, cache read 1.3, cache write 5m 13, write 1h 26. No tiers or time
  schedule. Existing K3 card retained. The short alias is hidden from plaza to
  avoid a duplicate card, following the existing K3 alias convention.

Sources: [Cline live Pass catalog](https://api.cline.bot/api/v1/ai/cline/recommended-models),
[Kimi official native pricing](https://platform.kimi.com/docs/pricing/chat),
checked 2026-10-25. Ordinary Cline credits were not enabled.

Observed acceptance:

- Account test: HTTP 200, `test_complete.success=true`, mapped upstream exactly
  `cline-pass/kimi-k3.1-pro`, no SSE error.
- Existing owner group-18 key: `/v1/models` 200 lists both canonical and short
  names; `/v1/chat/completions` 200 returns canonical K3.1 Pro and real usage
  (89 prompt, 32 completion, including 23 reasoning tokens), no JSON error.
- Public plaza API 200: group 18, rate 0.3, canonical card visible; official
  reference values normalize to exactly CNY 13 / 68 / 1.3 / 13 / 26. Billing
  compatibility API values remain USD-normalized, using active FX 6.75.

Reversal: remove only the new canonical account mapping, both newly added
group allowlist entries, the new channel card and its short-alias hidden entry.
Keep the short account mapping that existed before this task. Use Admin API to
retain cache/outbox invalidation; do not restore a historical database snapshot.

The channel editor's automatic-pricing code is tracked separately in
`docs/features/CHANNEL_PRICING_AUTOFILL_20261025.md`; this live configuration
does not require a binary release.

## Channel editor release and acceptance

Released source `9a7cf06d0bba68d33a2b08f0f06675195a6913d4` through
[production workflow 40185841154](https://github.com/Turtle-Li/sub2api/actions/runs/40185841154),
completed successfully at 2026-10-25 10:52:34 UTC. Image verification, the
main-commit freshness gate, blue-green release and AWS Caddy verification all
passed. Green is running and healthy with the exact source revision; the
previous blue revision `819dce3c016d3dc116473c2b43295a7d4d7d1a8a` is stopped and
retained for the canonical rollback procedure.

Post-release read-only acceptance passed:

- Authoring CNY endpoint returns 13 / 68 / 1.3 / 13 / 26 per million tokens.
- Unknown family names and unauthorized K3.1 date/namespace aliases return
  `found:false`; Kimi model suggestions contain the canonical model once.
- Account Pass mapping, group 0.3 multiplier and the exact CNY channel card are
  preserved. Public plaza shows one canonical K3.1 card and hides its short alias.
- Public `/model-plaza` loads `ChannelsView-CLoP2M7_.js` and
  `PricingEntryCard-8nq9AlDV.js`. The latter contains both new default-price and
  group-pricing notices; SHA-256 is
  `f7ff66267df8516629abfba1655c3ae63135cd5777f8a9c89a5124461be20391`.

Validation before release: 99 related frontend tests, locale completeness (3),
typecheck, ESLint, Vite build, focused backend race tests, broad conventional
pricing/channel unit suites, Go build/vet and independent review passed.
Real-component browser QA covered automatic fill and manual edit protection in
light/dark modes. Production browser inspection timed out; production checks
therefore use APIs and the exact served component bundle. Optional expanded
race testing found an existing shared-Account parallel-fixture race outside this
change; see the feature contract for the evidence boundary.

The binary release can be reversed through the installed canonical image
rollback flow using the retained 819dce3c0 revision, after its existing readiness
and health gates. Keep current account/group/channel data: this feature adds no
schema migration, and its live model configuration also works on the previous
binary. Do not restore a historical database or use retired Azure hosts.
