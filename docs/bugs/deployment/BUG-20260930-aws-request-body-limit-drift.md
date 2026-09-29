# AWS request-body limit drifted from the application contract

## Symptom and impact

The AWS production application was configured for 128 MiB HTTP request bodies,
but the live Caddy edge stopped requests at decimal 100,000,000 bytes. Client
Responses WebSocket ingress used the same decimal value. A startup log also
reported an unlabeled 16 MiB WebSocket read limit, making it easy to mistake an
upstream response-message guard for the client upload ceiling.

Production was not currently capped at 16 MB, but the effective HTTP limit was
still lower than the application contract. More importantly, editing the
tracked AWS Caddyfile did not change the live host, so a future limit fix could
pass source review and still never reach production.

## Root cause

The request-body policy was split across independently projected layers:

- `/opt/sub2api/Caddyfile`, bind-mounted into the production Caddy container;
- `SERVER_MAX_REQUEST_BODY_SIZE` and `GATEWAY_MAX_BODY_SIZE` in the application
  runtime environment;
- `gateway.openai_ws.client_read_limit_bytes` for client WebSocket ingress;
- a separate 16 MiB upstream WebSocket response-message safety limit.

The blue-green release changed only the Caddy upstream slot inside the existing
live file. The GitHub deployment transferred only the Docker image, and no
release step transferred `deploy/aws-candidate/Caddyfile`. Consequently the
tracked template and the live body limit could drift indefinitely. The generic
Caddy templates intentionally use a different scoped 128 MB/16 MB policy, so
searching for one global numeric constant could not prove the AWS deployment.
The runtime env generator also copied an explicitly configured legacy client
WebSocket limit from the old container unless that key was made a managed
override.

## Repair contract

AWS production uses one exact 128 MiB (`134217728` byte) HTTP edge limit for
all API routes. Client Responses WebSocket ingress uses the same value. The
application runtime keeps both global HTTP limits at 128 MiB. Known pure-text
endpoint limits and feature-specific media budgets remain intentional narrower
business contracts. The 64 MiB post-decompression bomb guard is likewise a
decoded-payload security boundary, not a stale edge upload limit.

Forward blue-green releases treat the Server, Gateway, and client WebSocket
keys as one managed trio: remove every inherited occurrence, append one exact
`134217728` value for each, and reject a prepared target if any value is absent,
duplicated, or stale. The Caddy receiver also requires all three values on the
exact active OCI revision before it can mutate the edge configuration.

The 16 MiB upstream WebSocket response-message limit is retained, but bootstrap
logging must identify it as `upstream_ws_read_limit_bytes` and separately emit
`client_ws_read_limit_bytes=134217728`.

Every non-build-only AWS production workflow must upload the reviewed Caddy
template and SHA-256 through the restricted deploy account after the image
release. The root receiver must:

1. acquire the canonical host maintenance lock and reject unfinished Caddy or
   blue-green transactions;
2. require host, startup, and active Caddy views to agree on one serving slot,
   and require that slot's exact image revision and all three application
   ingress settings;
3. project the template's blue placeholder to that slot;
4. validate Caddy plus the exact 128 MiB body-limit contract before mutation;
5. update the bind-mounted configuration without replacing its inode;
6. reload and verify all three views and public health;
7. restore and force-reload the prior bytes if any activation check fails.

The previous configuration is retained as a root-only rollback backup, and the
release record binds the source commit, uploaded digest, and activated SHA.

## Verification

Source checks cover the exact four AWS `request_body` directives, four explicit
Content-Length guards, and four operator messages, and reject stale 16 MB,
100 MB, or `100000000` values. Receiver tests cover digest rejection, lock
contention, active-green projection, candidate-policy rejection, successful
three-view convergence with inode preservation, and rollback after reload or
verification failure. Compose tests require the Server, Gateway, and WebSocket
ingress environment keys with exact 128 MiB defaults in every supported
topology. Blue-green tests begin with inherited 256 MiB HTTP and decimal
100 MB WebSocket values, prove all three are normalized, and reject reuse after
one prepared value is changed back. A configuration test also proves these
environment values override stale values in the persistent `config.yaml`.

The production acceptance probe must then demonstrate that a valid authenticated
JSON request larger than 16 MiB reaches the application and receives an
application response rather than edge HTTP 413, while a request larger than
128 MiB is rejected by Caddy. Live host, startup, and Admin API configuration
hashes must converge, and the application bootstrap log must distinguish the
128 MiB client ingress value from the 16 MiB upstream response value.

## Rollback

The Caddy receiver restores the pre-change bytes in place and force-reloads the
prior validated configuration on failure. For an operator rollback, use the
receiver's retained root-only backup under the same maintenance lock, validate
it in the running Caddy version, restore it without replacing the bind-mount
inode, reload with `--force`, and repeat the three-view and health checks. The
application image remains governed by the normal blue-green rollback process.
An emergency automatic rollback may restore an existing generation created
before this ingress contract; only the new target-env identity check is
disabled on that rollback call so rollback availability is not lost. Every
ordinary forward release still rewrites and verifies the exact trio before
cutover, and Caddy activation remains impossible until the active revision has
all three exact values.
Any failed configuration transaction remains as recovery evidence and blocks
the next automated Caddy release until an operator verifies the restored views
and deliberately clears that transaction.
