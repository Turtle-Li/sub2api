# Batch image delivery switch drifted from mounted configuration

## Symptom and impact

The active AWS application container carried `BATCH_IMAGE_DELIVERY_ENABLED=false`
while the mounted `config.yaml` set `batch_image.delivery_enabled: true`.
Because Viper enables automatic environment binding, the stale container
environment won and silently disabled durable batch-image result delivery.
This affected the new OpenAI Images and Gemini/Vertex batch delivery path;
no customer batch was enabled during the incident window.

## Root cause

The blue-green release helper copied most of the previous container
environment into the new slot. It stripped managed Image Upscale and COS
credential variables, but did not strip `BATCH_IMAGE_DELIVERY_ENABLED`.
That key was historically created when delivery was disabled and survived
subsequent releases. The candidate and runtime guard contracts also did not
reject the stale override, so a healthy container could pass lifecycle checks
with an effective setting different from the mounted configuration.

## Repair contract

`BATCH_IMAGE_DELIVERY_ENABLED` is configuration-owned. Forward releases remove
every inherited occurrence and never emit the key into the application
container environment. Candidate matching and the runtime guard reject any
explicit occurrence, including `false`; the application therefore resolves
the setting only from the mounted configuration.

The repair is covered by the external blue-green mock test (stale old-slot
environment is stripped and a precreated stale candidate is rejected) and the
runtime-guard fake-Docker test (a healthy container with the override is
rejected before lifecycle work).

## Verification and rollout

Local syntax, blue-green external-runtime, and runtime-guard tests passed on
2026-09-30 before production helper installation. Production rollout must
verify that the active container has no `BATCH_IMAGE_DELIVERY_ENABLED` entry,
that the mounted YAML remains enabled, and that the COS Vault sidecar and
public health checks remain healthy. The release must remain behind the
canonical maintenance lock and blue-green helper.

## Rollback

Revert the helper and runtime-guard commit through the normal owner-repository
blue-green deployment workflow. Do not manually inject the switch into a live
container; if delivery must be disabled, change the reviewed YAML configuration
and perform a normal release so the effective setting is auditable.
