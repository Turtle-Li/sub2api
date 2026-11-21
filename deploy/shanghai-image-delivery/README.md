# Shanghai Image Studio delivery

This is a data-plane service, not an Image Studio origin. The authorized host
is the Shanghai 4C8G former image site, Registry `srv-image-124222154203`. Use
the already-pinned operator identity in the Registry and the former site's
`deploy/tencent-cloud-image-migration.md`. AWS remains the application and sole
task/billing authority. Do not use a different CVM or the game server. Do not
restart or reconfigure the colocated VSO, Daypass, database, tracking, or Nginx
services. The video workspace and upstream Video URL retention are out of scope.

## Runtime boundary

- Independent service `sub2api-image-delivery.service`, binary at
  `/opt/sub2api-image-delivery/image-delivery`, mode-0600 deployment config at
  `/etc/sub2api-image-delivery.conf`, private state under systemd
  `StateDirectory=sub2api-image-delivery`. No long-lived object-store, Mini,
  application, or database credentials are installed. New uploads record only
  the hash of a short-lived one-slot token. AWS signs exact COS object URLs
  using its existing Vault-backed credentials. Signed URLs stay in memory
  during the write and are never retained or logged.
- Port `18486` binds only to the CVM's existing Tailnet IPv4 address and
  accepts only the AWS peer's actual source IP. No ACL or public ingress
  change is required for a control-only smoke. For Mini, an optional
  separate, private Tailscale HTTPS Serve listener can proxy port `18488`
  to loopback `18487` only. Do not configure Funnel or attach this service to
  a public origin. Check the Mini's existing authorized route before enabling.
- Private spool is bounded to 48 MiB per image, 2 GiB total in flight, and
  4,096 slots. Incoming images must have a supported MIME and bounded
  dimensions. The Mini direct grant binds the exact slot and expected
  dimensions. The existing Mini upload contract was verified against the
  deployed private Registry revision `aaaee866b147598026fb5fc6351d3e1857b5c9c9`,
  under `deploy/office-mini-upscale-api`. Only a staged result and a
  verified exact COS object count as delivered. The Mini keeps the existing
  upload fields and never receives broad COS credentials.
- Temp bytes are synced and renamed before acknowledging receipt. A failed
  COS operation leaves the same local image for retry. Before a COS PUT the
  node checks the exact target with signed HEAD. Matching size, MIME and
  SHA-256 metadata reconciles a lost acknowledgement without another PUT.
  Mismatched objects are not overwritten. The COS request is synchronous with
  a 30-second outbound budget; there is no detached upload or retry queue on
  the node, and AWS must hold its existing indexing write permit while issuing
  a publish. Pending bytes must not be manually deleted or recycled on timeout.

## First canary contract (not activated)

- AWS uses the existing source checkpoint only for a designated user's
  session-wallet job, one image, exact Image 2.6 Flare or Sunburst, and 2K.
  The persisted output marker pins the route before Mini or COS work. Indexing
  then does not require the original account or a merged base64 JSONL object.
  This lane does not apply to API-key jobs, Gemini/Vertex, other
  tiers/providers, or video.
- Normally Mini uploads directly to Shanghai over the isolated Tailnet HTTPS
  listener. Shanghai writes the original to COS and creates the standard
  480-edge JPEG from the local spool. AWS persists only the small preview via
  its existing thumbnail fence. Preview failure does not block delivery of a
  verified original; the existing bounded backfill can still repair it.
- The compatible Mini companion is the **not yet deployed** Registry revision
  `0169abe976f3251e6ff9f3ac7c66fa3dd4ae2390` (`deploy/office-mini-upscale-api`).
  It adds `direct_upload_recovery` to `/health` and permits authenticated GET of
  the same job's pixels after a failed direct upload. Only this Mini recovery
  path pulls those pixels back through AWS, checkpointing them before staging
  to Shanghai. Already-2K sources and completed Aliyun bytes also use the final
  checkpoint.
- Local queue overflow, a Mini outage detected before submission, explicit
  rejection (429), or a recorded failed Mini job with no output can trigger
  Aliyun. Keep the shared limiter and persist the one-shot claim immediately
  before the paid request. A failed direct upload with output, ticket expiry,
  missing Mini job status, or an ambiguous paid response does not authorize
  new work. Keep any recoverable result with retries and Feishu alerts. If a
  receipt is missing, reconcile the actual outcome before an operator acts.
- After exact COS verification and item indexing, the existing wallet ledger
  settles once. Access remains 360h from settlement. Only then does the active
  worker acknowledge and remove the Shanghai slot. The DB marker stays pending
  until the idempotent ACK and its DB confirmation both succeed. Monitor spool
  age and capacity; this is not sized or authorized for general traffic.

## Install and validate without customer traffic

1. Verify the active AWS identity and the CVM/registry assignment read-only.
   Verify that ports `18486`, `18487`, `18488` and the three dedicated paths
   above are unclaimed. Record current service health and disk space. Make no
   changes outside this service.
2. From the reviewed checkout, run `go test -race ./internal/pkg/shanghaidelivery`
   under `backend`, then build `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build
   -trimpath -o <local-temp>/image-delivery ./cmd/image-delivery`. Compare the
   local/remote SHA-256 before installing. Install the binary and service unit
   root-owned, then validate the unit with `systemd-analyze verify`.
3. Configure these non-secret values in the dedicated service's mode-0600 file:

   ```text
   IMAGE_DELIVERY_LOCAL_TAILNET_IP=<verified CVM peer>
   IMAGE_DELIVERY_AWS_TAILNET_IP=<verified AWS peer>
   IMAGE_DELIVERY_COS_HOST=<current private bucket virtual-host name>
   IMAGE_DELIVERY_COS_PREFIX=<current prefix without leading or trailing slash>
   IMAGE_DELIVERY_INGRESS_URL=
   ```

4. Start the service without enabling it on boot. Confirm the isolated
   listener, AWS `/health` returns `204`, other peers cannot use the controller,
   and neither the application nor the old site's listeners are altered. Send
   only a synthetic image with a random QA ID under the allowed COS prefix.
   Use the existing Vault agent to issue the short-lived exact PUT and HEAD;
   do not display the URLs or broadly copy credentials. Restart the dedicated
   service before publish, re-read the receipt, publish, and repeat publish
   on the same key. Compare an end-to-end checksum and remove only the scoped
   smoke object and acknowledged local slot after verification.
5. Before production routing, also verify Mini private HTTPS and result
   recovery, exact AWS build/config, gallery output and one idempotent wallet
   settlement. Verify app retention remains 360h and obtain the existing
   AWS blue-green gates. A listening service or a successful COS test by itself
   does not enable Image Studio on AWS.
6. Install the compatible Mini and Shanghai builds before enabling the
   application flag. Keep `BATCH_IMAGE_SHANGHAI_DELIVERY_URL` unset by default.
   For the canary, set it to the verified Tailnet control origin and set
   `BATCH_IMAGE_SHANGHAI_DELIVERY_CANARY_USER_ID` to the approved user. Set
   only the canary user to `0` to stop admission but continue recovering
   existing jobs. Reconcile the pinned jobs and ACKs before removing the URL,
   changing its object prefix, or activating an incompatible rollback image.

### Control-only smoke — 2026-11-21 Pacific time

The dedicated systemd unit is running in control-only mode, not enabled at
boot, and no app traffic is routed to it. A synthetic 5,153,193-byte image
staged from AWS in 0.846 s. The service restart preserved the exact digest
and byte count. The first publish including HEAD verification completed in
0.616 s; repeating the publish against the already confirmed object took
0.195 s. The exact-object AWS HEAD matched the checksum metadata and length
without reading the image. The QA object and staged file were cleaned up.

The AWS host had six successful direct Tailnet pings (80–82 ms) and six
successful private health requests (162–244 ms); the active app container
also reached health. The CVM resolved the COS endpoint to a non-public
address. These samples establish functional connectivity, not a long-term
SLA or comparison to a dedicated public HTTPS ingress. Watch the actual
Tailnet path (direct vs. relay), timeouts, pending spool age and throughput
before production routing. Mini ingress and application cutover are not
enabled by this test.

Local application and PostgreSQL checks cover the same Mini job after failed
direct upload, a lost COS PUT acknowledgement across worker restart, rejection
and one-shot cloud admission, one wallet capture, and ACK after durable
settlement. These tests do not exercise a live paid upstream or Feishu webhook.

The current private route is not proof that the public or private option will
always be more stable. Compare the same payloads, errors, and tail latencies
before changing the cross-region control path. Keep the same-region COS leg
internal unless measurements justify a change there too.

## Stop or recover

For canary rollback, only stop `sub2api-image-delivery.service` and, if one
was installed for this service, remove its exact private Serve listener. Do
not reset Tailnet Serve or Funnel globally. Retain private state. For an active
production lane, drain or explicitly reconcile staged outputs before turning
off the lane or promoting an incompatible AWS application. An ambiguous
remote result is not authorization to rerun generation or remove a slot.
