# Cline non-streaming provider attribution

Bug: BUG-20261025-cline-provider-attribution

现象: After the capability pin release, a normal production Chat Completions
request returned `choices[0].message.provider_metadata.gateway.routing` with
`finalProvider: deepseek` and canonical slug
`deepseek/deepseek-v4.1-flash-1017`. Its new usage row recorded account 221 and
the correct Pass SKU, but `upstream_provider` was NULL. The management test
correctly reported DeepSeek. Existing October 23 usage has the same NULL gap.

复现条件: Send a non-streaming request for the configured DeepSeek Flash-1017
alias through the public gateway, then match its request ID to the new usage
row. Do not infer provider identity from the answer text or HTTP status.

调用链: `forwardAsRawChatCompletions` buffers the upstream response, calls
`upstreamResponseModelObserver.ObserveOpenAI`, and copies its provider into the
forward result used by usage persistence. `ObserveUpstreamProvider` reads only
top-level routing metadata, its `data` envelope, and direct-provider fields.
The management parser already supports routing under `choices.0.message`.

影响范围: Non-streaming planner provider attribution in usage records. Actual
request pinning works, as the upstream's routing metadata proves. No account,
group, billing calculation, migration or historical usage rewrite is needed.

疑似根因: ANALYSIS_READY, high confidence. The shared response observer omits
the nested message routing shape used by the real Cline response and the
existing management parser. The fixed switcher reference recorded in
`../../operations/CLINE_CAPABILITY_PIN_20261024.md` supports this same shape.

推荐修改方案: Add the nested message routing paths, including the existing
`data` envelope, to the shared observer. Add regression fixtures and assert
provider propagation through the real raw forwarding method. Preserve empty
attribution when metadata is absent or not a string; never derive it from a
configured pin. Re-run gateway production acceptance after publishing.

风险: This observer is shared by OpenAI-compatible responses. Keep existing
top-level/direct-provider handling and last-declaration behavior, and run the
shared observer and forwarding regressions as well as the Cline tests.

Regression evidence: before the observer repair, both nested JSON fixtures and
the real `forwardAsRawChatCompletions` result assertion failed with expected
`deepseek`, actual empty string. The non-string fixture remained empty.

After repair, the shared observer, raw forwarding and Cline suites passed with
`-race -shuffle=on -count=1` (6.119s). `go build ./...`, full backend
golangci-lint v2.14.0 (`0 issues`) and `git diff --check` passed. Hosted CI and
the final production acceptance are linked from the capability release record.
