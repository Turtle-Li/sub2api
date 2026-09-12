# Production CNY wallet cutover — 2026-09-12

The owner-approved USD→CNY transaction committed at **13:40:29.509217 CST**
while the API remained accepting. All 29 wallets were converted at 6.75;
their boundary sum changed from 259.30467517 USD to 1750.30655742 CNY.
Each wallet was rounded separately to its existing eight-decimal precision.
Future paid recharge is 1 CNY paid → 1 CNY credit, with the existing nominal
bonuses 0/0/5/20/75/145. Group, user and model discount multipliers are preserved.
Model cards may select USD or CNY; existing cards remain USD. Subscription
entitlements and subscription consumption remain USD. Historical payment,
refund, invoice and usage records were not relabeled or multiplied.

## Release and independent review

- PR 14 merged as `ba290a4744ab8606951e012d8c1e40c58277992a`.
  Tested application head: `4f191beb553057c736a7dfd8cb582b906420b661`;
  the merge additionally preserves the concurrent recharge-style operation docs.
- CI `34673981745` passed all five jobs; Security `34673983412` passed both.
  Claude independently approved the currency implementation, scoped cache
  refresh and cache-bypass implementation. Its final bypass review reported
  no release blockers. A later optional fallback question hit the provider's
  503 and is not represented as an additional approval.
- Build-only workflow `34674551212`, artifact `10291901992`, linux/amd64.
  Docker archive: 83,817,668 bytes, SHA-256
  `7d19778b38281c7274caba46280f9737ab066af957d9ce8a4040350d355aa3f9`.
  Actual config digest:
  `7c405eb4b02fbb28893f31c5264bc57acd0a9215aa7e46ac527836c470b63d89`.
  Loaded image ID:
  `sha256:fa2ac3fc55266032c883ecc620d38138375d78a095a8a122f6a153afab814df9`.
- Both normal blue-green releases used the installed lock-owning receiver.
  Logs: `/var/log/sub2api-release/gha-20260912-132312-ba290a47-1330151`
  and `/var/log/sub2api-release/gha-20260912-133729-ba290a47-1342766`.
  Both release gates passed real models/Responses requests and reported
  app 5xx/fatal/Caddy 5xx = 0 in their release windows.
- Current serving origin is `sub2api-candidate`, blue, accepting/background
  active, image `sub2api:auto-20260912-133729-ba290a47`. Green contains the
  same compatible application and stopped naturally through the drain monitor.
  The ancient stopped `sub2api` container was archived by renaming it to
  `sub2api-pre-cny-legacy-20260912`, outside the guard's fallback names.
  Do not start an old USD-only binary against the CNY database.

## Backups and writer exclusion

Canonical backup `sub2api-db-backup-20260912-131032.tar.gz` is 261,716,395 bytes,
SHA-256 `457ee623926c0604694b8950e17e0d93e2fd4cef39ca33523b30096215475794`.
It passed archive/dump/Redis checksum checks, isolated PostgreSQL restore,
dry-run conversion, factor-1 apply, duplicate refusal and exact pre-reopen
wallet rollback. Its root-only retained copy and checksum are outside backup
rotation at `/opt/sub2api-migration/currency-20260912/backups/` on `sub2api-db`.
The earlier 12:16 backup and rehearsal are also retained. No offsite-copy
claim is made for the expired backup relay.

The committed manifest also has a separate compressed schema dump in that
directory, `currency-manifest-committed-20260912.sql.gz`, SHA-256
`497e98511061a2ce9cab21bb6b0a873e84123bfeb020b33ae048fff36a45c857`.
Selected monetary acceptance JSON is retained under the protected
`live/acceptance/` directory, separate from Git.

The obsolete `sub2api-new` public and Tailnet sources were removed from the
dedicated DB host's exact-source firewall, UFW and PostgreSQL HBA. Verified
Azure public/Tailnet connectivity and unrelated rules were preserved. HBA
reload succeeded with zero parse errors; no PG/Redis restart was used.
At the currency gate there were zero obsolete-source connections; all
application connections came from the verified Azure source. Network backups
are under `/var/backups/sub2api-db-peer-retire/retire-20260912T050325Z.tLIeN7`.
The first helper's unsupported UFW `--force` syntax was corrected, and the
remaining steps were completed against exact before/after rule assertions.
The reusable helper now has a regression test for the installed syntax.

Application configuration/Caddy and prior agent identities are retained under
`/var/log/sub2api-release/currency-cutover-20260912/` on the origin. The recovered
www site, static files, certificate, payment and Feishu agents were preserved.

## Transaction and cache operation

The reviewed `wallet-to-cny.sql` SHA-256 is
`88251e21f4ff52827577b54a5aa10cb695ee6e39774945b39ad591643cfcb4d8`.
It ran exactly once with `recharge_factor=1`, `apply=true`, while holding the
origin's canonical maintenance lock. No pending payment/refund, unrefunded
balance order, frozen wallet or nonterminal batch passed the transaction gate.
Payment was in fact enabled; the run did not assume the absence of writers.
Public batch admission and the platform flusher remained disabled.

The protected `currency_cutover_20260912` schema records every converted field
at the exact transaction boundary and the prior selected settings. Besides
29 wallets, the manifest covers 29 standard API keys, 136 platform quota rows,
27 affiliate rows and the existing 5 USD promo promise (now 33.75 CNY).
Unlimited zero/NULL limits stay unlimited. SQL output, boundary snapshots and
timestamps are in `/opt/sub2api-migration/currency-20260912/live/` on the DB host.

The temporary bypass marker required root:root, 0600 and host `chattr +i`
because the image entrypoint recursively changes application-data ownership.
This was corrected and behaviorally verified before monetary SQL. Monetary
and auth cache reads used DB; subscription caches stayed untouched. Scoped
refresh ran immediately after commit, again after 60 seconds, and after the
old generation drained. The marker was removed with `chattr -i` then `rm`
under the maintenance lock. Normal caching is restored; no restart was needed.

## Live billing evidence

The pre-existing protected release credential was securely matched to key 46,
user 1, standard group 16 (multiplier 0.01), without printing its value.

| Probe | Currency | Wallet decrease | Usage actual cost | Result |
| --- | --- | --- | --- | --- |
| New blue with bypass, before SQL | USD | 0.00016225 | 0.0001622500 | Exact match |
| After SQL, bypass active | CNY | 0.00087649 | 0.0008764875 | Matches stored precision |
| Normal caches restored | CNY | 0.00091496 | 0.0009149625 | Matches stored precision |

The first CNY row has USD reference cost 0.012985, CNY total cost 0.08764875
(exactly ×6.75), and actual cost 0.0008764875 (exactly ×0.01). All 29 current
wallets reconciled against their converted manifest values less legitimate
post-boundary usage; group multipliers matched the pre-transaction snapshot.
DB balance 623.28400520 matched Redis 623.2840051974999 within 1e-8 after the
second debit. A fresh authenticated read recreated the auth cache and
`/v1/usage` returned `unit=CNY`; public settings returned CNY/rate 6.75.

While bypass was active, unlimited platform counters accumulated the same
actual cost because a cache miss deliberately selects the conservative write
path. With normal caches and all limits NULL, the next request correctly
left platform counters unchanged. The probe's zero key quota/rate limits
were not represented as a test of finite-limit enforcement.

The owner's existing subscription 9 had exhausted all windows and no reset
card, so it was preserved. A five-minute acceptance fixture (subscription 65)
was created only for the owner's user 1 in existing GPT group 13, with zero
initial usage, the normal group limits and an explicit acceptance note. The
existing key 46 was temporarily rebound with immediate auth/subscription
cache invalidation. Real `gpt-5.6-sol` models/Responses returned 200; usage
622287 recorded **0.018115 USD**, with identical increments in all three
subscription windows and **zero wallet debit**. Observed component prices
matched the pre-cutover USD probe: input 5, output 30 and cached input 0.5 per
million tokens. The optional account-stat override is correctly NULL for this
ordinary USD subscription row; it is not an independent cost oracle.
Finally key 46 was restored to group 16 and the fixture explicitly expired;
the entitlement and real usage remain for audit. No reset grant, payment,
wallet credit or ordinary subscriber entitlement was changed by this test.

A later all-wallet readback included 30 post-cutover wallet usage rows and
76 subscription rows: every wallet row was CNY, every subscription row USD,
and all 29 manifest wallets still reconciled within stored precision.

From 13:23:51 through 13:55:33 CST, 224 combined API/www health samples all
returned 200, including 106 samples over more than 15 minutes after the SQL.
These health checks are not a claim that every model request succeeded:
application logs also showed gpt-5.4 `/responses` failures in group 6, with
no balance-rejection or panic evidence. Separate later asynchronous audit
events reported `prompt_guard_unavailable`. Read-only investigation found the
Prompt Guard relay still targets the expired old peer; its timeouts predate
the currency change. However `blocking_enabled=false` and those audit failures
followed the HTTP 503 responses, so they do not establish the cause of the
customer request failures. Keep that dependency fault and the synchronous
request diagnosis separate from successful currency acceptance evidence.

## Forward recovery

Production has accepted new CNY financial facts. Do not restore the old full
database or execute `rollback-before-reopen.sql`. Preserve the manifest,
usage/dedup rows and protected backups, keep compatible code, and correct
forward if needed. Do not reauthorize the expired source or revive archived
USD-only containers. Future releases must preserve the CNY setting and rate,
the 1:1 recharge decision and a CNY-compatible automatic fallback.
