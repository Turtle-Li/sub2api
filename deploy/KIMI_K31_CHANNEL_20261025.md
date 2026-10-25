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
