export default {
  clineUpstream: {
    title: 'Cline 上游钉住',
    description: '枚举、校验并钉住 Cline Pass 订阅模型（cline-pass/*）背后的上游渠道。',
    pipelineHint:
      'Cline Pass 的订阅模型分两条路由管道：直连（OpenRouter，认顶层 provider.only/order/sort）与规划器（Vercel AI Gateway，认 providerOptions.gateway.*）。管道归属由 Cline 侧决定、可能随时间变化，钉住前请先探测。',
    probeCostHint: '探测、校验与测试都会用该账号发一条极小的真实请求（每次约千分之几美分级）。',
    refresh: '刷新',
    probeAll: '全部探测',
    probing: '探测中…',
    accountLabel: 'Cline 账号',
    selectAccount: '请选择 Cline 账号',
    loadingAccounts: '正在加载 Cline 账号…',
    noAccounts: '暂无 Cline 账号',
    noAccountsHint: '请先创建 Cline API Key 账号，再回来钉住它的上游渠道。',
    unsupported: '该账号无法探测：上游枚举要求 API Key 账号且指向官方 api.cline.bot 主机。',
    unsupportedHint: '钉住配置仍会保存并对该账号生效，但渠道清单需要手工填写。',
    modelsError: '模型清单刷新失败：{error}',
    emptyModels: '暂无可配置模型',
    emptyModelsHint: '请先探测该账号，加载 Cline Pass 订阅模型清单。',
    configuredCount: '已钉住 {count} 个',
    lastFetched: '更新于 {time}',
    notProbed: '未探测',
    noUpstreams: '尚未获得渠道清单',
    columns: {
      model: '模型',
      source: '来源',
      pipeline: '管道',
      upstreams: '渠道',
      lastProvider: '最近实际渠道',
      mode: '钉住',
      actions: '操作'
    },
    source: {
      subscription: '订阅清单',
      catalog: '账号目录',
      configured: '仅本地配置'
    },
    pipeline: {
      direct: '直连（OpenRouter）',
      planner: '规划器（Vercel）',
      unknown: '未知'
    },
    mode: {
      off: '不钉住',
      strict: '严格钉住',
      preferred: '优先+回退'
    },
    status: {
      ok: '可用',
      limited: '限流',
      bad: '不可钉',
      auth: '密钥异常',
      unknown: '未知'
    },
    actions: {
      configure: '配置',
      probe: '探测',
      probeModel: '探测该模型并刷新渠道清单',
      validate: '校验',
      validateModel: '把每个渠道各钉一次，标记真实可用性',
      test: '测试',
      save: '保存',
      saving: '保存中…',
      cancel: '取消',
      close: '关闭'
    },
    dialog: {
      title: '钉住上游 · {model}',
      mode: '模式',
      modeHintOff: '不注入上游偏好；排除与排序仍然生效。',
      modeHintStrict: '只使用勾选的第一个上游渠道。',
      modeHintPreferred: '网关按勾选顺序尝试，失败自动回退到下一个。',
      upstreams: '钉住顺序',
      upstreamsHint: '勾选要使用的上游渠道，下方顺序即回退顺序。',
      upstreamOrder: '已选顺序',
      upstreamMoveUp: '上移',
      upstreamMoveDown: '下移',
      exclude: '排除',
      excludeHint: '勾选「排除」的渠道永不被使用；已知渠道清单存在时会换算成网关的 only 白名单。',
      sort: '排序',
      sortDefault: '网关默认',
      sortCost: '最低成本',
      sortTtft: '最快首字',
      sortTps: '最高吞吐',
      knownUpstreams: '已知渠道',
      knownUpstreamsEmpty: '请先探测该模型以获得渠道清单。',
      needProbe: '该模型的渠道清单未知，请先探测；否则排除项无法生效。',
      testWithDraft: '用当前设置测试',
      testing: '测试中…',
      testResultTitle: '测试结果',
      testActual: '实际渠道',
      testCandidates: '候选',
      testMs: '{ms} 毫秒',
      testContent: '回复',
      testFailed: '测试失败',
      selected: '已选 {count} 个'
    },
    messages: {
      saved: '钉住配置已保存',
      saveFailed: '钉住配置保存失败',
      probed: '已探测 {count} 个模型',
      probeFailed: '探测失败',
      validated: '已校验 {count} 个渠道',
      validateFailed: '校验失败',
      loadFailed: '加载 Cline 上游状态失败'
    }
  }
}
