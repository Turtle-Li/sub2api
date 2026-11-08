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

Pending exact release, certificate/transport proof and carrier comparison.
