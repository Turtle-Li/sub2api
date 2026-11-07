# API automatic certificate management — 2026-11-08

The owner authorized restoring automatic certificate management after the
network investigation found an imported API certificate expiring at
2026-11-08 05:39:38 UTC (13:39:38 Asia/Shanghai).

## Diagnosis and change

The production `api.turtleligpt.com` site explicitly loaded files under
`/etc/sub2api-certs/api/current/`. Its certificate had only the API hostname
in its SAN. Caddy reported `unmanaged certificate expires within 1 day`
and skipped automatic management when matching loaded certificates existed.
No certificate-related systemd timer or default certificate-receiver
configuration was found on the current AWS host. This does not establish
the historical absence of all external renewal jobs.

Remove only the API site's explicit certificate-file directive. The named
HTTPS site then uses Caddy's ACME issuance and renewal with the existing
persistent Caddy data storage. Preserve its routes, body limits, upstream,
streaming transport, container lifecycle and the www site. The imported www
certificate is separate and was observed to expire on 2026-12-11.

The behavior follows [Caddy Automatic HTTPS](https://caddyserver.com/docs/automatic-https):
manually loaded certificates suppress automatic management unless explicitly
overridden. Public DNS and the ACME challenge path must still reach this host;
the Caddy data volume must remain persistent. The root source and activation
contract remain `deploy/README.md` and `deploy/AWS_CANDIDATE_20260923.md`.

## Activation and proof

Use the explicitly dispatched production workflow for the exact `main`
revision, targeting `sub2api-aws-candidate`. It builds the immutable image and
sends its matching Caddy template through the canonical receivers. The Caddy
receiver also checks that its commit equals the active application OCI
revision: a standalone new configuration commit cannot be loaded onto an old
application revision. Do not bypass this check or label new configuration as
old source. The receiver holds the shared production maintenance lock, checks
host/startup/live agreement, projects the active slot, validates the candidate
and preserves its rollback transaction. No raw container restart is needed.

Caddy's default reverse-proxy configuration reload can close WebSockets;
standard blue-green application draining alone does not prove every client
WebSocket was preserved. A passive idle check found four persistent upgraded
connections and made no admission/configuration change. The owner was told
that this configuration reload may require client reconnection. Use the
canonical release and retain its old-generation/drain safeguards. Ordinary
managed-certificate renewals subsequently happen inside Caddy without this
configuration reload.

Acceptance requires a real newly served certificate with the correct chain,
hostname and more than seven days of validity, plus API/www health and the
existing application generation remaining healthy. An unchanged old
certificate with HTTP 200 is not renewal proof. Preserve the persistent
Caddy data volume and inspect its certificate maintenance logs after activation.

The old imported API certificate is not a valid long-term rollback after its
November 8 expiry. An application fallback must preserve the working automatic
API certificate configuration and persistent Caddy data; do not restore the
old certificate directive from an obsolete template.

## Activation evidence

Configuration commit: `217158d3d97147a1204f5c8b81c3e0670fbd62ed`.
The initial standalone receiver attempt was rejected before live mutation
because the active image revision was `6d39b7edb`. The subsequent standard
[workflow 42417275856](https://github.com/Turtle-Li/sub2api/actions/runs/42417275856)
builds `d65140f7a43c76788003d319b484f1fa5723043e`, which adds a concurrent status
acceptance document to the certificate fix; application code equals the then
running `6d39b7edb`.

That workflow successfully activated healthy application slot `sub2api-blue`,
but its Caddy step rolled back: the receiver probed HTTPS while first ACME
issuance was still solving its challenge, then canceled issuance by restoring
the manual-certificate configuration. See
`docs/bugs/deployment/BUG-20261108-first-acme-health-race.md`.

The receiver repair keeps strict TLS and configuration checks, allows bounded
HTTPS readiness, and adds a protected acknowledgement for an already verified
rollback. Install only the reviewed receiver at root:root mode 0750 under the
shared maintenance lock, retaining the previous helper as evidence. The
candidate template itself is unchanged from the active `d65140f7a` image:
acknowledge and retry with that exact commit and digest, rather than relabeling
it with the later receiver-repair commit. No application rebuild or lifecycle
change is required for this retry.

## Completed activation

Receiver repair commit: `f9e6ff8560d7382c2068e8da54f7a264871a13f7`.
Its installed root:root 0750 SHA-256 is
`d72228977a0f8d9a78d26b3c170c653565954ce925a3ae8ee6b7a0b2adf2d2c9`.
The previous helper was preserved under the root-only backup directory.
The complete receiver regression suite and ShellCheck passed for this source.

At 2026-11-07 22:14:41 UTC the new receiver acknowledged and archived the
already restored transaction after strict provenance, backup, configuration
and HTTPS checks. At 22:15:20 Caddy obtained the certificate; at 22:15:21 the
receiver completed the unchanged `d65140f7a` template release. The first three
TLS probes were temporarily unavailable while issuance completed. Final host
Caddy SHA-256:
`4f1d4238206ede6ccc2b664ae488506810fef01c7fb3315866be7bd8218302d6`.

External verification at 22:16 UTC passed the system trust chain and hostname
for the new Let's Encrypt YE1 certificate, expiring **2027-02-05 21:16:46 UTC**
(2027-02-06 05:16:46 Asia/Shanghai). Its leaf SHA-256,
`f2cee77eee945df3b28c78981b155af61b2b21f6d5135641b5ea57d07ea25a0e`,
matches the managed certificate in the persistent Caddy data volume. Caddy
logged renewal information and retains the existing `/data` and `/config`
volumes. The imported API directive is absent.

API health, www health and www homepage returned 200; unauthenticated API
models returned 401. Application blue remained healthy with zero restarts and
no OOM; Caddy remained running with zero restarts. Neither Caddy-config nor
blue-green transaction remains pending. These checks establish certificate
activation, not repair of the original user's intermittent network failure.
The independent `api-proxy` alias still returns 525 and the direct default
remains. No endpoint/CCS feature code or production setting was included.
