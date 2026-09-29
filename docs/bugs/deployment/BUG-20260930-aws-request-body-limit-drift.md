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

The first production activation exposed one further release-gate defect before
the Caddy receiver created a transaction. Caddy automatically adds the path of
the source Caddyfile to each `file_server.hide` list. The same live bytes were
therefore represented as `/dev/stdin` in the host adaptation,
`/etc/caddy/Caddyfile` in the startup adaptation, and
`/tmp/sub2api-release-sub2api-<slot>.Caddyfile` in the Admin API state left by
the blue-green switch. A byte-for-byte canonical JSON hash treated those
security-equivalent generated paths as configuration drift and rejected the
release even though every other field matched.

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
For three-view hashing, only the source identity expected for that view is
replaced with an internal marker, and only inside a `file_server.hide` list.
The host view requires `/dev/stdin`, the startup view requires the configured
startup path, and a candidate requires its exact receiver-generated path. The
active view accepts the startup path, requires a normal blue-green temporary
path to match the selected slot, and separately recognizes the tightly named
rollback paths. Operator-supplied hide entries, arbitrary or cross-view paths,
and every other JSON field remain in the hash. This preserves the
full-configuration drift gate without confusing Caddy's automatic source-file
protection with a semantic change.

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
The receiver regression fixture models distinct host, startup, blue-green,
candidate, and rollback source paths. It proves those reviewed generated paths
converge while an unreviewed path, a near-miss suffix, a normal-release path for
the other slot, a candidate-only receiver path or host-only identity in the
active view, and unrelated active JSON still fail before a transaction is
created.

The production acceptance probe must then demonstrate that a valid authenticated
JSON request larger than 16 MiB reaches the application and receives an
application response rather than edge HTTP 413, while a request larger than
128 MiB is rejected by Caddy. Live host, startup, and Admin API configuration
hashes must converge, and the application bootstrap log must distinguish the
128 MiB client ingress value from the 16 MiB upstream response value.

## Production closure on 2026-09-30

The AWS host's upload path was measured before choosing the final ceiling. The
16, 32, 64, and 128 MiB loopback-to-public-TLS probes all completed with HTTP
200 and observed burst upload rates of 54,585,258, 103,258,067, 36,679,547,
and 113,003,948 bytes per second respectively. A separate sustained 1 GiB
transfer averaged 15.753 MiB/s. At that sustained rate, 128 MiB occupies the
upload path for about 8.1 seconds, while 256 MiB would occupy it for about
16.3 seconds. The host has 2 GiB of RAM and more than 1 GiB available, but the
larger limit would double per-request exposure without a demonstrated product
need. The production contract therefore remains exactly 128 MiB.

GitHub Actions run `36640878475` successfully built and switched the
application to `6b8a1aab879002bd4057080f69b7b1288e36a0c4`, then stopped safely
before creating a Caddy transaction because the original three-view hash
treated Caddy's generated `file_server.hide` source paths as drift. No partial
Caddy mutation or release transaction remained. The view-specific identity
repair was committed in `369c21efa4afb11758552b650adf5e5c225ee942` and the
reviewed receiver SHA-256
`bf9fbdcb86f2b1eab7b7c16a000249f8103f82292cf24ff9461ea9be02ca49bc`
was installed under the canonical maintenance lock. Its prior version is kept
at
`/opt/sub2api/backups/caddy-receiver-hotfix-369c21efa-20260929T233551Z/`.

The Caddy-only recovery used the unchanged `6b8a1aab` template and the same
root receiver used by the workflow. It completed at `2026-09-29T23:36:21Z`
with active slot `green`, template digest
`sha256:8469457ee548ed85c3cb88f0a61359d32ee2902a6ef3919ce87acf2752857257`,
and final host/startup Caddyfile SHA-256
`bb81db5a19648a277a0288c3f51abb31c0c81104bb94046e1fc31c979eae5baa`.
The rollback backup and mode-0600 completion record share the prefix
`/opt/sub2api/backups/Caddyfile.before-caddy-config-release-20260929T233619Z-6b8a1aab8790.5PcHC0`.

Post-release verification found four exact 128 MiB request-body handlers, four
Content-Length guards, and four 128 MiB operator messages in both the tracked
file and active Admin API JSON. Host and startup files were byte-identical;
there were no stale AWS 16 MiB or decimal 100,000,000-byte values. The active
application was healthy with zero restarts and no OOM, and each of
`SERVER_MAX_REQUEST_BODY_SIZE`, `GATEWAY_MAX_BODY_SIZE`, and
`GATEWAY_OPENAI_WS_CLIENT_READ_LIMIT_BYTES` appeared exactly once with value
`134217728`. Startup logs separately reported the intentional
`upstream_ws_read_limit_bytes=16777216` and
`client_ws_read_limit_bytes=134217728` values.

An authenticated 17 MiB JSON request uploaded all 17,825,792 bytes and reached
the application, which returned `404 model_not_found`; it was not rejected by
Caddy and created zero usage rows. A declared Content-Length of 134,217,729
bytes returned the expected Caddy `413` with the 128 MiB message. Public health
remained OK, both automatic timers remained disabled, the maintenance lock was
available, and no release transaction was left behind.

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
