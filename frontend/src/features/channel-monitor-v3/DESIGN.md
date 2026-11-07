# Model status cards

The owner-supplied November 7 reference defines the information hierarchy:
platform sections containing group cards, three request metrics, an 18-cell
timeline, explicit missing samples and a separate scheduled-check footer.
This extends the existing product shell and design tokens; it does not create
a new visual system or introduce imagery. Product and statistics contract:
`docs/features/MODEL_AVAILABILITY_GROUPS.md`.

Use the incumbent application font, monospaced/tabular metric values,
PlatformIcon, rounded cards, subtle borders and existing light/dark surfaces.
Operational emerald, degraded amber, failure rose and unknown gray communicate
state together with text. Request success, cache share and first-token P50 are
the primary figures; exact check models belong only to the admin group editor. Prices
and multipliers belong to the model plaza, not this dashboard.

The grid is one column on phones, two at medium widths and three on wide
screens. Search and visible refresh feedback precede platform sections.
Timeline buttons expose a textual source/status/time explanation on hover,
focus and tap. No samples, insufficient samples and stale metrics have explicit
copy and em dashes, never fabricated percentages. Failed refresh clears old
health and preserves group identity.

Public cards use the existing public shell. Signed-in cards use AppLayout;
all users see only added active public groups, independent of model-plaza or binding eligibility. Only administrators see
Manage checks and its inline editor. Save preserves the complete versioned
configuration, and model selection always remains an explicit owner action.
The homepage has its own `#status` card section with the same sample guards,
metrics and observation window. Model plaza contains prices without status.
The November 8 correction aligns 18 cells to ten-minute checks (180 minutes).
Sparse observations retain success/error labels without a percentage; a current
empty partial slot is collecting. Public configuration is grouped by platform,
searchable, excludes private/inactive groups and includes domestic providers.
Only added groups have editor rows. A searchable public-group selector and Add
button precede the list. Each row has a custom display name, a group-scoped
searchable request-model selector, a separate scheduled-check switch and Remove.
Remove hides the public card and pauses checks while preserving model/name;
readding restores those fields with checks disabled. Unchecked legacy rows stay
hidden. The model selector explains loading, error/retry, empty and unavailable
retained choices. Native routes and existing Select/tokens remain authoritative.

Initial module visual verification used desktop1440 and mobile390/320, light/dark,
ordinary-user/admin fixtures and real public/admin production views. The
deliberately insufficient fixture includes non-null66.7% to verify client
suppression. Live captures verify12public cards and12admin name fields; model
plaza has no status UI, and homepage `#status` is separate from prices.
The required finish review used an independent read-only code reviewer in
place of the unavailable named finish-reviewer role. Final disposition:
**APPROVE**, after fixing hidden-target preservation. The two detector warnings
were cross-selector false positives. Local evidence: `.omo/status-module-qa.md`,
`.omo/evidence/status-module-code-review.md` and live screenshots (not shipped
application assets). Screenshot layout evidence is distinct from API uptime.

The later explicit-selection correction passed103frontend tests, backend and
PostgreSQL checks, and a fresh independent APPROVE with no material findings.
Detector reported no findings. Real local desktop1440/mobile390 verified
add/select/name/save/remove/readd. Live origin, homepage and status module
publish exactly2selected groups; the admin editor has2rows and group-scoped
model choices. Live390px has no overflow. No production settings were saved.
Evidence and canonical release: `docs/operations/MODEL_STATUS_SELECTION_RELEASE_20261108.md`.
