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
the primary figures; exact check models belong only to collapsed admin request settings. Prices
and multipliers belong to the model plaza, not this dashboard.

The grid is one column on phones, two at medium widths and three on wide
screens. Search and visible refresh feedback precede platform sections.
Timeline buttons expose a textual source/status/time explanation on hover,
focus and tap. No samples, insufficient samples and stale metrics have explicit
copy and em dashes, never fabricated percentages. Failed refresh clears old
health and preserves group identity.

Public cards use the existing public shell. Signed-in cards use AppLayout;
all users see active public groups, independent of model-plaza or binding eligibility. Only administrators see
Manage checks and its inline editor. Save preserves the complete versioned
configuration, and model selection always remains an explicit owner action.
The homepage has its own `#status` card section with the same sample guards,
metrics and observation window. Model plaza contains prices without status.
The November 8 correction aligns 18 cells to ten-minute checks (180 minutes).
Sparse observations retain success/error labels without a percentage; a current
empty partial slot is collecting. Public configuration is grouped by platform,
searchable, excludes private/inactive groups and includes domestic providers.
Each row has a custom display name; request-model fields stay collapsed.

Bounded visual verification used fixture data at desktop and 390px mobile,
light/dark, ordinary-user and administrator views, plus homepage summaries.
The deliberately insufficient fixture also contained non-null percentages to
verify client suppression. Screenshots prove layout, not live availability.
The required finish review used an available independent read-only code
reviewer in place of the unavailable named finish-reviewer role. Final
disposition: **ship**, no material fixes; the two detector warnings were
cross-selector false positives. Local evidence: `.omo/status-cards-qa.md` and
`.omo/evidence/status-cards-code-review.md` (not shipped application assets).
