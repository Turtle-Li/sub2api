# Image Studio: model selection and embedded creation rail

## Owner-selected contract

The owner rejected the duplicated standalone header, forced black theme, exposed
group selector and separate batch guide. After seeing rendered Kimi and Gemini 4
comparisons, the owner selected Kimi's left input rail and right gallery. Claude
reviewed that choice and also supported the rail. This supersedes the initial
dark floating-composer visual contract, without replacing the adopted core code.

Kimi used the configured `kimi-k3.1-pro` relay after its official OAuth CLI denied
subscription access. Gemini's actual response identified `gemini-4-argon` at high
reasoning; Claude Code's configured `claude-opus-5-5` completed a read-only review.
Only a sanitized screenshot and design brief were sent to these consultations.
Specifications, rendered comparisons and review notes are local delivery evidence
under `.omo/design/image-studio-redesign/`; no model credentials are versioned.

The existing Vue AppLayout supplies the only page header and navigation. The
Studio uses the host light/dark theme, a 320px desktop creation rail and a gallery
with status/search filters. The same form opens as a native modal on phones, with
focus containment, prompt focus, a sticky heading and generation action, and safe
area padding. It has explicit single/batch modes, capability-driven parameters,
reference removal and continuous editing. Production has no prototype-only demo
switches or sample task data.

## Reuse and ownership

`frontend/src/features/image-studio/UPSTREAM.md` retains the MIT source pin
`88lin/gpt-image-studio@5523a592af054bfe4cbee76c2ef8d866ce5cd81e`, adopted modules
and intentional differences. The pinned InputBar source and virtual-grid tests
were checked again before modifying the host adapter. Core task states, measured
virtualization, lazy image reads, viewer gestures and continuation lifecycle are
retained. The rail and card geometry are scoped CSS/controlled host presentation.

`workspaceAPI.ts` uses the existing non-secret owned-key directory and existing
key-scoped session clients. It deduplicates provider/model identities, chooses a
stable eligible internal route, retains separate history cursors, and binds image
reads and cancellations to the original key. Directory fan-out is bounded to three.
A continuation stays on its original key; changing to an incompatible route keeps
the reference and explicitly starts a new work. A user/session change aborts old
reads. No provider credential is sent to or stored in the frontend.

Live acceptance exposed the existing per-user image-download concurrency limit:
parallel thumbnails from multiple keys returned HTTP 429. The host workspace now
serializes thumbnail/detail/edit reads together, retaining original-key ownership,
continuing after a failed asset and refusing queued work after abort. It neither
raises the server limit nor repeats generation. The canonical public item status
is `succeeded` (`BatchImageItemToPublic`); status labels and edit-reference advance
accept it alongside the older aliases. Regressions first reproduced four failures
with the real contract before the host-only correction.

Same-body uncertain submissions retain their idempotency identity and original
route. There is no automatic POST replay or cross-provider/key failover. Task and
settlement facts still belong to the server; failure does not imply a refund.
The original ledger, holds, quotas, shared admission and provider adapters remain
unchanged. Official WorkBuddy updating and retry policy remain unchanged.

The old 2,914-line batch guide view and sidebar entry are removed. Its two URL
forms redirect to `/image-studio`; dashboard shortcuts now open that workspace.
Existing batch APIs, persisted tasks and billing are retained.

## Price administration

The owner clarified one configured price per model, while allowing existing group
pricing internally. Admin now starts from models instead of choosing among all
text/subscription groups. Clicking a row reveals the existing group settings and
accounts. Advanced routing/allowlists remain collapsed. The source group and
shared scope are explicit: separate image groups can retain separate model prices.
The directory shows existing source-price configuration, not a new global quote.
This delivery does not alter live prices, permissions, accounts or gateway policy.

## Acceptance and rollback

Targeted frontend regressions cover model deduplication, per-key paging/assets,
uncertain submission pinning, cross-key continuation rejection, server parameters,
two-edit reference advance, cancellation, viewer lifecycle, admin grouping and
price/allowlist preservation. `pnpm build` includes locale completeness and type
checks. Changed executable UI files also pass scoped ESLint.

The actual application is exercised against an explicitly labelled synthetic local
API, covering desktop/phone, light/dark, mixed task states, batch submission, edit
handoff, cancellation and admin save/list refresh. A 390x500 viewport verifies a
reduced visible height; it is not an actual iOS keyboard test. Screenshots and the
precise final QA/review verdict are in `.omo/design/image-studio-redesign/QA.md`.
Local fixture generation does not establish live provider availability.

No schema or pricing migration exists. Rollback uses the previous compatible
blue-green image, retaining tasks, billing, group/account configuration and API
ownership. No database restore or gateway version pin is needed.
