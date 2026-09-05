# Taiwan Premium ingress pre-cutover readiness

Task: `SUB2-TW-CUTOVER-20260902`

Checkpoint: 2026-09-05 Asia/Shanghai

Status: **TRANSPORT CANARY PASS; AUTHENTICATED COMPATIBILITY CANARY PASS; NORMAL-FINAL FIXED-EGRESS CANARY PENDING; DNS CUTOVER BLOCKED**

This is the current preparation record for the retained GCP Taiwan Premium
transport candidate. It does not authorize a DNS change, a public traffic
switch, an application restart, an account/proxy mutation, or retirement of
the old origin.

## Current safety state

- Production DNS remains `api.turtleligpt.com -> 206.119.172.211`.
- The old origin remains the production and sole background owner. It is the
  rapid rollback target until the migration window and the provider expiry
  are both closed.
- The GCP Premium transport address is `130.211.243.139`; Azure remains the
  application origin at `4.216.216.16`.
- The first controlled candidate publication is complete. Four reviewed,
  credential-free scripts were installed on Azure under the root-only
  rollback bundle `codex-script-bundle-20260905-02`; the source image revision
  is `8cdef55eab042318ae33c3a91f308fa59455b865`. The install verified exact
  SHA-256 values, `root:root` ownership, and mode `0750`; it did not restart
  the application, reload Caddy, change DNS, or change an account/proxy
  binding.
- A second candidate-only blue-green publication completed at
  `2026-09-05 02:25 CST` with the same immutable image and
  `SUB2API_FIXED_EGRESS_COMPATIBILITY_MODE=true`. The candidate remains
  `traffic=accepting ... background=standby`; public DNS and the old origin
  were not changed.
- The bounded unauthenticated probes can still appear in access logs and
  invalid-auth counters. The candidate remains a standby origin and the old
  origin remains the only public production path.

## Local implementation gate

The current worktree contains the transport hardening and the shared image
route contract verifier. The following checks passed on this checkpoint:

```text
python unittest (image route + gateway/Caddy drift): PASS (13 tests)
test-caddyfile-cache.sh: PASS
gcp-taiwan-line/tests/transport-config-test.sh: PASS
runtime-guard-test.sh: PASS
install-autodeploy-runtime-mode-test.sh: PASS
server-release-container-guard-test.sh: PASS
blue-green-external-runtime-mock-test.sh: PASS
Python py_compile: PASS
bash -n: PASS
Go gateway/config targeted tests: PASS
git diff --check: PASS
```

ShellCheck has no errors in the changed transport scripts. The remaining
messages are informational existing test-fixture warnings (`SC2016` for
literal source matching and the pre-existing subshell `SC2030/SC2031` pair).

The route verifier is strict during ordinary release, stage, commit, and
runtime checks. Azure listener rollback deliberately allows the companion to
be absent so recovery is not blocked by an artifact that has not yet been
installed; Caddy syntax, container binding, transaction hashes, and reload
verification remain strict. A server rollback similarly uses warning-only
route checks while preserving the upstream/Caddy safety gates.

## Fingerprint and evidence rule

The historical 2026-09-02 adapted/live Caddy fingerprint
`8a9e08798e183dc4566fb7a72bd357ca4ad54a13d84d7fb5d20fd3336d75dd4a` is
historical evidence only. The shared image-route contract is now part of the
JSON verification fingerprint, so the old value must not be copied into a
new release record. After the companion verifier is installed on Azure:

1. Run `verify-azure-caddy-json.py` against the adapted startup JSON and the
   live Admin API JSON in the same maintenance window.
2. Require the two values to match and record the new SHA-256 in the current
   evidence record.
3. Re-run `verify-transport.sh azure`, `verify-transport.sh gcp`, and the
   exact-IP canary before declaring T3/T5 fresh. The Azure companion is now
   installed; the controlled publication evidence below is the fresh
   fingerprint checkpoint.

This same-run rule prevents a stale historical fingerprint from masking route
drift.

## Read-only checks allowed while users are active

Use direct pinned requests only; do not use the production DNS answer as the
candidate target. The normal low-volume checks are unauthenticated and do not
create paid tasks. A separately approved, single authenticated request may be
run only as the controlled canary recorded below; do not turn it into a loop.
The low-volume checks are:

```bash
curl --noproxy '*' --resolve api.turtleligpt.com:443:130.211.243.139 \
  https://api.turtleligpt.com/health
curl --noproxy '*' --resolve api.turtleligpt.com:443:130.211.243.139 \
  https://api.turtleligpt.com/v1/models
curl --noproxy '*' --resolve api.turtleligpt.com:443:4.216.216.16 \
  https://api.turtleligpt.com/health
```

The image route verifier sends empty unauthenticated requests and expects
`401`. A repeated invalid-auth probe may receive `429` from the per-IP abuse
limiter; that is recorded as rate-limited reachability evidence, not silently
converted to a success. Do not loop probes or print authorization headers.

These checks prove edge reachability only. They do not close the authenticated
generation, Responses WebSocket/continuation, or image functional gates.

The bounded read-only sample at 2026-09-05 00:15 CST returned:

```text
old /health: 200
Azure /health: 200
GCP /health: 200; GCP /v1/models: 401
GCP HTTP :80 /health: 308
Azure POST /images/generations: 401
Azure POST /v1/images/generations: 401
Azure GET /v1/images/batches: 401
GCP POST /images/generations: 401
GCP POST /v1/images/generations: 401
GCP GET /v1/images/batches: 401
```

The sample used no authorization header and no paid payload. It is evidence
that the candidate reaches the application without an edge 404; it is not a
functional image-generation result.

## Controlled candidate publication and canary (2026-09-05)

The following operations were completed without changing the public A record:

```text
Azure verify-transport: PASS (Caddy contract, image routes, TLS, forged PROXY rejection)
GCP exact-address canary from Azure: PASS (TLS fingerprint equality, HTTP redirect, h3=false)
Fixed-egress relay handshake from Azure: PASS (100.70.128.60:1080 and 100.81.60.44:1080)
Fixed-egress relay -> api.openai.com unauthenticated HTTP: PASS (both returned 401)
Caddy adapted/live contract fingerprint: 6dcad91052e1c5cbf0649610c99a9cc0c199e52c10df084a50cbe6dcdc2004ad (equal)
Installed companion verifier SHA-256: 92c18bd608ca4330de61d7713e7af7d93b463754fddd715e146c6aa2a6479b05
```

One deliberately tiny authenticated Responses request used the same token and
body against all three addresses, without printing the credential or response
body:

```text
Authenticated /v1/models: old/Azure/GCP all HTTP 200, 13 model IDs each
old 206.119.172.211: HTTP 200, response id and CANARY_OK present
Azure 4.216.216.16: HTTP 502, upstream_error
GCP 130.211.243.139: HTTP 502, upstream_error
```

Candidate application logs show that this request selected the existing
`default-美国` proxy row (external `isp.decodo.com:10002`) and excluded the
account before opening an authenticated proxy session. The Ops record has
`upstream_status_code=NULL`; the client-visible `502` was the gateway mapping
of the local fixed-egress policy error. Separate no-credential TCP probes from
both hosts reached the proxy listener and received the expected `407`, so the
evidence is not a GCP TCP hop or Caddy route failure. No production account
binding was changed to mask this failure.

A second owner-supplied temporary API key produced the same three-way result
under the strict default and selected the same account/proxy pair. The exact
database error was local policy rejection before any proxy connection:
`ACCOUNT_PROXY_UNAVAILABLE` caused by `FIXED_EGRESS_PROXY_INVALID`; the bound
`default-美国` row is a legacy authenticated `http` proxy with a configured
backup, while the strict OpenAI OAuth validator requires an active,
non-expiring, no-backup, credential-free Tailnet `socks5h:1080` row. This is
why the old image still worked and the new strict image returned `502`.

For a controlled candidate-only test, the same image was recreated with the
audited compatibility override. The temporary key then returned:

```text
Azure 4.216.216.16 /v1/models: HTTP 200 (13 models)
GCP 130.211.243.139 /v1/models: HTTP 200 (13 models)
Azure 4.216.216.16 /v1/responses (gpt-5.4-mini, max_output_tokens=4): HTTP 200
GCP 130.211.243.139 /v1/responses (gpt-5.4-mini, max_output_tokens=4): HTTP 200
Azure/GCP /health: HTTP 200; old public /health: HTTP 200
```

Candidate application logs recorded the authenticated request as `status_code`
`200` for account `11`; no `ops_error_logs` row was created for the canary.
This proves the purchased ISP proxy is usable from the candidate when the
legacy route is explicitly allowed. Compatibility mode is a temporary
candidate test setting, not the normal-final fixed-egress contract. The final
gate still requires an isolated account and one of the verified Tailnet
fixed-egress `socks5h:1080` rows, created and bound through the authenticated
compare-and-set admin flow, followed by stream/continuation and image checks.
Do not admit user traffic or change DNS until those gates close.

## Low-traffic execution order

Run this sequence only after the owner confirms a low-traffic window and the
active application/queue state is visible on both hosts:

1. Acquire the shared maintenance lock and record connection counts, current
   A-record/TTL, active image digest, Caddy hashes, and old-origin health.
2. Install the exact reviewed artifacts on Azure and GCP as root-owned files;
   install `verify_image_route_contract.py` beside the flattened release
   scripts with owner `root:root`, mode `0750`, and a recorded SHA-256. The
   Azure bundle is complete. The GCP TCP-only HAProxy does not consume this
   companion; its separate bootstrap/update fingerprint remains a gate because
   the instance intentionally has no project SSH identity.
3. Resolve or roll back any retained Caddy transaction, then run the Azure
   adapted/live JSON comparison and `verify-transport.sh azure`. The current
   candidate transaction is absent and the Azure verifier passed.
4. Run `verify-transport.sh gcp` and the exact-address public canary. Verify
   TLS identity, HTTP redirect, h1/h2, source-IP handling, and expected
   unauthenticated statuses.
5. With a task-scoped credential grant, run one authenticated basic request,
   one Responses stream/continuation check, and one image request. Keep
   credentials and response bodies out of logs; stop on any functional,
   timeout, 5xx, TLS, or source-IP regression.
6. Finish the fixed-egress account/proxy CAS and background-owner transfer
   gates. Azure must be the sole background owner; the old host must be
   fenced to `traffic=accepting ... background=standby` before DNS changes.
7. Obtain QA/reviewer/Claude disposition on this exact artifact/evidence
   snapshot, then change only the `api.turtleligpt.com` A record to
   `130.211.243.139`.
8. Monitor through at least two observed TTLs while keeping both origins
   available. On a stop condition, restore the A record to `206.119.172.211`
   first; stop or detach GCP only as a secondary containment action.

## Open gates

- Authenticated OAuth/fixed-egress bindings and their CAS evidence.
- Azure application state-file bind/inode convergence and sole background
  ownership transfer.
- Authenticated basic generation, Responses stream/continuation, and image
  functional evidence through the exact GCP address.
- GCP-side installation/update evidence for the transport bundle (the GCP
  bootstrap has no project SSH identity and was not changed in this canary).
- Independent QA and final repository review on the same hashes.
- Claude advisory review of the final post-fix snapshot. The first fresh
  request was submitted; if the review service is temporarily unavailable,
  keep DNS blocked and retry rather than treating the missing review as a
  pass.
- Cloudflare control-plane record/proxy-state confirmation and action-time
  owner approval.

Until these gates close, the correct operating state is candidate standby,
old-origin production, and no public traffic switch.
