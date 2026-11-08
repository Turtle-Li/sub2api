# Model group availability mail diagnosis —2026-11-08

ANALYSIS_READY. The owner reported repeated mail labelled
`Model group availability`. This is the scheduled group-connectivity notifier,
not the passive user success-rate or cache calculation. Source inspected:
`3a9ecff5965490271a9baee1e562a325439311ed`; this release does not change it.

## Evidence and mechanism

Bug/incident: STATUS-MAIL-20261108.

现象: Several generic group-availability messages reach the operator, while
current status is healthy. Expected: alert only after the claimed check's
three attempts fail; a recovered retry must not alert.

复现条件: Existing live probe rows, account cooldown/logs and durable successful
SMTP-delivery identities provide equivalent evidence. Queries were read-only
and time-bounded. No new provider request or test email was sent. This device's
QQ Mail reader is not configured; no claim is made to have read the inbox.

调用链: group runner → normal account scheduler/forwarder → durable final
probe outcome → existing Ops notification preferences → existing email template
and hourly group/outcome/recipient deduplication → SMTP → delivery marker.
The notifier does not use cache ratios or intermediate customer retry errors.

影响范围: Four selected domestic groups share WorkBuddy account246. One account
cooldown can independently trigger one alert for each of those four groups.
No unrelated recipient or private identity is published here.

疑似根因（confirmed locally）: At2026-11-08T05:10:43.874259Z
(Beijing13:10:43), the existing OpenAI gateway logged `connection refused` for
account246 and temporarily removed it from scheduling until05:20:43.816518Z.
DeepSeek17,Kimi18,GLM35andMiniMax36finished their05:10claimed checks as
failures after3attempts, with final category `no_available_account`.
Their four distinct successful SMTP markers are05:10:46.228979Z,
05:10:49.053638Z,05:10:51.864932Zand05:10:54.747017Z. They represent the
same shared-account incident across four group notifications. The exact cause
of the upstream listener refusing that connection is not established by these
Sub2API records; do not infer a WorkBuddy restart or change its runtime.

Kimi,GLM andMiniMax recovered in the05:20slot; GLM succeeded on attempt2 and
did not send a failure message. DeepSeek's05:20slot still failed3attempts,
but the same-hour delivery identity suppressed a second email. All four
succeeded from05:30through the inspected08:30slot; live08:40status showed all
six selected groups operational. Account246was active/schedulable, without
an account error; its retained cooldown timestamp had expired.

Earlier matched delivery markers belong to the already-paused Kiro29check
(November7UTC09:02–15:00), the previously corrected Kimi reasoning-only
classification, and isolated old one-attemptGLM/MiniMax checks. Keep the prior
Kimi diagnosis/release in `MODEL_STATUS_RETRY_CORRECTION_20261108.md`; do not
restore cleared history, claims or receipts to alter this explanation.

Current preferences enable alert mail, disable recovery messages and set no
additional hourly rate limit. Durable source identity still limits a continuing
failure to one successful email per group/outcome/hour. Therefore recovery is
visible in status/probe records but did not produce a recovery email.

推荐修改方案: No detector/configuration change is justified by duplicate delivery
or retry-recovery evidence here. Keep the owner's models, three-attempt policy,
account protection and existing alert preferences. If the owner later requests
less mail, shared-account incident consolidation and explicit recovery notices
are separate behavior changes; do not silently disable failure notifications.

风险: Final check categories describe the last attempt, not all prior attempts;
`no_available_account` must not be asserted for every attempt. Current health
does not erase a previous connection failure. The original24h consumption trial is
independent; retain every attempt/missing-usage receipt.

Confidence: high for trigger, deduplication and recovery, based on matching code,
private probe receipts, successful delivery hashes and the cooldown log.
Uncertainty: the underlying WorkBuddy listener event and actual mailbox receipt
are outside this evidence. No credentials, email bodies or recipients persisted.

Local sanitized artifacts: `.omo/status-probe-alert-{diagnosis,deliveries,cause,
connection}.{sql,json,jsonl}`. Notification/model settings and production data
were not changed for this investigation.
