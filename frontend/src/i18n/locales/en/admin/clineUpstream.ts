export default {
  clineUpstream: {
    title: 'Cline Upstream Pinning',
    description:
      'Enumerate, validate and pin the upstream provider behind Cline Pass subscription models (cline-pass/*).',
    pipelineHint:
      'Cline Pass routes each model through one of two pipelines: direct (OpenRouter, honours top-level provider.only/order/sort) or planner (Vercel AI Gateway, honours providerOptions.gateway.*). The pipeline is decided by Cline and can change over time, so probe before pinning.',
    probeCostHint:
      'Probe, validate and test each send one tiny real request through this account (a fraction of a cent per call).',
    refresh: 'Refresh',
    probeAll: 'Probe all',
    probing: 'Probing…',
    accountLabel: 'Cline account',
    selectAccount: 'Select a Cline account',
    loadingAccounts: 'Loading Cline accounts…',
    noAccounts: 'No Cline account',
    noAccountsHint: 'Create a Cline API-key account first, then come back to pin its upstreams.',
    unsupported:
      'This account cannot be probed: upstream enumeration requires an API-key account pointing at the official api.cline.bot host.',
    unsupportedHint:
      'Pinning configuration is still stored and injected for this account, but the provider list must be entered by hand.',
    modelsError: 'Model list could not be refreshed: {error}',
    emptyModels: 'No model to configure',
    emptyModelsHint: 'Probe the account to load the Cline Pass subscription models.',
    configuredCount: '{count} pinned',
    lastFetched: 'Updated {time}',
    notProbed: 'Not probed',
    noUpstreams: 'No upstream learned yet',
    columns: {
      model: 'Model',
      source: 'Source',
      pipeline: 'Pipeline',
      upstreams: 'Upstreams',
      lastProvider: 'Last provider',
      mode: 'Pin',
      actions: 'Actions'
    },
    source: {
      subscription: 'Subscription',
      catalog: 'Catalog',
      configured: 'Local only'
    },
    pipeline: {
      direct: 'Direct (OpenRouter)',
      planner: 'Planner (Vercel)',
      unknown: 'Unknown'
    },
    mode: {
      off: 'Off',
      strict: 'Strict pin',
      preferred: 'Preferred + fallback'
    },
    status: {
      ok: 'Available',
      limited: 'Rate limited',
      bad: 'Not pinnable',
      auth: 'Key issue',
      unknown: 'Unknown'
    },
    actions: {
      configure: 'Configure',
      probe: 'Probe',
      probeModel: 'Probe this model and refresh its upstream list',
      validate: 'Validate',
      validateModel: 'Pin every upstream once and mark real availability',
      test: 'Test',
      save: 'Save',
      saving: 'Saving…',
      cancel: 'Cancel',
      close: 'Close'
    },
    dialog: {
      title: 'Pin upstreams · {model}',
      mode: 'Mode',
      modeHintOff: 'No pinning. Exclusions and sorting still apply.',
      modeHintStrict: 'Only the first selected upstream is used.',
      modeHintPreferred:
        'The gateway tries the selected upstreams in order and falls back on failure.',
      upstreams: 'Pin order',
      upstreamsHint: 'Select the upstreams to use; the order below is the fallback order.',
      upstreamOrder: 'Selected order',
      upstreamMoveUp: 'Move up',
      upstreamMoveDown: 'Move down',
      exclude: 'Exclude',
      excludeHint:
        'Excluded upstreams are never used. When the known list is available they are converted into an allow-list for the gateway.',
      sort: 'Sort',
      sortDefault: 'Gateway default',
      sortCost: 'Lowest cost',
      sortTtft: 'Fastest first token',
      sortTps: 'Highest throughput',
      knownUpstreams: 'Known upstreams',
      knownUpstreamsEmpty: 'Probe this model to learn its upstream list.',
      needProbe: 'Probe this model first: the upstream list is unknown, so exclusions cannot be enforced.',
      testWithDraft: 'Test with these settings',
      testing: 'Testing…',
      testResultTitle: 'Test result',
      testActual: 'Actual upstream',
      testCandidates: 'Candidates',
      testMs: '{ms} ms',
      testContent: 'Reply',
      testFailed: 'Test failed',
      selected: '{count} selected'
    },
    messages: {
      saved: 'Pinning configuration saved',
      saveFailed: 'Failed to save pinning configuration',
      probed: 'Probed {count} model(s)',
      probeFailed: 'Probe failed',
      validated: 'Validated {count} upstream(s)',
      validateFailed: 'Validation failed',
      loadFailed: 'Failed to load Cline upstream state'
    }
  }
}
