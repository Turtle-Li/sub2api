# Taiwan ingress review disposition

Task: `SUB2-TW-CUTOVER-20260902`

Verdict before final re-review: **transport implementation ready for frozen
review; production DNS cutover remains blocked**.

## Prior review findings

| Finding | Disposition |
| --- | --- |
| Azure verifier could pass a detached or ineffective startup file | Fixed. It requires host/container SHA and device:inode equality, structurally verifies adapted startup JSON and live admin JSON, and requires equal security-contract fingerprints. The updated verifier passed on Azure. |
| End-to-end client address handling was not reviewable | Fixed for the current Caddy contract. The full production API site is frozen in `AZURE-CADDY-RUNTIME-EVIDENCE-2026-09-02.md`; the JSON verifier rejects header-trust and explicit XFF rewrites. Direct documentation links define Caddy's default connection-peer behavior. |
| Healthy old rollback origin and sole background ownership had no explicit fence | Fixed at the control-plane boundary. `sub2api-node-state.sh rollback-standby` atomically writes background standby before traffic accepting; tests cover it. The installed legacy helper was used as the equivalent `drain` then `preflight`, and both host/container views show `traffic=accepting`, `background=standby`. Azure intentionally remains standby until OAuth binding and exact-image recreation are authorized. |
| HAProxy retained root privileges and had no safe live update | Fixed and live-verified. Debian `haproxy` user/group, `/var/lib/haproxy` chroot, a retryable retained transaction, and seamless `update`/reload are present. The live verifier proves worker UID and chroot. |
| Bootstrap failure/idempotency and Debian security-mirror handling were incomplete | Fixed. Success/failure markers, healthy-rerun behavior, exact root-owned official mirror handling, and the installed Debian package revision are verified. One-shot metadata was removed. |
| Azure/HAProxy transaction failure paths could discard recovery authority | Fixed. Azure restores before state deletion; HAProxy retains explicit `STAGED`/`ROLLED_BACK` recovery state and can restage. |
| Transport tests were not in CI and stream/TLS evidence was overstated | Fixed where evidence exists. CI runs the transport regression and ShellCheck; direct Azure/GCP certificate fingerprints are compared automatically; 401 rows explicitly say no stream was carried. Authenticated streaming remains an explicit blocker. |
| DNS baseline omitted other record types and proxy status | Partially fixed. Public DNS proves A `206.119.172.211`, TTL 30, and no AAAA/CNAME/SVCB/HTTPS. Cloudflare control-plane proxy status remains blocked because the available signed-in browser surface did not return a usable page snapshot; no retired credential was reused. |
| Review snapshot was not a durable Git revision | Open. The exact working-tree hashes are frozen for review, but production cutover remains blocked until the change has a durable repository revision. Unrelated pre-existing optimizer report edits are excluded. |

## Additional defect found during finalization

Azure contained two valid retained Caddy transactions: the older customer-Host
transaction ended at SHA `878825beb35ca208496161c107e86958d3d8140bd1d758ab6868220b8e840013`,
which exactly matched the Taiwan listener transaction's starting SHA. The
listener was rolled back, the customer transaction committed after its own
semantic probes, and the listener was re-staged to
`9dc842ad2ee0ac18d89bb3f680c761170ff5b5b38b43ab6437b3c3637c766356`.

Future coexistence is prevented in both directions. The customer-Host
`prepare` refuses the Taiwan transaction; the Taiwan `stage` refuses the
customer transaction; the server release, blue-green helper, and runtime guard
refuse either. Rollback and commit remain available so an operator can always
resolve a retained transaction. The live negative mutual-exclusion check,
Azure runtime verification, and direct GCP canary passed after the repair.

## Remaining production gates

1. Create two authenticated fixed-egress proxy records and compare-and-set bind
   the two still-unbound active OAuth accounts; no raw SQL.
2. Commit or roll back the retained listener transaction, then recreate the
   Azure application from the exact retained image so state-file bind inodes
   are current and Azure can become the sole background owner.
3. Run authenticated basic generation, Responses continuation/streaming, and
   image canaries through the exact GCP address without exposing credentials.
4. Confirm Cloudflare proxy status and obtain action-time confirmation before
   changing the A record. Keep the old origin online and background-standby.
5. Record a durable Git revision for the final reviewed files.

No remaining gate is silently treated as passed. DNS remains on the old origin.
