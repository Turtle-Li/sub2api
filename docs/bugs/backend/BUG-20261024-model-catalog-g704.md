# Model protocol catalog G704 false positive

Bug: BUG-20261024-model-catalog-g704

现象: golangci-lint v2.14.0 reports G704 at the model catalog's
`http.NewRequestWithContext` call. The same finding reproduces on unchanged
baseline `c4d096ebea9dbbcd0a495ebe56f815181e6f2484`; it was not introduced by
the Cline capability pin change.

复现条件: Run `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run --timeout=5m ./internal/service/...`
from `backend/`.

调用链: `modelCatalogProtocols` reads the account-owned Chat base URL and
captures immutable URL/header values for the background refresh.
`fetchModelProtocolCatalog` calls `validateUpstreamBaseURL`, which delegates to
`validateOutboundURL`, before constructing or sending any request. The
repository HTTP upstream additionally enforces resolved-IP policy when the
configured URL allowlist requires it. The request disables redirects.

影响范围: Background protocol catalog refresh and the service lint gate.
Account URL and HTTP/private-host policy are administrator-owned configuration.
Allowlist-disabled mode intentionally permits custom destinations; an enabled
allowlist fails closed for missing/nonmatching hosts, and rejects private hosts
unless explicitly permitted. This finding alone does not establish a bypass of
those documented boundaries or a lower-privilege SSRF exploit.

疑似根因: ANALYSIS_READY, high confidence. The pinned analyzer dependency is
gosec v2.29.0. Its `analyzers/ssrf.go` registers no SSRF sanitizers and explicitly
states that custom allowlist validation is not modeled. The analyzer propagates
taint through the project's validator even when validation succeeds. Existing
runtime validation is present; the unrecognized trust boundary is the cause of
this report.

推荐修改方案: Retain the URL validation and no-redirect behavior. Add a
point-specific G704 explanation at the already-validated constructor, following
the production-code policy in `backend/.golangci.yml`. Add catalog-boundary
tests proving invalid destinations cause no HTTP request or credential send,
and explicit HTTP/private-host options preserve their existing behavior. Do
not disable G704 globally, change allowlists, or restructure the HTTP path to
hide taint from the analyzer.

风险: The allowlist is an operator-controlled policy. Its disabled mode is not
an SSRF protection promise; this change must not silently tighten it and break
intentional private upstreams. Existing DNS/rebinding policy remains owned by
the shared HTTP repository. No application behavior, migration or production
configuration change is required for the false-positive annotation.

Validation: the ten catalog policy cases passed before the annotation, confirming
the existing boundary. After the annotation, full backend golangci-lint v2.14.0
reported `0 issues`. The focused service/repository suite passed with
`-race -shuffle=on -count=1` (3.304s / 11.587s), including real HTTP redirect
suppression and resolved-IP/private-host tests. No global linter exclusion was
added. This is a false-positive disposition, not a newly repaired SSRF exploit.

The Cline capability release record links the final commit and deployment result.
