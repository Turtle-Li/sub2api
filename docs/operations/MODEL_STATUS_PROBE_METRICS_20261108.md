# Scheduled checks fill quiet-group metrics — 2026-11-08

The owner requests ten-minute checks to populate card metrics without real
traffic. Real group requests remain the preferred source independently for
success and first-token latency. Missing metrics use explicitly labelled check
success, native first-token P50 or historical whole-check duration P50. Public
cards, authenticated cards and the embedded homepage share this contract.

Six completed rounds qualify success; six successful valid timings qualify each
P50 independently. All use the current model and three-hour window, with a
completed result within15minutes. Stale, disabled and insufficient probes remain
unknown. At most three attempts produce one final result; retries do not create
extra samples, while every attempt's tokens remain in the private consumption
report. Cache continues to use real dashboard usage and the already documented
best-complete-hour statistic. No extra warm-up requests or owner config changes.

Migration272 adds nullablefirst_token_ms. Native gateway receipts are persisted
only on validated success. Historical/old-writer rows stayNULL; total duration
is never mislabelled first token. The versioned public fallback DTO exposes only
aggregate timings/rates. Homepage immutablev7 preservesv6 for rollback.
Pinned upstream contract, intentional differences and source/test references:
`docs/features/MODEL_AVAILABILITY_GROUPS.md`.

Validation: scoped service/handler/routes unit checks and real PostgreSQL
repository tests pass, including legacyNULL and final-attempt timing.68frontend
feature tests and9static metric tests pass. Frontend production build, Go build,
publisher syntax and diff checks pass. Real Chrome1440/390px dark fixtures
confirm true-request priority, labelled legacy duration/nativeTTFT fallback,
disabled unknowns, search reflow and no horizontal overflow on both surfaces.

Rollback: use the canonical blue-green helper with the previous compatible
image under the shared maintenance lock. Leave additive migration272 in place;
old writers ignore it. Restore the exact pre-v7 homepage backup under the same
lock and retain immutablev6 assets. No paid claim, usage row or owner setting
needs deletion or reversal. Exact production release/live evidence follows
below.

## Production release and live verification

Implementation source `2a7267d2ed422c5320e495d9a1df798ad48221c0`, safely pushed
directly to the owner's `main` after fetching and confirming no concurrent
advance. The staged-secret guard passed. Frozen23file independent review:
Standards/SpecAPPROVE, codeQualityStatusCLEAR, no findings or blockers.

Canonical workflow [42467189109](https://github.com/Turtle-Li/sub2api/actions/runs/42467189109)
completed SUCCESS. Version0.2.21, image
`sub2api:auto-20261108-013640-2a7267d2`; exact-source transport/image validation,
blue-green release and Caddy activation passed. Host release record:
`/var/log/sub2api-release/gha-20261108-013640-2a7267d2-2736529`.
Release audit: app5xx=0, appfatal=0, Caddy5xx=0.

Final read-only inspection at09:40UTC: Caddy resolves solely toblue; source
matches2a7267d2, healthy,0restarts/OOM, sole active background owner with matching
read-only generation fence. Prior green3a9ecff5 was firststandby and then stopped
by the normal drain monitor; no forced connection interruption or retained-drain
override. Production config remainsversion5, probeMD5
`ec263ba0fb0fddbde71cc8786192c207`, selected6/10/17/18/35/36 unchanged.

Both loopback-pinned API/www status endpoints pass version1, privacy,18cells/
10minute spacing, cache metadata and metric-source checks. All six groups are
operational with qualified100%check success at verification. RealGPT metrics
remain request-sourced; idleClaude/DeepSeek/Kimi/GLM/MiniMax now expose separate
check rates and whole-check P50, with no synthetic request values. No prices or
selected model/account/token/sample-count details were added to public JSON.
The nullablebigint migration is confirmed live. Each enabled group naturally
wrote one valid native first-token receipt in the09:40UTC scheduled round;
native P50 intentionally waits for six successful timings while historical
whole-check duration fills the card immediately.

Homepage immutablev7 published after app validation, under the shared maintenance
lock with old-hash CAS, asset checks and atomic HTML replacement. Digests:

- HTML `a500c437fa3f7a3c22c89ad957216e2f17ee9624f5c0c1c131578b8193658e45`.
- JS `9458a7371c907d1215e33253cbc31afb556e12579702c204642d02a8880bcc9f`.
- CSS `b9b4975e1d6bbd2fd769fb5fa98aa3b68b22734a6bcb29299118566d754dc095`.
- Evidence `/var/log/sub2api-release/model-groups-20261107.HrV28b`.
- Rollback `/data/sub2-web/home/index.before-status-probe-metrics-20261108.html`;
  prior HTML577f73ebdf628c200a57a7d9c03fbe498b63dd17128eb5d404a25fe69bc58e66
  and immutablev6 assets retained.

Real Chrome live status and homepage both verified at1440/390px. Source labels
match API: realGPT100%/3.0s, idleClaude100%/2.0s check duration. Cache remains
independent real usage (GPT97.9%,Claude97.7%,DeepSeek97.3% at capture); legitimate
absent cache remains absent. No horizontal overflow or console errors. Desktop
and mobile screenshots are task-local `status-probe-metrics-live-*.png` under
`.omo/`, distinct from controlled local fixture captures.

No owner models, switches, alert preferences, retry limit or ten-minute interval
changed; no additional paid diagnostics were sent. Original trial
2026-11-07T20:03:22Z–2026-11-08T20:03:22Z and automation24 remain unchanged.
Native timing adds no token use. The24hour consumption report is still pending
its existing end time; no completed-day claim is made here.
