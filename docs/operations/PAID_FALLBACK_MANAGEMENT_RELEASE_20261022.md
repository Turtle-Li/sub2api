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

## DeepSeek acceptance: PASS

After the first biometric attempt failed before credential injection, the owner explicitly requested a retry and completed local verification. The unchanged, reviewed consumer `.omo/evidence/paid-fallback/live-test.py` (SHA256 `c7c9f0b061ad7bc41d8b2f56f85118781b9a7f707eadf8e3c2de42e72f1eb65f`) completed `smoke` through the bounded Vault injection with exit0. All supplier calls used `deepseek-v4.1-flash` and exclusive fixture user81/group33/WB237/Cline238/keys131+132; the outside-policy control used exclusive group34.

| Scenario | Observed result |
| --- | --- |
| Policy off, ordinary healthy | WB237,200, ordinary usage, no paid attempt |
| Policy on, ordinary healthy, streaming | WB237,200 with content, no paid attempt |
| Ordinary login fault, policy on | Cline238,200 with content, one paid attempt |
| Second key in the same group | Cline238,200, one paid attempt; no key allowlist |
| Second key outside enabled groups | 503, no paid attempt |
| Policy off, ordinary login fault | 502, no paid attempt |
| Ordinary fault, paid streaming | Cline238,200 with content, one paid attempt |
| Ordinary recovery with the same session ID | WB237,200, ordinary usage, no paid attempt |

The three new attempts have HTTP-header outcome200, group33/account238/model `deepseek-v4.1-flash`, and all belong to incident `e2cf23a2-b428-472e-96f5-78b4390a5ad6`, opened23:23:43 CST and resolved23:26:07. New usage IDs are911660,911662,911665,911669,911673,911676. The three paid rows estimate USD0.0000411; all six new usage rows estimate USD0.0000681. These are usage-snapshot estimates, not a reconciled supplier bill; older fixture usage is excluded.

Cleanup passed: both keys returned401, exact fixture auth caches were invalidated, both fixture accounts and groups33/34 are disabled, fixture user81 is inactive, and policy is off with group33 retained. Original source-account221/232 configuration fingerprints match before/after. Notifications remain default off; no delivery rows or human email were generated. The non-secret metadata request was deleted, Vault is locked, and this task's biometric grant was revoked.

Post-canary read-only checks through23:28:21 CST observed36 GPT usage records and zero GPT error-log rows during the DS window. Blue remained healthy at source8fec with zero restarts/OOM and API health200. No production GPT account/group was modified and no second deployment was performed.

Local evidence: `.omo/evidence/paid-fallback/management-ds-live-20261022.jsonl`, SHA256 `068d582e19ce1af31013c6fb4d32769c3a5e33105ab0c06667d13a81aa3ee56a`, and `management-ds-postcheck-20261022.txt`. This live acceptance covers DS HTTP and streaming recovery; actual email delivery stayed out of scope and stateful GPT transports rely on the already completed local tests/review rather than induced production faults.

## Rollback

Use the canonical verified-image/server release helpers under the shared maintenance lock. Retained prior runtime source is `7c34b50abb400cb66fc0bb554dc15b7ba1a07879`; its image is `sub2api:auto-20261022-033505-7c34b50a`. Do not manually change Caddy or stop active containers. Keep policy/notifications off, paid markers and audit history intact. An older binary that cannot isolate reserve markers requires disabling and unbinding reserve accounts first. Do not drop the new audit tables or undo financial history.
