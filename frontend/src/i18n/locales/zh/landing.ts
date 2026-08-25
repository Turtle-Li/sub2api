export default {
  batchImageGuide: {
    title: '图片批量生成',
    description: '一次提交多条提示词，任务完成后可统一下载图片结果'
  },
  // Home Page
  home: {
    viewOnGithub: '在 GitHub 上查看',
    viewDocs: '查看文档',
    docs: '文档',
    switchToLight: '切换到浅色模式',
    switchToDark: '切换到深色模式',
    dashboard: '控制台',
    login: '登录',
    getStarted: '立即开始',
    goToDashboard: '进入控制台',
    // 默认首页（TurtleRoute 落地页）
    landing: {
      hero: {
        title: '一个端点，通向所有模型',
        lede: '统一密钥、跨服务商路由、失败自动绕行。已有的 Anthropic / OpenAI SDK 调用不用改一行。',
        facts: {
          providers: '{count} 家上游',
          endpoint: '1 个统一端点',
          protocols: '4 套协议面'
        }
      },
      table: {
        label: '路由表',
        note: '代表性模型，非完整目录。价格与全量列表以模型广场为准。',
        cols: {
          model: '模型 ID',
          provider: '上游',
          capability: '能力',
          region: '区域'
        },
        filterLabel: '筛选',
        filters: {
          all: '全部',
          international: '国际',
          china: '国内',
          reasoning: '推理',
          multimodal: '多模态'
        },
        tags: {
          coding: '代码',
          reasoning: '推理',
          multimodal: '多模态',
          general: '通用'
        },
        regions: {
          international: '国际',
          china: '国内'
        },
        count: '{shown} / {total} 条路由',
        viewAll: '模型广场'
      },
      request: {
        label: '请求示例',
        hint: '点上方任意一行，示例同步更新',
        baseHint: 'base URL 取自当前站点',
        copy: '复制',
        copied: '已复制'
      },
      topology: {
        index: '01',
        title: '主路由挂了，请求自己换条路',
        lede: '一个模型可以挂多条上游路由。健康检查不通过的路由会被移出轮询，调用方拿到的仍然是同一个端点、同一个密钥。',
        client: '你的应用',
        gateway: '统一端点',
        states: {
          active: '承载中',
          standby: '待接管',
          degraded: '已移出轮询'
        },
        fail: '模拟主路由故障',
        restore: '恢复主路由',
        note: '示意拓扑，可点击。线上真实健康状况请看渠道状态页。'
      },
      local: {
        index: '02',
        title: '本地推理后端，挂成一条普通路由',
        lede: 'Ollama、vLLM 这类本地后端和云端服务商一样注册为路由，共用同一个端点、同一套密钥与配额策略。',
        fileName: 'routes.yaml',
        points: {
          endpoint: { term: '同一端点', desc: '客户端仍然调用 /v1/messages，不区分本地与云端' },
          scheduling: { term: '统一调度', desc: '本地路由参与同一份轮询与故障绕行策略' },
          policy: { term: '一致口径', desc: '密钥、限额与用量统计规则保持不变' }
        }
      },
      next: {
        index: '03',
        title: '接下来',
        items: {
          console: { label: '控制台', desc: '创建密钥、查看用量' },
          status: { label: '渠道状态', desc: '登录后查看各路由的实时健康状况' },
          plaza: { label: '模型广场', desc: '完整模型目录与实时价格' },
          docs: { label: 'API 文档', desc: '接入方式与协议差异' },
          github: { label: 'GitHub', desc: '开源仓库与更新记录' },
          app: { label: '桌面 App', desc: '开发中，发布后在此提供下载' }
        },
        pending: '开发中'
      }
    },
    footer: {
      allRightsReserved: '保留所有权利。'
    }
  },

  // Key Usage Query Page
  keyUsage: {
    title: 'API Key 用量查询',
    subtitle: '输入您的 API Key 以查看实时消费金额与使用状态',
    placeholder: 'sk-ant-mirror-xxxxxxxxxxxx',
    query: '查询',
    querying: '查询中...',
    privacyNote: '您的 Key 仅在浏览器本地处理，不会被存储',
    dateRange: '统计范围:',
    dateRangeToday: '今日',
    dateRange7d: '7 天',
    dateRange30d: '30 天',
    dateRange90d: '90 天',
    dateRangeCustom: '自定义',
    apply: '应用',
    used: '已使用',
    detailInfo: '详细信息',
    tokenStats: 'Token 统计',
    dailyDetail: '按日明细',
    modelStats: '模型用量统计',
    // Table headers
    date: '日期',
    model: '模型',
    requests: '请求数',
    inputTokens: '输入 Tokens',
    outputTokens: '输出 Tokens',
    cacheCreationTokens: '缓存创建',
    cacheReadTokens: '缓存读取',
    cacheWriteTokens: '缓存写入',
    totalTokens: '总 Tokens',
    cost: '费用',
    // Status
    quotaMode: 'Key 限额模式',
    walletBalance: '钱包余额',
    // Ring card titles
    totalQuota: '总额度',
    limit5h: '5 小时限额',
    limitDaily: '日限额',
    limit7d: '7 天限额',
    limitWeekly: '周限额',
    limitMonthly: '月限额',
    // Detail rows
    remainingQuota: '剩余额度',
    expiresAt: '过期时间',
    todayExpires: '(今日到期)',
    daysLeft: '({days} 天)',
    usedQuota: '已用额度',
    resetNow: '即将重置',
    subscriptionType: '订阅类型',
    subscriptionExpires: '订阅到期',
    // Usage stat cells
    todayRequests: '今日请求',
    todayInputTokens: '今日输入',
    todayOutputTokens: '今日输出',
    todayTokens: '今日 Tokens',
    todayCacheCreation: '今日缓存创建',
    todayCacheRead: '今日缓存读取',
    todayCost: '今日费用',
    rpmTpm: 'RPM / TPM',
    totalRequests: '累计请求',
    totalInputTokens: '累计输入',
    totalOutputTokens: '累计输出',
    totalTokensLabel: '累计 Tokens',
    totalCacheCreation: '累计缓存创建',
    totalCacheRead: '累计缓存读取',
    totalCost: '累计费用',
    avgDuration: '平均耗时',
    // Messages
    enterApiKey: '请输入 API Key',
    querySuccess: '查询成功',
    queryFailed: '查询失败',
    queryFailedRetry: '查询失败，请稍后重试',
    noDailyUsage: '暂无按日用量数据',
  },

  // Setup Wizard
  setup: {
    title: 'Sub2API 安装向导',
    description: '配置您的 Sub2API 实例',
    database: {
      title: '数据库配置',
      description: '连接到您的 PostgreSQL 数据库',
      host: '主机',
      port: '端口',
      username: '用户名',
      password: '密码',
      databaseName: '数据库名称',
      sslMode: 'SSL 模式',
      passwordPlaceholder: '密码',
      ssl: {
        disable: '禁用',
        require: '要求',
        verifyCa: '验证 CA',
        verifyFull: '完全验证'
      }
    },
    redis: {
      title: 'Redis 配置',
      description: '连接到您的 Redis 服务器',
      host: '主机',
      port: '端口',
      username: '用户名（可选）',
      password: '密码（可选）',
      database: '数据库',
      usernamePlaceholder: '默认用户留空',
      passwordPlaceholder: '密码',
      enableTls: '启用 TLS',
      enableTlsHint: '连接 Redis 时使用 TLS（公共 CA 证书）'
    },
    admin: {
      title: '管理员账户',
      description: '创建您的管理员账户',
      email: '邮箱',
      password: '密码',
      confirmPassword: '确认密码',
      passwordPlaceholder: '至少 8 个字符',
      confirmPasswordPlaceholder: '确认密码',
      passwordMismatch: '密码不匹配'
    },
    ready: {
      title: '准备安装',
      description: '检查您的配置并完成安装',
      database: '数据库',
      redis: 'Redis',
      adminEmail: '管理员邮箱'
    },
    status: {
      testing: '测试中...',
      success: '连接成功',
      testConnection: '测试连接',
      installing: '安装中...',
      completeInstallation: '完成安装',
      completed: '安装完成！',
      redirecting: '正在跳转到登录页面...',
      restarting: '服务正在重启，请稍候...',
      timeout: '服务重启时间超出预期，请手动刷新页面。'
    }
  },

  // Common
}
