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

Use `sub2api-aws-candidate` and the installed, root-owned
`sub2api-caddy-config-release.sh` with the exact commit and Caddyfile SHA-256.
The canonical receiver holds the shared production maintenance lock, checks
host/startup/live configuration agreement, projects the active slot, validates
the candidate and preserves its rollback transaction. Do not restart Caddy
or application containers to activate this change.

Caddy's default reverse-proxy reload behavior can close WebSockets. Before
activation, wait for an idle window and use the existing node-admission fence
to confirm zero in-flight requests; reopen admission immediately after the
canonical configuration transaction. Do not force-close existing requests to
create that window. Ordinary managed-certificate renewals subsequently happen
inside Caddy and do not require repeating this configuration change.

Acceptance requires a real newly served certificate with the correct chain,
hostname and more than seven days of validity, plus API/www health and the
existing application generation remaining healthy. An unchanged old
certificate with HTTP 200 is not renewal proof. Preserve the persistent
Caddy data volume and inspect its certificate maintenance logs after activation.

At authoring, the configuration change is prepared but activation is not yet
claimed. Append exact deployment and public-certificate evidence after success.
