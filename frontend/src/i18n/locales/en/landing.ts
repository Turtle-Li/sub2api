export default {
  batchImageGuide: {
    title: 'Batch Image Generation',
    description: 'Submit multiple prompts in one job and download the generated images when complete'
  },
  // Home Page
  home: {
    viewOnGithub: 'View on GitHub',
    viewDocs: 'View Documentation',
    docs: 'Docs',
    switchToLight: 'Switch to Light Mode',
    switchToDark: 'Switch to Dark Mode',
    dashboard: 'Dashboard',
    login: 'Login',
    getStarted: 'Get Started',
    goToDashboard: 'Go to Dashboard',
    // Default home page (TurtleRoute landing)
    landing: {
      hero: {
        title: 'One endpoint. Every model behind it.',
        lede: 'Unified keys, cross-provider routing, automatic failover. Your existing Anthropic / OpenAI SDK calls stay exactly as they are.',
        facts: {
          providers: '{count} upstreams',
          endpoint: '1 unified endpoint',
          protocols: '4 protocol surfaces'
        }
      },
      table: {
        label: 'Route table',
        note: 'A representative selection, not the full catalog. Pricing and the complete list live in the model plaza.',
        cols: {
          model: 'Model ID',
          provider: 'Upstream',
          capability: 'Capability',
          region: 'Region'
        },
        filterLabel: 'Filter',
        filters: {
          all: 'All',
          international: 'International',
          china: 'China',
          reasoning: 'Reasoning',
          multimodal: 'Multimodal'
        },
        tags: {
          coding: 'Coding',
          reasoning: 'Reasoning',
          multimodal: 'Multimodal',
          general: 'General'
        },
        regions: {
          international: 'Intl',
          china: 'CN'
        },
        count: '{shown} / {total} routes',
        viewAll: 'Model plaza'
      },
      request: {
        label: 'Request example',
        hint: 'Pick any row above and the example follows',
        baseHint: 'base URL taken from this site',
        copy: 'Copy',
        copied: 'Copied'
      },
      topology: {
        index: '01',
        title: 'When the primary route dies, the request takes another one',
        lede: 'A model can sit behind several upstream routes. Routes that fail health checks drop out of rotation; callers keep hitting the same endpoint with the same key.',
        client: 'Your app',
        gateway: 'Unified endpoint',
        states: {
          active: 'Serving',
          standby: 'Ready',
          degraded: 'Out of rotation'
        },
        fail: 'Simulate primary failure',
        restore: 'Restore primary',
        note: 'Illustrative topology, clickable. Live route health is on the Channel Status page.'
      },
      local: {
        index: '02',
        title: 'A local backend is just another route',
        lede: 'Register Ollama, vLLM, or anything else you host the same way you register a cloud provider. Same endpoint, same keys, same quota policy.',
        fileName: 'routes.yaml',
        points: {
          endpoint: { term: 'Same endpoint', desc: 'Clients still call /v1/messages and never branch on local vs. cloud' },
          scheduling: { term: 'Same rotation', desc: 'Local routes join the same rotation and failover policy' },
          policy: { term: 'Same accounting', desc: 'Keys, limits, and usage reporting stay on one set of rules' }
        }
      },
      next: {
        index: '03',
        title: 'Next',
        items: {
          console: { label: 'Console', desc: 'Mint keys, review usage' },
          status: { label: 'Channel status', desc: 'Sign in for live health per route' },
          plaza: { label: 'Model plaza', desc: 'Full catalog and current pricing' },
          docs: { label: 'API docs', desc: 'Integration paths and protocol differences' },
          github: { label: 'GitHub', desc: 'Source and release notes' },
          app: { label: 'Desktop app', desc: 'In development; downloads land here on release' }
        },
        pending: 'In development'
      }
    },
    footer: {
      allRightsReserved: 'All rights reserved.'
    }
  },

  // Key Usage Query Page
  keyUsage: {
    title: 'API Key Usage',
    subtitle: 'Enter your API Key to view real-time spending and usage status',
    placeholder: 'sk-ant-mirror-xxxxxxxxxxxx',
    query: 'Query',
    querying: 'Querying...',
    privacyNote: 'Your Key is processed locally in the browser and will not be stored',
    dateRange: 'Date Range:',
    dateRangeToday: 'Today',
    dateRange7d: '7 Days',
    dateRange30d: '30 Days',
    dateRange90d: '90 Days',
    dateRangeCustom: 'Custom',
    apply: 'Apply',
    used: 'Used',
    detailInfo: 'Detail Information',
    tokenStats: 'Token Statistics',
    dailyDetail: 'Daily Detail',
    modelStats: 'Model Usage Statistics',
    // Table headers
    date: 'Date',
    model: 'Model',
    requests: 'Requests',
    inputTokens: 'Input Tokens',
    outputTokens: 'Output Tokens',
    cacheCreationTokens: 'Cache Creation',
    cacheReadTokens: 'Cache Read',
    cacheWriteTokens: 'Cache Write',
    totalTokens: 'Total Tokens',
    cost: 'Cost',
    // Status
    quotaMode: 'Key Quota Mode',
    walletBalance: 'Wallet Balance',
    // Ring card titles
    totalQuota: 'Total Quota',
    limit5h: '5-Hour Limit',
    limitDaily: 'Daily Limit',
    limit7d: '7-Day Limit',
    limitWeekly: 'Weekly Limit',
    limitMonthly: 'Monthly Limit',
    // Detail rows
    remainingQuota: 'Remaining Quota',
    expiresAt: 'Expires At',
    todayExpires: '(expires today)',
    daysLeft: '({days} days)',
    usedQuota: 'Used Quota',
    resetNow: 'Resetting soon',
    subscriptionType: 'Subscription Type',
    subscriptionExpires: 'Subscription Expires',
    // Usage stat cells
    todayRequests: 'Today Requests',
    todayInputTokens: 'Today Input',
    todayOutputTokens: 'Today Output',
    todayTokens: 'Today Tokens',
    todayCacheCreation: 'Today Cache Creation',
    todayCacheRead: 'Today Cache Read',
    todayCost: 'Today Cost',
    rpmTpm: 'RPM / TPM',
    totalRequests: 'Total Requests',
    totalInputTokens: 'Total Input',
    totalOutputTokens: 'Total Output',
    totalTokensLabel: 'Total Tokens',
    totalCacheCreation: 'Total Cache Creation',
    totalCacheRead: 'Total Cache Read',
    totalCost: 'Total Cost',
    avgDuration: 'Avg Duration',
    // Messages
    enterApiKey: 'Please enter an API Key',
    querySuccess: 'Query successful',
    queryFailed: 'Query failed',
    queryFailedRetry: 'Query failed, please try again later',
    noDailyUsage: 'No daily usage data',
  },

  // Setup Wizard
  setup: {
    title: 'Sub2API Setup',
    description: 'Configure your Sub2API instance',
    database: {
      title: 'Database Configuration',
      description: 'Connect to your PostgreSQL database',
      host: 'Host',
      port: 'Port',
      username: 'Username',
      password: 'Password',
      databaseName: 'Database Name',
      sslMode: 'SSL Mode',
      passwordPlaceholder: 'Password',
      ssl: {
        disable: 'Disable',
        require: 'Require',
        verifyCa: 'Verify CA',
        verifyFull: 'Verify Full'
      }
    },
    redis: {
      title: 'Redis Configuration',
      description: 'Connect to your Redis server',
      host: 'Host',
      port: 'Port',
      username: 'Username (optional)',
      password: 'Password (optional)',
      database: 'Database',
      usernamePlaceholder: 'Leave empty for default user',
      passwordPlaceholder: 'Password',
      enableTls: 'Enable TLS',
      enableTlsHint: 'Use TLS when connecting to Redis (public CA certs)'
    },
    admin: {
      title: 'Admin Account',
      description: 'Create your administrator account',
      email: 'Email',
      password: 'Password',
      confirmPassword: 'Confirm Password',
      passwordPlaceholder: 'Min 8 characters',
      confirmPasswordPlaceholder: 'Confirm password',
      passwordMismatch: 'Passwords do not match'
    },
    ready: {
      title: 'Ready to Install',
      description: 'Review your configuration and complete setup',
      database: 'Database',
      redis: 'Redis',
      adminEmail: 'Admin Email'
    },
    status: {
      testing: 'Testing...',
      success: 'Connection Successful',
      testConnection: 'Test Connection',
      installing: 'Installing...',
      completeInstallation: 'Complete Installation',
      completed: 'Installation completed!',
      redirecting: 'Redirecting to login page...',
      restarting: 'Service is restarting, please wait...',
      timeout: 'Service restart is taking longer than expected. Please refresh the page manually.'
    }
  },

  // Common
}
