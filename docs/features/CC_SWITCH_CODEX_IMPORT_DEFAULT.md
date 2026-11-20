# CC Switch Codex import default

Under **System Settings → General → Site Settings**, administrators can set
the **Default Codex model for CC Switch** (`ccswitch_codex_model`). An omitted
or null update keeps the stored value. A blank value resets it to `gpt-6.2-sol`;
missing settings also fall back to that ID. Use a model supported by the OpenAI
group assigned to the key. The setting does not change group model routing.

The public settings endpoint and HTML bootstrap expose the same non-secret
model ID. New **API Keys → Import to CCS** links for OpenAI groups write it to
both the `model` URL parameter and the embedded Codex TOML. Existing imported
providers, other platforms, the API base URL, WebSocket settings, and manual
**Use Key** templates are unaffected. This does not include the earlier
optional API endpoint selector or any Cloudflare/DNS change.

This is an additive entry in the existing settings table. It needs no schema
migration or production setting mutation to take effect; a prior image ignores
the entry on rollback.

## Pinned reference

The Sub2API reference remains `Wei-Shaw/sub2api` at
`a60a29549f488a854966aaec9541abbe006cac22`, see
`frontend/src/utils/ccswitchImport.ts` and its test
`frontend/src/utils/__tests__/ccswitchImport.spec.ts`. Retain the provider URL
and single `/v1/usage` normalization, plus the fork's full UTF-8 Codex settings.
The editable model ID intentionally replaces the upstream fixed default. Keep
the existing LGPL-3.0 license and source notices.

CC Switch's consumer contract is checked against `farion1231/cc-switch` at
`1d5d90f4aba88447d422a16cdec5282ec5331fd7`, in
`src-tauri/src/deeplink/provider.rs` and `src-tauri/src/deeplink/tests.rs`.
Its Codex import accepts the model URL parameter and can read the model from
embedded TOML. No CC Switch client code is copied.
