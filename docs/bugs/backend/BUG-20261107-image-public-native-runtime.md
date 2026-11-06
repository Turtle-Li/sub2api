# Native batch public runtime wiring

Bug: BUG-20261107-image-public-native-runtime

现象: The public submission path rejects native providers before creating a job
with `BATCH_IMAGE_NO_ACCOUNT_AVAILABLE`, even with an eligible account. Root
observed this against release4649ae0d2; this worker performs no production calls.

复现条件: Construct the public service through `ProvideBatchImagePublicService`
with a provider object store and otherwise complete gateway dependencies.

调用链: Wire → ProvideBatchImagePublicService → runtime provider registry →
native provider SubmissionAvailable → nil gateway → unavailable.

影响范围: Muse, WorkBuddy, and configured Wan/Seedream public admission. The
worker runtime has its own correctly supplied gateway. Legacy OpenAI admission
does not require its executor, so its successful operation did not expose this.

疑似根因: Direct source evidence: the public provider function explicitly passes
nil instead of the shared OpenAI gateway. Missing gateway transport/concurrency
checks correctly fail closed. There is no BatchImage dependency in the gateway
constructor, so normal Wire injection is sufficient without a lifecycle hook.
This is independent of the catalog's group-price resolution defect.

推荐修改方案: Inject the shared gateway into the public provider function and
regenerate Wire. Preserve all runtime checks and the disabled-by-default bridge.
No provider transport, model identity, retry, account or ledger change is needed.

风险: This makes configured native providers available to admission; account,
pricing and opt-in checks still apply. Regression exercises the actual public
provider constructor with complete and missing dependencies. Deployment and
real upstream acceptance remain root-owned.

Developer evidence: `.omo/evidence/public-native-runtime/` contains the failing
constructor regression, passing focused/broader tests and Wire generation log.
Reviewed provider contracts remain the pinned Muse and WorkBuddy feature docs;
this repair changes local composition only, not their upstream adapters.
