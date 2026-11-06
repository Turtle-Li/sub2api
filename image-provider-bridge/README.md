# Sub2 image provider bridge

Reusable Python HTTP core extracted from the owner's oneclick VSO repository at
`4edef85ab3716fd43989b4a49561a148ba500d93`. This package contains no VSO queue,
leases, evidence persistence, control plane, polling loop, wallet or cost ledger.
Sub2 owns admission, attempt persistence, upstream task IDs and reconciliation.

## Run and build

Python 3.11+; `requests==2.34.2` matches the source
`skills/candidates/engineering.5/hosiery-image-generation/requirements.lock:51`.
The committed uv.lock pins transitive dependencies. From this directory:

```sh
uv sync --locked
uv run --locked python -m sub2_image_bridge < request.json
uv run --with pytest python -m pytest tests -q
uv build
```

For production install the resulting wheel into an isolated Python environment;
invoke that environment's Python with `-m sub2_image_bridge`. The main Sub2
Docker pipeline installs the locked package at `/opt/sub2-image-bridge` using
the same Alpine Python in its builder and runtime stages. Dependencies are
installed before runtime; uv is not shipped in the final image. Inherit the caller's proxy
environment. No hardcoded proxy, token environment variable or command argument.

## Protocol v0.1

One UTF-8 JSON object on stdin, EOF, one JSON object and newline on stdout.
Normal success, validation errors and provider errors exit 0; inspect `state`.
Go must bound process time, capture stdout privately, and treat process death or
an unreadable response following submit as ambiguous. Never replay a submit on
an ambiguous result. `request_key` is tracking only, not idempotency.

Nonsecret example (placeholder API key):

```json
{"provider":"seedream","operation":"submit","model":"doubao-seedream-5-0-pro-260628","prompt":"Preserve product; use a plain backdrop.","references":["data:image/png;base64,iVBORw0KGgo="],"parameters":{"size":"2K","profile_version":"4","n":1},"request_key":"sub2-item-123-attempt-1","secret":{"api_key":"ark-placeholder","endpoint_url":"https://ark.cn-beijing.volces.com/api/v3","timeout_seconds":120}}
```

Wan uses provider `wan`, model `wan2.7-image` or `wan2.7-image-pro`, profile
`2`, and a Beijing workspace endpoint shaped as
`https://ws-abcdefgh.cn-beijing.maas.aliyuncs.com/api/v1` with `sk-` credentials.
Wan submit returns `{"state":"accepted","task_id":"…"}`. Its status, collect
and cancel input is only `{provider,operation,task_id,secret}`. Status/cancel
return `{state,task_id,task_status}`; pending/running state is accepted, terminal
status state is completed. A completed status operation does not mean a delivered
image: inspect task_status. Collect fetches existing status once; only SUCCEEDED
with exactly one image downloads bytes. No internal polling or resubmission.

Seedream submit is synchronous. Successful submit/collect returns:

```json
{"state":"completed","images":[{"mime_type":"image/png","b64_json":"..."}],"usage":{"image_count":1}}
```

Wan includes task_id; Seedream includes request_id. Usage is observed image count,
not price or a billable amount. Seedream has no status, collect or cancel operation.
`{"provider":"wan","operation":"capabilities"}` or Seedream equivalent
requires no credential and returns supported operations.

Errors use `state: rejected|outcome_unknown`, stable `category` and constant
redacted message. No upstream body or upstream error code crosses this boundary.
Network/5xx/bad2xx submit results are outcome_unknown. Transport performs one
request, no retries. A 4xx valid error object is rejected. Malformed provider
objects are conservatively unknown. Go must persist its attempt marker BEFORE
invocation and retain unknown outcomes for reconciliation.

## Bounds and intentionally preserved constraints

- stdin ceiling 48 MiB; upstream JSON/stdout ceiling 34 MiB; each downloaded or
  decoded image maximum 24 MiB. Session buffering bounds decompressed bytes.
  Python peak memory can exceed these individual ceilings due to JSON/base64
  copies; these are payload bounds, not an RSS guarantee.
- timeout integer 1–600 seconds (default120); the Go process deadline additionally
  bounds total duration. Only n=1; unsupported fields and operations rejected.
- Reference data URLs only, prompt1–6000 characters. Wan accepts0–9 references;
  Seedream current Pro0–10, Lite0–14. Empty references selects text-to-image.
- Original Wan sizes and Seedream profile1–4 model/size/PNG/sequential policies
  retained. Defaults Wan2, Seedream4. No arbitrary dimensions or model aliases.
- Original Wan `.aliyuncs.com` HTTPS result allowlist, MIME checks, no redirects,
  bounded streaming and no authorization on downloads retained.
- Source request validation is extracted with orchestration-envelope predicates
  removed; generated request bodies still pass the retained native predicates.

## Source ownership and differences

See SOURCE_MANIFEST.json for source line ranges and SHA-256 hashes. Original
WanPhaseHTTPClient, SeedreamHTTPClient, result decoders, size/reference policy
functions and transport test helpers are copied verbatim. `wan_policy.py` and
`seedream_policy.py` contain body checks mechanically separated from lease and
envelope checks; expected output count is fixed to1, with unreachable multi-output validator
branches removed and sequential_image_generation_options rejected by exact field
sets. Input/body validation errors are rejected before any transport; response
parsing errors (including excessive JSON recursion) after transport are unknown. Credential/error base types
are neutral replacements. Source HTTP User-Agent remains unchanged for fidelity.
A Session subclass adds bounded JSON buffering without modifying client methods.
The boundary tightens unknown-response classification for malformed Wan2xx,
redacts errors and restricts options to the supported single-item contract.

No root license grant was found in the VSO repository. This is owner-authorized
reuse within the owner's projects, not a claim that VSO is public-domain or
permissively licensed. Preserve ownership and this provenance notice; obtain an
explicit license grant before redistributing the extracted source externally.
Requests and its dependencies retain their own licenses in installed packages.

## Intentional neutral text-to-image policy adaptation

VSO's mandatory reference images belong to its product-edit workflow. The neutral
bridge permits `references: []` using the same HTTP clients: Wan sends a single
text item in the user message; Seedream omits the optional image field. This
adapts only the extracted body predicates; original helper functions remain
verbatim. Model, upper reference limits, output size, PNG and sequential policy
remain fixed. Official contracts verified by the integration owner on 2026-11-07:
[Wan generation/editing API](https://help.aliyun.com/zh/model-studio/wan-image-generation-and-editing-api-reference)
and [Ark image generation API](https://docs.volcengine.com/docs/ark/image-generation-api?lang=en).
Tests exercise exact text-only bodies and one transport call for both providers.

## Read-only admission catalog for Go

`capabilities` now includes `models`, a catalog of the exact current default-profile
model IDs (Wan profile2, Seedream profile4). Each entry supplies `model`,
`profile_version`, `image_sizes`, `default_image_size`, `default_aspect_ratio`,
`max_reference_images`, `response_mime_types`, and `sizes` entries shaped as
`{image_size,aspect_ratio,size}`. There is no network call or credential input.
No new `validate` operation was added: Go can reject unsupported model/tier/ratio
combinations before reserving credit by consulting this catalog.

Go must select a row and forward **its exact `size` string** as `parameters.size`,
together with the entry's `parameters.profile_version`. Do not send `image_size`
or `aspect_ratio` fields to submit (those are public Go/UI fields, not bridge
request fields). Defaults are2K and1:1. For example Wan1K/1:1 maps to1024*1024;
Wan2K/4:5 maps to1638*2048. Seedream Pro1K/1:1 maps to1024x1024;
Seedream Lite2K/4:5 maps to1920x2400. The catalog publishes all9 source-approved
ratios for each supported tier. Pixel rounding is retained exactly from source.

Published default-profile tiers: both Wan models1K/2K; Seedream Lite2K/4K;
Seedream Pro1K/2K. Legacy profiles remain callable for frozen work but are not
advertised as new selectable models. A model matching a source's future-date
pattern is also not advertised until an exact model constant is pinned.

Size strings come from the original frozen size sets and are filtered through
original model/size policies. Ratio labels classify existing integer dimensions
with bounded rounding tolerance; they never synthesize new dimensions. Maximum
reference counts come from original policy predicates. The response MIME list
expresses formats the original collectors can decode, not an output-format
selector or a promise that all three formats will be produced. Pro still requests
PNG through its original body policy. No additional generation options are added.

## Sub2 Go runtime installation (disabled by default)

The Go runtime uses `batch_image.python_bridge_executable` (environment
`BATCH_IMAGE_PYTHON_BRIDGE_EXECUTABLE`), default empty. Install this package before
starting the server: `uv sync --locked --no-dev --no-editable`, then configure the
absolute `.venv/bin/python` path. No server operation installs dependencies.
A missing executable, missing module, invalid catalog or absent shared account
runtime hides the native models. Restart after replacing the package/catalog.

Optional Docker path from repository root (no deployment or activation):
`docker build -f image-provider-bridge/Dockerfile --build-arg SUB2_IMAGE=sub2api:local -t sub2api:bridge-local .`.
The base image must already be built from this checkout. This installs the locked
package at build time; configure `/opt/sub2-image-bridge/.venv/bin/python` only
when intentionally enabling it. The main production Dockerfile now includes the
same locked installation, so current release images do not need this extension.
Packaging does not enable the backend configuration: its default stays empty.
The Docker build runs both providers' credential-free capabilities operations
as UID1000 with networking disabled, verifies two models per provider and nonempty
size catalogs, and fails if the installed module or locked requests version is
unavailable. This runtime layer can be checked without compiling the application:
`docker build --target image-bridge-runtime -t sub2api:bridge-runtime .`.
Production still builds the exact main commit in the manually dispatched release
workflow; no server compilation, extra mount or privilege is required.
The image retains this provenance notice, SOURCE_MANIFEST.json and the adopted
Image Studio UI's original LICENSE under `/app/licenses/image-studio/`.

Use existing OpenAI API-key accounts with exact identity model mappings and
positive configured per-image prices. Provider IDs are `wan_images` and
`seedream_images`; native endpoint/key validation remains in the Python core.
Wan uses the documented workspace HTTPS base URL; Seedream uses the fixed Ark
endpoint. Keys are server-side account credentials only. Never add them to
browser payloads or command-line arguments.

Every actionable bridge operation acquires the shared account concurrency slot.
It is held for the child lifetime, released while waiting on remote Wan work,
and reacquired for collection. An account HTTP(S) proxy replaces all inherited
proxy and no-proxy aliases for the child. Unsupported proxy schemes fail closed;
without an account proxy the configured parent network environment is inherited.
The Go runner discards stderr, bounds JSON input/output and process time, and
never replays an unknown submission. Wan receipt recovery invokes the original
collect action (GET status then approved download), never another submit.
Native source resolution is stored directly without the Image2.5 upscale stage.
The public `capabilities` DTO is derived from the Python catalog. MIME entries
are accepted response types, not a requested transcoding guarantee.
