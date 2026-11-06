# BUG-20261107-image-catalog-group-price

Bug: native image catalog ignores configured group tier prices.

现象: a native model with a positive group image price is omitted from discovery.
复现条件: Muse/WorkBuddy (also native bridges), no global image card, positive group tier.
调用链: ListModels calls the global BatchImageUnitPrice directly; Submit calls
resolvePricingSnapshot, which first reads Group.GetImagePrice(requested tier).
影响范围: native image discovery; no provider transport or ledger change required.
疑似根因: confirmed static divergence at ListModels pricing gate versus
resolvePricingSnapshot. The real resolver cannot price an unknown native model
globally; the configured group price is never consulted by the former.
推荐修改方案: native discovery reuses the existing submission pricing snapshot
for supported tiers, accepting a model only if at least one tier has a positive
finite price. Preserve legacy Gemini/OpenAI discovery and provider readiness.
风险: missing, zero and malformed native prices must remain unavailable; never
invent global tariffs or relax readiness to make discovery pass.

Analysis status: ANALYSIS_READY. Provider contracts inspected: WorkBuddy
a19c78fc (images.go/tests), Muse app.py native task contract documented at
5542a479; the change does not alter either upstream transport. Existing reference
and license records remain authoritative. Local branch preflight reports
CANONICAL_NOT_READY because .agent-worktree.toml is absent; this is the parent's
explicit shared worktree, with Git/release owned by parent.

Validation evidence is recorded in .omo/evidence/image-catalog-group-price/.
Independent review and production verification remain parent-owned.
