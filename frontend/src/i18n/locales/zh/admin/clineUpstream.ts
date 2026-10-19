export default {
  clineUpstream: {
    title: 'Cline 上游钉住',
    description: '枚举、校验并钉住 Cline Pass 订阅模型（cline-pass/*）背后的上游渠道。',
    pipelineHint:
      'Cline Pass 的订阅模型分两条路由管道：直连（OpenRouter，认顶层 provider.only/order/sort）与规划器（Vercel AI Gateway，认 providerOptions.gateway.*）。管道归属由 Cline 侧决定、可能随时间变化，钉住前请先探测。',
    manualProbePlaceholder: '手动探测任意模型 ID，如 cline-pass/…',
    manualProbeHint: '探测还没出现在清单里的模型 ID（例如刚发布的变体）。',
    recommendationLabel: '推荐流程：',
    recommendationStep1: '① 全部探测',
    recommendationStep2: '② 校验目标模型',
    recommendationStep3: '③ 严格钉住官方渠道',
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
    noPoolHint:
      '未探测到该模型的可选渠道池（网关忽略了占位探测），钉住可能无效。你仍可在下方强制指定渠道名，并用「测试」验证是否真的生效。',
    notPinnable: '不可钉',
    singleUpstreamHint: '该模型只有单一上游、没有可选渠道池，钉住不会生效。',
    columns: {
      model: '模型',
      source: '来源',
      pipeline: '管道',
      upstreams: '渠道',
      lastProvider: '最近实际渠道',
      mode: '钉住',
      actions: '操作'
    },
    pipelineHintDirect: '直连管道（OpenRouter）：钉住写进顶层 provider。',
    pipelineHintPlanner: '规划器管道（Vercel AI Gateway）：钉住写进 providerOptions.gateway。',
    pipelineHintUnknown: '尚未探测管道类型，请先探测该模型。',
    source: {
      subscription: '订阅',
      catalog: '目录',
      configured: '本地'
    },
    sourceHint: {
      subscription: '来自 Cline 推荐模型接口（Cline Pass 订阅清单）。',
      catalog: '来自该账号自己的模型目录接口。',
      configured: '仅本地存在：之前钉住或探测过，Cline 清单里已不再返回。'
    },
    pipeline: {
      direct: '直连',
      planner: '规划器',
      unknown: '未探测'
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
      unknown: '未知',
      notAdopted: '未采纳'
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
      modeHintStrict: '只使用勾选的第一个上游渠道；该渠道限流或不可钉时请求会直接失败，建议先「校验」确认可用。',
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
      manualUpstream: '强制指定渠道名',
      manualUpstreamPlaceholder: '例如 deepseek',
      manualUpstreamAdd: '添加',
      manualUpstreamHint:
        '探测清单可能不全：网关对未知渠道名可能静默忽略而不是报错。在这里直接填渠道名强制钉住，再用「测试」看网关是否真的采纳。',
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
    detail: {
      toggle: '展开探测详情',
      pipeline: '管道',
      canonicalSlug: '真实模型 slug',
      probedAt: '探测时间',
      validatedAt: '校验时间',
      never: '从未',
      harvestNote: '渠道清单来源',
      harvestOk: '渠道清单来自网关的占位渠道探测。',
      lastError: '最近错误',
      pinnable: '可否钉住',
      pinnableYes: '可以',
      pinnableNo: '不可（单一上游）',
      responseModel: '响应模型',
      routingRaw: '网关路由元数据',
      statuses: '各渠道状态'
    },
    adherence: {
      adopted: '钉住已生效——由被钉渠道服务',
      fallback: '钉住已生效——由回退渠道服务',
      notAdopted: '钉住未生效——由其它渠道服务',
      violated: '被排除的渠道仍在服务',
      unresolved: '无法判定——响应未声明实际渠道'
    },
    oneClick: {
      button: '一键钉住官方渠道',
      title: '一键把模型钉到厂商直供渠道',
      description:
        '只对「已探测出渠道池、且池里确实有该厂商直供渠道」的模型执行严格钉住；其余一律跳过并逐条列出，不会改动。',
      summary: '将钉住 {pinned} 个模型，跳过 {skipped} 个',
      columns: { model: '模型', channel: '将钉住的渠道' },
      skip: {
        notProbed: '跳过——尚未探测',
        notPinnable: '跳过——单一上游，无法钉住',
        noVendorChannel: '跳过——该模型的渠道里没有厂商直供'
      },
      nothingToDo: '暂无可钉模型：请先探测，或没有任何模型暴露厂商直供渠道。',
      apply: '应用',
      applied: '已钉住 {count} 个模型'
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
