# Inline group availability release — 2026-11-07

Owner-authorized correction: show availability inside homepage and model-plaza
group cards for anonymous and ordinary signed-in viewers, using the existing
plaza visibility boundary. Enable a small trial of inexpensive representative
models every ten minutes with minimal output and existing ops notifications.

## Build and local acceptance

- Implementation `0de6a0fbdf03c8db59935c2aa9f03d784de25dae`; final deadline/mail
  correction `63d65d123eff3703dabdb4f5e292140c52fe8a62`.
- Final Go service/handler/routes regressions pass with monitor/plaza,
  notification, paid-fallback SMTP and stream-context coverage. Real PostgreSQL
  claims, separate facts, config JSON roundtrip and migration tests pass.
- Frontend build/typecheck/locales pass; 52 targeted tests and 12 static catalog
  tests pass. Actual local Chrome layout/interaction QA passes for homepage
  widths 1440/768/390/320 and ordinary-user 1440/390 light/dark. The fixture
  screenshots do not establish live provider health.
- Main-session self-review, not independent review or human acceptance.
  Local evidence: `.omo/group-status-self-review.md` and adjacent test logs.

## Release boundary

Application host: approved alias `sub2api-aws-candidate`, root-owned
`/opt/sub2api`, config `/etc/sub2api-autodeploy.env`, repository
`https://github.com/Turtle-Li/sub2api.git`, branch `main`, background `activate`.
Data host: approved alias `sub2api-db`, existing PostgreSQL 18 container
`sub2api-migration-postgres`, under `/opt/sub2api-migration`. No credentials or
new identities are introduced. Fresh read-only preflight found healthy green
revision `c878bd57359a58fb6f030edf23bb5446bf29aad6`, zero restarts. Latest archive
`sub2api-db-backup-20261107-031541.tar.gz`, 339411170 bytes, exists on the data
host; this observation is not a fresh restore-rehearsal claim.

GitHub returned HTTP422 for the first dispatch while Actions permissions and
workflow state remained enabled. The signed-in GitHub page identified the fork
as automatically paused. Re-enabling its existing workflows restored dispatch;
CLA Assistant remains manually disabled. No billing/security setting changed.

Run `42355191170` was cancelled during image build before any production switch
so the SMTP/deadline corrections could be included. The canonical replacement
workflow is [42356693465](https://github.com/Turtle-Li/sub2api/actions/runs/42356693465),
built from final source above. It completed successfully; no source was built
on production.

The static homepage is a separate publication. Digest-bound
`deploy/publish-model-group-status-static.sh` holds the canonical maintenance
lock, publishes immutable assets before the atomic HTML switch, and verifies
the origin and matching availability API. Expected old HTML digest:
`e3aaca466cf67d730a5db32ef55cde21a721115595705573f9f66b3d081a6a7c`.
New HTML `704c1955ea56cb6253e09e0962e6899706410b4bbd54e74c9f4c7983c39aeaf8`;
module `f9cba8bc1d599795a6152933f763715664bb63c8d42f2e9df68f960b98c7ee43`;
CSS `65014e0f555cc57a943f2fe64c1899bb05b425e9b715801fc0f42e37a5288c46`.
Rollback HTML: `/data/sub2-web/home/index.before-model-groups-20261107.html`.

## Runtime acceptance

The canonical workflow and separate locked static publication both succeeded.
At the final read-only checkpoint, `sub2api-blue` was running and healthy at
`63d65d123eff3703dabdb4f5e292140c52fe8a62`, with zero restarts. The prior green
container was stopped. Static publication evidence is retained at
`/var/log/sub2api-release/model-groups-20261107.UtKRl5`.

Configuration version3 keeps anonymous publication enabled and enables exactly
three representative checks, one per group and ten-minute slot:

| Group | Representative model | 09:00 / 09:10 UTC slots | Combined tokens |
| --- | --- | --- | --- |
| 6, GPT metered | `gpt-6-luna` | success / success | 22 input, 10 output |
| 10, official Claude | `claude-haiku-5-5` | success / success | 1046 input, 2 output |
| 29, Kiro | `claude-haiku-4-5` | failure / failure | 0 reported input/output |

First slot executions finished at 09:02:25–27 UTC; the second at 09:10:23–25.
Read-only PostgreSQL inspection found zero duplicate group/slot records. The
Kiro account returned upstream HTTP503 with a no-available-resource indication
on both attempts. No max-token/model-parameter rejection was observed in those
bounded errors. The failure remains visible; no manual replay or history
rewrite was performed. Zero reported tokens on failed attempts does not prove
the upstream consumed no quota.

The existing ops alert switch and recipient were retained. One exact
group29/failure/hour delivery receipt at `2026-11-07T09:02:29.06510458Z` proves
SMTP acceptance, without exposing its recipient. The second failure shares
that hourly identity and did not create another delivery. Existing recovery
notifications remain disabled by their prior preference; local recovery-mail
tests passed, but no production recovery delivery is claimed.

Live Chrome verification confirmed inline homepage status in all six initially
displayed group cards, with ten public groups available in the existing
catalog. The signed-in model plaza rendered all ten scoped status rows. Both
successful representative groups showed 100% detection success, while Kiro
showed 0%; real-request rates remained separately labelled. The homepage
模型状态 link targets `#models`. Ordinary-user scope and responsive light/dark
coverage were validated with local fixtures and backend tests, not a separate
production ordinary-user login.

Local real-surface screenshots: `.omo/group-status-live-home.jpg` and
`.omo/group-status-live-plaza.jpg`. The enabled targets remain running for the
owner-authorized trial. Admin controls are under `/admin/channels/monitor` →
分组主动检测, with a global switch and individual group/model switches.

Output limits request one Claude output token and sixteen OpenAI completion
tokens. The Codex OAuth adapter omits unsupported output caps; observed GPT
outputs were five tokens per check. Claude input includes the existing provider
adapter prompt overhead. These checks do not debit customer usage records, but
they can consume upstream quota. No daily quota estimate is inferred from the
first two slots.

## Rollback

Disable the global/per-group scheduled-check switch before using an older
binary. Retain additive migration268 and all check/history rows. The prior
healthy application revision `c878bd57359a58fb6f030edf23bb5446bf29aad6` remains
the compatible canonical rollback candidate; use the release helper and its
maintenance lock, never raw container lifecycle commands. Static rollback
restores only the saved HTML under the same lock. Do not restore a database,
alter billing data, or change Caddy configuration to undo this feature.
