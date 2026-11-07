# Image Studio administration

The admin route `/admin/image-models` now begins with a deduplicated model directory.
Selecting a model opens its existing group image settings, shared allowlist and
bound-account controls; internal group switching is in a collapsed advanced
section. It uses the normal Vue admin shell/components. The November 8 redesign
is documented in `IMAGE_STUDIO_REDESIGN_20261108.md`; source adoption remains in
`frontend/src/features/image-studio/UPSTREAM.md`.

Only groups with image or batch-image access configured enter this directory.
Catalogue reads are bounded to three in parallel. A configuration-ready binding
is preferred, then a batch-enabled binding, with group ID as the stable tie-break.
Rows show the selected binding's existing USD source-price configuration for the
model's supported resolution tiers, before existing multipliers; they are not a
new global quote. Zero remains a real configured price. The configuration panel
identifies the source group and shared price scope. Separate model prices can
continue to use separate image groups. No default account, rate, allowlist or
provider is changed when this page is shipped.

## Configuration ownership

- Group image switches, tier prices, image multiplier and batch settings use the
  existing group update API. Only changed fields are submitted. No additional
  configuration store, image executor, pricing engine or settlement ledger exists.
- `model_allowlist` remains a shared group-wide rule. Text models, custom entries
  and wildcard rules must survive edits made from this page. An exact image model
  cannot be disabled while an unchanged broader rule still permits it. Enabling
  this whitelist restricts all model calls for the group, not only image jobs.
- Existing account editing owns credentials, model mapping and group bindings.
  Native image adapters still use the documented OpenAI-compatible account shape;
  the WorkBuddy text account platform is not the image-adapter account shape.
- No existing production group/account is activated just by shipping this page.

## Read-only catalogue

`GET /api/v1/admin/groups/:id/image-models` returns the standard panel envelope:
`models`, `candidates`, and nullable `unavailable_reason`.

`models` reuses `BatchImagePublicService.ListModels` and the existing group model
allowlist, with inactive groups excluded. `candidates` lists recognized image
models for configuration and can include an unavailable provider. Candidate
presence is not proof of credentials, pricing, runtime activation or scheduling.
The UI says “configuration available”; this endpoint never generates an image or
claims a successful remote-provider health check. User-specific key, entitlement,
balance and request validation still apply when the user submits a real job.

## Official WorkBuddy retry mode

`PATCH /api/v1/admin/accounts/:id/workbuddy-image-policy` accepts only a strict
boolean `native_retry_accepted`. The target must be an OpenAI API-key account with
an exact WorkBuddy image identity mapping. The server uses the existing atomic
`UpdateAccountExtra` merge, preserving unrelated keys and the independent
`workbuddy_image_single_attempt_verified` attestation. Existing transactional
scheduler publication and admin mutation auditing remain in use.

This account-level setting affects every group bound to that account. It does not
pin, patch or update the official gateway. Its upstream may retry internally and
consume extra upstream quota; Sub2 retains its own no-unknown-replay and single
settlement contracts. Turning off native acceptance does not clear an existing
independent single-attempt attestation. See
`WORKBUDDY_IMAGE_PROVIDER_20261107.md` for the pinned source and original tests.

Both new routes require the normal full-admin boundary. Restricted management
keys are not granted these routes; future scoped-key support must add resource
authorization as well as a route permission, not only a permission-table entry.

## Verification and rollback

Targeted backend tests cover authorization, catalogue filtering and strict policy
updates; frontend tests cover shared whitelist preservation and form state.
Browser acceptance uses an explicitly labelled local synthetic fixture, with no
production credentials or paid generation. Exact executed checks and screenshots
are recorded in `.omo/evidence/image-admin/` during delivery.

There is no schema migration. Application rollback uses the previously compatible
release image and preserves the existing group/account configuration and ledger.
