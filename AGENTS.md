# Independent Sub2API customization

- This checkout tracks https://github.com/ZvonimirSun/sub2api.git; it is a separate project from sibling Sub2API deployments. Do not inherit their servers, databases, accounts, or deployment targets. Deployment authority and operational boundaries are recorded in the private project deployment record; in the primary checkout see local `docs/deploy/test-server.md`. Never infer a target from sibling projects.
- Codex turn-state monitor requirements, upstream reference snapshots, integration boundaries and validation: `docs/features/codex-turn-state-monitor.md`. Read these before modifying this feature.
- Upstream baseline for the initial customization: `881f3202694c6bc932446931a30c27d9675178b9`. Preserve existing Codex identity, account isolation and model auditing contracts.
- Never persist account credentials, proxy passwords or raw turn-state values in frontend state, task results, logs, fixtures or documentation. Reuse the application's account/proxy storage and authentication.
