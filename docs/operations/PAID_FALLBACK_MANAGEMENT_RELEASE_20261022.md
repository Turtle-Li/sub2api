# Paid fallback management release — 2026-10-22

## Released behavior and scope

Runtime source `8fecb5956cc9f0aab416c8cff49ae6f5507bccc5` implements the owner's group-wide reserve policy: every authenticated user in selected groups is eligible, ordinary recovery takes precedence over paid affinity, and account list/create/edit/bulk controls expose the reserve tier beside priority. See [the feature contract](../features/PAID_FALLBACK_MANAGEMENT_20261022.md).

The owner authorized deployment and isolated DeepSeek acceptance, conditional on an actual successful GPT call before traffic switches. Production GPT accounts and groups were not modified. The documented gosec G704 finding at unchanged `model_protocol_catalog.go:236` was accepted as a release exception after independent review identified the administrator-controlled URL and existing URL/DNS/redirect defenses. The finding remains visible; no lint suppression was added.

## Exact release and validation

- Production workflow [39798284420](https://github.com/Turtle-Li/sub2api/actions/runs/39798284420) succeeded for exact source `8fecb5956cc9f0aab416c8cff49ae6f5507bccc5`, version `0.2.17`, AWS target `aws-candidate`.
- Canonical root-owned scripts matched repository SHA256 values before dispatch. The real-request gate was enabled for `gpt-5.6-sol`, timeout180, with the existing root-only probe credential. Active-green authenticated models and Responses probes returned200 before dispatch.
- On the candidate blue instance, `switch.log` lines8–9 record authenticated models200/Responses200 after 22:55:11 CST. Caddy switching starts at line29 at 22:55:17 CST and commits at line44 at 22:55:18 CST. The actual GPT probe therefore passed before traffic changed. The host-side gate aborts before Caddy mutation on probe failure and does not depend on the agent remaining connected.
- Release evidence directory: `/var/log/sub2api-release/gha-20261022-075435-8fecb595-939721` on documented alias `sub2api-aws-candidate`; application image `sub2api:auto-20261022-075435-8fecb595`, active slot `sub2api-blue`.
- Actual released archive: SHA256 `2cb8dae4301bb01a10b6683494b03f11d03ab9fd4195be570ef34a22b42cccf4`, `87112896` bytes; host-loaded image ID `sha256:1bd22aa7bf198093a7d20c3bbff9b4396c316fef7f96d244054a2cc1fb079ca8`. These values come from the canonical receiver's `candidate.env` after its digest and OCI identity checks.
- Blue is healthy, zero restarts/OOM; API/www loopback-pinned HTTPS health both returned200. No retained release transaction remains. Caddy selects only blue.
- Old green connections drained 4→1→0. The canonical monitor stopped green at 22:57:23 CST only after observing zero connections; no manual stop or drain override was used.
- Migrations `264_paid_fallback_operations.sql` and `264a_paid_fallback_usage_index_notx.sql` applied. Their stored checksums match the runner's SHA256-of-trimmed-SQL calculation. The concurrent paid-usage index is valid and ready. Initial audit tables were empty.
- Post-switch observation through 23:04 CST saw67 GPT usage records. The read-only GPT error-log query for 22:45:18–23:04:41 returned no rows;666 new-runtime log lines had no panic/fatal or migration failure. This is bounded observation, not a claim that every transport or future request has been tested.

Source validation before release: full backend unit/integration, frontend tests/build/lint, focused race checks, browser fixtures, source repairs rereview and canary review passed. Current-source CI [39768313991](https://github.com/Turtle-Li/sub2api/actions/runs/39768313991) failed only on the documented G704 finding; security run [39768315833](https://github.com/Turtle-Li/sub2api/actions/runs/39768315833) passed. The earlier build-only artifact is separate from this actual production workflow.

## DeepSeek acceptance boundary

The reviewed isolated canary has not run. `infra-vault unlock-run -- run-item-env-file` failed local task-scoped biometric authentication and exited64 before a session key or test credential was injected. Vault status returned `locked`, biometric enrollment remains present, and the non-secret request file was deleted after the bounded attempt. The owner has been asked when to retry local verification. No credential is requested in chat and no bypass is permitted.

At the final read-only check, both fixture keys and both fixture accounts remained disabled, paid policy was off for fixture group33, attempts remained0, and notifications were absent/default off. No management supplier acceptance or human email was sent.

Resume only the pending DeepSeek acceptance; do not redeploy this unchanged runtime. Consumer `.omo/evidence/paid-fallback/live-test.py` SHA256 is `c7c9f0b061ad7bc41d8b2f56f85118781b9a7f707eadf8e3c2de42e72f1eb65f`. Recreate the owner-only metadata request from the private credential reference, obtain intentional local Vault verification, run its bounded `smoke`, and retain cleanup/401/source-fingerprint evidence. Test only fixture user81/group33/WB237/Cline238/keys131+132 and the owned outside-policy control group. Do not modify production GPT accounts/groups or enable customer paid scopes.

## Rollback

Use the canonical verified-image/server release helpers under the shared maintenance lock. Retained prior runtime source is `7c34b50abb400cb66fc0bb554dc15b7ba1a07879`; its image is `sub2api:auto-20261022-033505-7c34b50a`. Do not manually change Caddy or stop active containers. Keep policy/notifications off, paid markers and audit history intact. An older binary that cannot isolate reserve markers requires disabling and unbinding reserve accounts first. Do not drop the new audit tables or undo financial history.
