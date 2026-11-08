# Cloudflare API alias ingress — 2026-11-08

The owner authorized repairing `api-proxy.turtleligpt.com` and repeating the
direct-versus-proxied carrier comparison after it works. The public proxied A
record already targets the existing AWS origin. No DNS change is required.

## Diagnosis and minimal change

The origin Caddy configuration served api, aws-test and www, but did not know
the new API alias. Direct-origin TLS with the API hostname succeeded, while
the alias failed with `TLSV1_ALERT_INTERNAL_ERROR`, including from origin
loopback. Cloudflare's edge TLS succeeded and its HTTP response was 525.
The existing managed API certificate covered only `api.turtleligpt.com`.

Add the alias to the existing API site address list. Both names then use the
same authentication-facing API routes, exact 128 MiB body contract, selected
upstream, streaming transport and header policy. Caddy manages each hostname's
certificate in the existing persistent data volume. Preserve www and the
existing direct API. Do not relax certificate verification to hide a handshake
failure. This change does not select a new default endpoint.

Source baseline: `544a7415fd605403b1e227fd75a3edb59fcd822d`. Relative to the
observed active image `5f2fceba80a32dae72bec2ccdebe19d74577e0b3`, that baseline
adds an image-studio acceptance document only. Keep the unrelated endpoint/CCS
feature worktree outside this release.

## Validation and release contract

Use `deploy/README.md` and `deploy/API_CERT_AUTORENEW_20261108.md`. Run the
Caddy receiver regression suite and the image-route contract tests; validate
and inspect the real adapted candidate with the installed Caddy version.
The canonical receiver requires its template commit to equal the active image
revision, so dispatch the standard exact-main image/configuration workflow.
Do not relabel the modified template as the previous image revision.

Both application and Caddy changes remain inside the documented maintenance
lock, generation admission, standby fencing, drain and rollback boundaries.
Caddy configuration reload can close existing WebSockets. Routine subsequent
managed-certificate renewal does not require another configuration reload.
The receiver's bounded health readiness permits asynchronous first issuance;
verify actual alias issuance separately because the canonical health target
is the original direct API.

Acceptance requires a real valid alias origin certificate, public Cloudflare
health 200 with the correct JSON, unauthenticated API rejection, authenticated
HTTP/SSE and Codex-compatible WebSocket probes, plus the original API/www
health. Keep probe credentials in the existing protected machine injection;
third-party carrier probes receive public `/health` URLs only.

After acceptance, compare repeated direct and actual api-proxy measurements
from matching regions/carriers. Separate successful HTTP transport timing
from model first-event latency and disconnections. A previous www control or
525 response is not a passing measurement of the repaired alias.

## Activation evidence

The owner-authorized canonical workflow
[`42462156586`](https://github.com/Turtle-Li/sub2api/actions/runs/42462156586)
completed successfully at 2026-11-08 08:26:51 UTC for exact source
`c47d7a34ec12affaa794e0243e2d911aca116d21`. Acceptance observed healthy active
`sub2api-blue`, zero restarts and no OOM; the previous green was stopped by the
canonical drain flow. The live Caddyfile SHA-256 was
`0af471b56749d02d470dd331669fbe0ffdb61b174cbab7b52610902528055cf2`.

The managed alias certificate is Let's Encrypt YE1, exact SAN
`api-proxy.turtleligpt.com`, valid until 2027-02-06 07:28:03 UTC, SHA-256
`74f5530af69ebf420904c11fecb9f2c40d4c8806b0c8b6a7d51671c5c883ce43`.
Caddy logged actual successful issuance at 08:26:36 UTC. The first ACME
challenge failed, then HTTP key-authentication challenge requests succeeded
and authorization finalized. Preserve the public HTTP challenge path and
persistent Caddy data for renewal. Both API hostnames have managed certificates;
do not restore the expired imported API TLS directive on rollback.

Both hostnames passed verified loopback and public TLS health with exact
`{"status":"ok"}` JSON, rejected unauthenticated models requests with 401,
and completed one authenticated SSE request (200) and one Codex-compatible
WebSocket request (101) with `response.completed`. The existing protected
release canary model was `gpt-5.6-sol`; credentials remained on the origin.
The startup-adapted and active Admin JSON each passed the alias's twelve-path
image-route contract. Public www remained 200. The temporary probe was removed.
These are short functional checks, not a long-session Codex stability claim.

After repair, four public ITDOG GET `/health` measurements completed at
08:30:03–08:36:45 UTC, in direct/proxy/proxy/direct order, with 1,293 total rows.
Each endpoint had 554 mainland carrier measurements across 31 provinces;
Tibet Mobile and Qinghai Mobile had no available nodes. Keep failures and one
unreturned proxy row in the availability denominators. Direct succeeded
538/554 (97.1%), proxy 461/554 (83.2%). Matching same-round same-node 200 rows
produced 459 pairs from 250 unique nodes: mean direct 961ms versus proxy
2817ms, mean paired increase 1855ms; p95 2508ms versus 7615ms. Repeated probes
are measurements, not additional independent nodes.

Carrier paired means were Telecom 1001/2074ms, Unicom 650/5404ms and Mobile
1090/2135ms (direct/proxy). There were 79 direct-success/proxy-not-success
measurements, including 76 Unicom; reverse disagreement occurred twice.
Some proxy failures were around the public tester's ten-second window. These
results do not identify the affected customer's original timeout or prove an
ISP block. Keep direct as the default and the repaired alias optional; no
automatic replay of uncertain model submissions or default endpoint change
was introduced. The separate endpoint/CCS feature worktree remains unreleased.
