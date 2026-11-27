import { defineComponent } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const {
  getStats,
  getAvailableModels,
  getTempUnschedulableStatus,
  listScheduledTests,
  listErrorPassthrough,
  listTLSFingerprintProfiles,
  getPool,
  getPoolMembers
} = vi.hoisted(() => ({
  getStats: vi.fn(),
  getAvailableModels: vi.fn(),
  getTempUnschedulableStatus: vi.fn(),
  listScheduledTests: vi.fn(),
  listErrorPassthrough: vi.fn(),
  listTLSFingerprintProfiles: vi.fn(),
  getPool: vi.fn(),
  getPoolMembers: vi.fn()
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key, locale: { value: 'zh' } }),
  createI18n: () => ({ global: { locale: { value: 'zh' }, t: (key: string) => key } })
}))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn(), showWarning: vi.fn() })
}))
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ isSimpleMode: false })
}))
vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getStats,
      getAvailableModels,
      getTempUnschedulableStatus
    },
    scheduledTests: {
      listByAccount: listScheduledTests
    },
    errorPassthrough: {
      list: listErrorPassthrough
    },
    tlsFingerprintProfiles: {
      list: listTLSFingerprintProfiles
    },
    accountPools: {
      get: getPool,
      listMembers: getPoolMembers
    }
  }
}))
vi.mock('vue-chartjs', () => ({
  Line: defineComponent({ template: '<div data-test="line-chart" />' }),
  Doughnut: defineComponent({ template: '<div data-test="doughnut-chart" />' })
}))
vi.mock('chart.js', () => ({
  Chart: { register: vi.fn() },
  ArcElement: {},
  CategoryScale: {},
  LinearScale: {},
  PointElement: {},
  LineElement: {},
  Title: {},
  Tooltip: {},
  Legend: {},
  Filler: {}
}))

import AccountStatsModal from '@/components/admin/account/AccountStatsModal.vue'
import AccountTestModal from '@/components/admin/account/AccountTestModal.vue'
import ScheduledTestsPanel from '@/components/admin/account/ScheduledTestsPanel.vue'
import ErrorPassthroughRulesModal from '@/components/admin/ErrorPassthroughRulesModal.vue'
import TLSFingerprintProfilesModal from '@/components/admin/TLSFingerprintProfilesModal.vue'
import ReAuthAccountModal from '@/components/admin/account/ReAuthAccountModal.vue'
import TempUnschedStatusModal from '@/components/account/TempUnschedStatusModal.vue'
import AccountPoolModal from '@/components/admin/account/AccountPoolModal.vue'

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: ['show', 'title', 'width', 'zIndex'],
  template: '<div data-test="base-dialog" :data-z-index="String(zIndex ?? \'\')"><slot /><slot name="footer" /></div>'
})

const commonStubs = {
  BaseDialog: BaseDialogStub,
  Icon: true,
  Select: true,
  ConfirmDialog: true,
  OAuthAuthorizationFlow: true,
  AccountUsageCell: true,
  AccountStatusIndicator: true,
  AccountCapacityCell: true,
  PlatformTypeBadge: true,
  Teleport: true
}

const sampleAccount = {
  id: 42,
  name: 'acc-42',
  platform: 'anthropic',
  type: 'oauth',
  status: 'active',
  schedulable: true,
  concurrency: 2,
  credentials: {}
} as any

describe('Lazy-mounted admin account modals immediate initialization (no TDZ)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getStats.mockResolvedValue({
      summary: {
        days: 30,
        actual_days_used: 1,
        active_days: 1,
        total_requests: 0,
        total_tokens: 0,
        total_cost: 0,
        total_user_cost: 0,
        total_standard_cost: 0,
        avg_daily_cost: 0,
        avg_daily_user_cost: 0,
        avg_daily_requests: 0,
        avg_daily_tokens: 0,
        avg_duration_ms: 0,
        today: { date: '2026-11-27', requests: 0, tokens: 0, cost: 0, user_cost: 0, standard_cost: 0 },
        highest_cost_day: null,
        highest_request_day: null
      },
      history: [],
      models: []
    })
    getAvailableModels.mockResolvedValue([{ id: 'claude-sonnet-4-5', display_name: 'Claude Sonnet 4.5' }])
    getTempUnschedulableStatus.mockResolvedValue({ active: false, state: null })
    listScheduledTests.mockResolvedValue([])
    listErrorPassthrough.mockResolvedValue([])
    listTLSFingerprintProfiles.mockResolvedValue([])
    getPool.mockResolvedValue({
      id: 7,
      name: 'pool-7',
      platform: 'openai',
      description: '',
      stats: { total: 0, active: 0, error: 0, rate_limited: 0, disabled: 0 }
    })
    getPoolMembers.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 0 })
  })

  it('mounts AccountStatsModal with show=true without TDZ and immediately loads stats', async () => {
    const wrapper = mount(AccountStatsModal, {
      props: { show: true, account: sampleAccount },
      global: { stubs: commonStubs }
    })
    await flushPromises()
    expect(getStats).toHaveBeenCalledWith(42, 30)
    wrapper.unmount()
  })

  it('mounts AccountTestModal with show=true without TDZ and immediately loads models', async () => {
    const wrapper = mount(AccountTestModal, {
      props: { show: true, account: sampleAccount },
      global: { stubs: commonStubs }
    })
    await flushPromises()
    expect(getAvailableModels).toHaveBeenCalledWith(42)
    wrapper.unmount()
  })

  it('mounts ScheduledTestsPanel with show=true without TDZ and immediately loads plans', async () => {
    const wrapper = mount(ScheduledTestsPanel, {
      props: { show: true, accountId: 42, accountName: 'acc-42' },
      global: { stubs: commonStubs }
    })
    await flushPromises()
    expect(listScheduledTests).toHaveBeenCalledWith(42)
    wrapper.unmount()
  })

  it('mounts ErrorPassthroughRulesModal with show=true without TDZ and immediately loads rules', async () => {
    const wrapper = mount(ErrorPassthroughRulesModal, {
      props: { show: true },
      global: { stubs: commonStubs }
    })
    await flushPromises()
    expect(listErrorPassthrough).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('mounts TLSFingerprintProfilesModal with show=true without TDZ and immediately loads profiles', async () => {
    const wrapper = mount(TLSFingerprintProfilesModal, {
      props: { show: true },
      global: { stubs: commonStubs }
    })
    await flushPromises()
    expect(listTLSFingerprintProfiles).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('mounts ReAuthAccountModal with both show=false and show=true without TDZ', async () => {
    const hiddenWrapper = mount(ReAuthAccountModal, {
      props: { show: false, account: sampleAccount },
      global: { stubs: commonStubs }
    })
    await flushPromises()
    hiddenWrapper.unmount()

    const openWrapper = mount(ReAuthAccountModal, {
      props: { show: true, account: sampleAccount },
      global: { stubs: commonStubs }
    })
    await flushPromises()
    openWrapper.unmount()
  })

  it('mounts TempUnschedStatusModal with show=true without TDZ and immediately loads status', async () => {
    const wrapper = mount(TempUnschedStatusModal, {
      props: { show: true, account: sampleAccount },
      global: { stubs: commonStubs }
    })
    await flushPromises()
    expect(getTempUnschedulableStatus).toHaveBeenCalledWith(42)
    wrapper.unmount()
  })

  it('renders AccountPoolModal with z-index 45 so lazy child dialogs (default z-index 50) stack above it', async () => {
    const wrapper = mount(AccountPoolModal, {
      props: { show: true, poolId: 7, groups: [], proxies: [] },
      global: { stubs: commonStubs }
    })
    await flushPromises()
    const dialog = wrapper.get('[data-test="base-dialog"]')
    expect(dialog.attributes('data-z-index')).toBe('45')
    wrapper.unmount()
  })
})
