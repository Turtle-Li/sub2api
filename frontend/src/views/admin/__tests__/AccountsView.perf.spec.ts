import { defineComponent } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const {
  listAccounts,
  getAllProxies,
  getAllGroups,
  listAccountPools,
  getBatchTodayStats,
  getBatchUsage,
  getUpstreamBillingProbeSettings
} = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  getAllProxies: vi.fn(),
  getAllGroups: vi.fn(),
  listAccountPools: vi.fn(),
  getBatchTodayStats: vi.fn(),
  getBatchUsage: vi.fn(),
  getUpstreamBillingProbeSettings: vi.fn()
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
  createI18n: () => ({ global: { locale: { value: 'zh' }, t: (key: string) => key } })
}))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn() }) }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn(), showWarning: vi.fn() }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isSimpleMode: false }) }))
vi.mock('@/composables/useStepUp', () => ({
  useStepUp: () => ({}),
  isStepUpBlocked: () => false,
  isStepUpCancelled: () => false,
  stepUpBlockReason: () => ''
}))
vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      list: listAccounts,
      getBatchTodayStats,
      getBatchUsage,
      getUpstreamBillingProbeSettings
    },
    proxies: { getAll: getAllProxies },
    groups: { getAll: getAllGroups },
    accountPools: { list: listAccountPools }
  }
}))

import AccountsView from '../AccountsView.vue'

const DataTableStub = defineComponent({
  props: ['columns', 'data', 'loading'],
  template: `
    <div data-test="data-table">
      <span v-for="col in columns" :key="col.key" data-test="column-key">{{ col.key }}</span>
      <div v-for="row in data" :key="row.id">
        <slot name="cell-usage" :row="row" :value="null" />
      </div>
    </div>
  `
})

const UsageCellStub = defineComponent({
  name: 'AccountUsageCell',
  props: ['account', 'requestBatchedUsage', 'manualRefreshToken', 'batchedUsageError'],
  mounted() {
    this.requestBatchedUsage?.(this.account)
  },
  template: '<div data-test="usage-cell" :data-error="batchedUsageError || \'\'" />'
})

const mountView = () =>
  mount(AccountsView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: { template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>' },
        DataTable: DataTableStub,
        AccountTableActions: {
          template: '<div><button data-test="manual-refresh" @click="$emit(\'refresh\')" /><button data-test="create-btn" @click="$emit(\'create\')" /><slot name="after" /></div>'
        },
        AccountTableFilters: true,
        AccountBulkActionsBar: true,
        AccountPoolStrip: true,
        AccountUsageCell: UsageCellStub,
        AccountActionMenu: true,
        CreateAccountModal: { props: ['show'], template: '<div data-test="create-modal" :data-show="String(show)" />' },
        Pagination: true,
        ConfirmDialog: true,
        TotpStepUpDialog: true,
        Icon: true,
        HelpTooltip: true,
        Teleport: true
      }
    }
  })

describe('AccountsView performance & compact view safeguards', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.clearAllMocks()
    vi.spyOn(window, 'matchMedia').mockImplementation(() => ({
      matches: true,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn()
    }) as unknown as MediaQueryList)

    listAccounts.mockResolvedValue({
      items: [
        { id: 1, name: 'oa-1', platform: 'openai', type: 'oauth', status: 'active', schedulable: true, concurrency: 2, group_ids: [10] },
        { id: 2, name: 'ag-2', platform: 'antigravity', type: 'oauth', status: 'active', schedulable: true, concurrency: 2, group_ids: [10] }
      ],
      total: 2,
      page: 1,
      page_size: 20,
      pages: 1
    })
    getAllGroups.mockResolvedValue([{ id: 10, name: 'default', platform: 'openai' }])
    getAllProxies.mockResolvedValue([{ id: 1, name: 'p1' }])
    listAccountPools.mockResolvedValue([])
    getBatchTodayStats.mockResolvedValue({ stats: {} })
    getBatchUsage.mockResolvedValue({ usage: {}, errors: {} })
    getUpstreamBillingProbeSettings.mockResolvedValue({ enabled: true })
  })

  it('defers proxies/all until create modal opens, skips group counts, and skips today-stats for oauth-only pages', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(getAllGroups).toHaveBeenCalledWith(undefined, { includeCounts: false })
    expect(getAllProxies).not.toHaveBeenCalled()
    expect(getBatchTodayStats).not.toHaveBeenCalled()

    await wrapper.get('[data-test="create-btn"]').trigger('click')
    await flushPromises()
    expect(getAllProxies).toHaveBeenCalledTimes(1)
    expect(getAllGroups).toHaveBeenCalledWith()
    expect(wrapper.get('[data-test="create-modal"]').attributes('data-show')).toBe('true')
    wrapper.unmount()
  })

  it('includes Gemini Vertex and third-party Gemini API keys in today-stats while excluding official Gemini, OAuth, and CN multi-protocol accounts', async () => {
    vi.useFakeTimers()
    listAccounts.mockResolvedValue({
      items: [
        { id: 1, name: 'oa-1', platform: 'openai', type: 'oauth', status: 'active', schedulable: true, concurrency: 2, group_ids: [10] },
        { id: 2, name: 'gem-official', platform: 'gemini', type: 'apikey', status: 'active', schedulable: true, concurrency: 2, credentials: { base_url: 'https://generativelanguage.googleapis.com' } },
        { id: 3, name: 'gem-vertex', platform: 'gemini', type: 'service_account', status: 'active', schedulable: true, concurrency: 2, credentials: {} },
        { id: 4, name: 'gem-relay', platform: 'gemini', type: 'apikey', status: 'active', schedulable: true, concurrency: 2, credentials: { base_url: 'https://relay.example.com/v1' } },
        { id: 5, name: 'claude-key', platform: 'anthropic', type: 'apikey', status: 'active', schedulable: true, concurrency: 2, credentials: {} },
        { id: 6, name: 'ds-cn', platform: 'deepseek', type: 'apikey', status: 'active', schedulable: true, concurrency: 2, credentials: {} }
      ],
      total: 6,
      page: 1,
      page_size: 20,
      pages: 1
    })

    const wrapper = mountView()
    await flushPromises()
    await vi.advanceTimersByTimeAsync(1)
    await flushPromises()

    expect(getBatchTodayStats).toHaveBeenCalledWith([3, 4, 5])
    expect(getBatchUsage).toHaveBeenCalledWith([1, 2], false, { snapshotOnly: true })

    vi.useRealTimers()
    wrapper.unmount()
  })

  it('re-reads SWR stale & deferred snapshots across slow refreshes until fresh and stops after completion', async () => {
    vi.useFakeTimers()
    getBatchUsage
      .mockResolvedValueOnce({
        usage: {
          '2': {
            source: 'snapshot',
            snapshot_refresh_state: 'deferred',
            updated_at: new Date().toISOString(),
            five_hour: null,
            seven_day: null,
            seven_day_sonnet: null,
            antigravity_quota: { 'claude-opus-4-6': { utilization: 10, reset_time: '' } }
          }
        },
        errors: {}
      })
      .mockResolvedValueOnce({
        usage: {
          '2': {
            source: 'snapshot',
            snapshot_refresh_state: 'refreshing',
            updated_at: new Date().toISOString(),
            five_hour: null,
            seven_day: null,
            seven_day_sonnet: null,
            antigravity_quota: { 'claude-opus-4-6': { utilization: 10, reset_time: '' } }
          }
        },
        errors: {}
      })
      .mockResolvedValueOnce({
        usage: {
          '2': {
            source: 'active',
            updated_at: new Date().toISOString(),
            five_hour: null,
            seven_day: null,
            seven_day_sonnet: null,
            antigravity_quota: { 'claude-opus-4-6': { utilization: 42, reset_time: '' } }
          }
        },
        errors: {}
      })

    const wrapper = mountView()
    await flushPromises()
    await vi.advanceTimersByTimeAsync(1)
    await flushPromises()
    expect(getBatchUsage).toHaveBeenCalledTimes(1)

    // 2500ms: deferred -> refreshing (still re-reads)
    await vi.advanceTimersByTimeAsync(2505)
    await flushPromises()
    expect(getBatchUsage).toHaveBeenCalledTimes(2)
    expect(getBatchUsage).toHaveBeenNthCalledWith(2, [2], false, { snapshotOnly: true })

    // +5000ms: slow upstream finishes -> active fresh data
    await vi.advanceTimersByTimeAsync(5005)
    await flushPromises()
    expect(getBatchUsage).toHaveBeenCalledTimes(3)
    expect(getBatchUsage).toHaveBeenNthCalledWith(3, [2], false, { snapshotOnly: true })

    // Once fresh (no snapshot_refresh_state), no further re-reads fire.
    await vi.advanceTimersByTimeAsync(30000)
    await flushPromises()
    expect(getBatchUsage).toHaveBeenCalledTimes(3)

    vi.useRealTimers()
    wrapper.unmount()
  })

  it('gives refreshing a fresh retry budget when a task is finally admitted on the last deferred check', async () => {
    vi.useFakeTimers()
    const staleDeferred = () => ({
      usage: {
        '2': {
          source: 'snapshot',
          snapshot_refresh_state: 'deferred',
          updated_at: new Date().toISOString(),
          five_hour: null,
          seven_day: null,
          seven_day_sonnet: null,
          antigravity_quota: { 'claude-opus-4-6': { utilization: 10, reset_time: '' } }
        }
      },
      errors: {}
    })
    const staleRefreshing = () => ({
      usage: {
        '2': {
          source: 'snapshot',
          snapshot_refresh_state: 'refreshing',
          updated_at: new Date().toISOString(),
          five_hour: null,
          seven_day: null,
          seven_day_sonnet: null,
          antigravity_quota: { 'claude-opus-4-6': { utilization: 10, reset_time: '' } }
        }
      },
      errors: {}
    })
    const freshActive = () => ({
      usage: {
        '2': {
          source: 'active',
          updated_at: new Date().toISOString(),
          five_hour: null,
          seven_day: null,
          seven_day_sonnet: null,
          antigravity_quota: { 'claude-opus-4-6': { utilization: 77, reset_time: '' } }
        }
      },
      errors: {}
    })

    // 0s: deferred (1), 2.5s: deferred (2), 7.5s: deferred (3), 17.5s: deferred (4),
    // 32.5s (last deferred check!): admitted -> refreshing (1),
    // 35s: refreshing (2), 40s: refreshing (3), 50s: fresh active!
    getBatchUsage
      .mockResolvedValueOnce(staleDeferred())
      .mockResolvedValueOnce(staleDeferred())
      .mockResolvedValueOnce(staleDeferred())
      .mockResolvedValueOnce(staleDeferred())
      .mockResolvedValueOnce(staleRefreshing())
      .mockResolvedValueOnce(staleRefreshing())
      .mockResolvedValueOnce(staleRefreshing())
      .mockResolvedValueOnce(freshActive())

    const wrapper = mountView()
    await flushPromises()
    await vi.advanceTimersByTimeAsync(1)
    await flushPromises()
    expect(getBatchUsage).toHaveBeenCalledTimes(1)

    // Advance through all 4 deferred retries (2.5 + 5 + 10 + 15 = 32.5s).
    for (const delay of [2505, 5005, 10005, 15005]) {
      await vi.advanceTimersByTimeAsync(delay)
      await flushPromises()
    }
    expect(getBatchUsage).toHaveBeenCalledTimes(5)

    // Admitted on the last deferred retry; refreshing gets its own retry budget (2.5 + 5 + 10 = 17.5s more).
    for (const delay of [2505, 5005, 10005]) {
      await vi.advanceTimersByTimeAsync(delay)
      await flushPromises()
    }
    expect(getBatchUsage).toHaveBeenCalledTimes(8)

    // Once active fresh quota is observed, no further re-reads fire.
    await vi.advanceTimersByTimeAsync(30000)
    await flushPromises()
    expect(getBatchUsage).toHaveBeenCalledTimes(8)

    vi.useRealTimers()
    wrapper.unmount()
  })

  it('replaces superseded retry timer without burning the final retry budget when a new passive request arrives', async () => {
    vi.useFakeTimers()
    const refreshingResp = () => ({
      usage: {
        '2': {
          source: 'snapshot',
          snapshot_refresh_state: 'refreshing',
          updated_at: new Date().toISOString(),
          five_hour: null,
          seven_day: null,
          seven_day_sonnet: null
        }
      },
      errors: {}
    })
    const activeResp = () => ({
      usage: {
        '2': {
          source: 'active',
          updated_at: new Date().toISOString(),
          five_hour: null,
          seven_day: null,
          seven_day_sonnet: null,
          antigravity_quota: { 'claude-opus-4-6': { utilization: 61, reset_time: '' } }
        }
      },
      errors: {}
    })

    // Global refreshing schedule: 6 initial ramp + 52 steady = 58 max retries.
    // Burn the first 57 retries so only the final (58th, 30s) retry remains pending.
    const first57Delays = [
      2500, 5000, 10000, 15000, 20000, 25000,
      ...Array.from({ length: 51 }, () => 30000)
    ]
    let freshReady = false
    getBatchUsage.mockImplementation(async () => (freshReady ? activeResp() : refreshingResp()))

    const wrapper = mountView()
    await flushPromises()
    await vi.advanceTimersByTimeAsync(1)
    await flushPromises()

    for (const delay of first57Delays) {
      await vi.advanceTimersByTimeAsync(delay + 5)
      await flushPromises()
    }
    const callsBeforeSupersede = getBatchUsage.mock.calls.length
    expect(callsBeforeSupersede).toBe(58)

    // 5s into the final 30s timer, a new passive request advances the token and returns refreshing.
    // Because the superseded timer never fired, the final retry budget must remain intact!
    await vi.advanceTimersByTimeAsync(5000)
    const cells = wrapper.findAllComponents(UsageCellStub)
    cells[1].props('requestBatchedUsage')(cells[1].props('account'))
    await vi.advanceTimersByTimeAsync(1)
    await flushPromises()
    expect(getBatchUsage).toHaveBeenCalledTimes(callsBeforeSupersede + 1)

    // Fresh data becomes ready before the replacement final timer (30s) fires.
    freshReady = true
    await vi.advanceTimersByTimeAsync(30005)
    await flushPromises()
    expect(getBatchUsage).toHaveBeenCalledTimes(callsBeforeSupersede + 2)

    // After fresh quota is observed, no more timers run.
    await vi.advanceTimersByTimeAsync(60000)
    await flushPromises()
    expect(getBatchUsage).toHaveBeenCalledTimes(callsBeforeSupersede + 2)

    vi.useRealTimers()
    wrapper.unmount()
  })

  it('observes all 20 accounts even behind 80 queued items + 2 running slots (1218s..1479s) and surfaces explicit retry hint when deferred budget exhausts', async () => {
    vi.useFakeTimers()

    // Scenario A: 80 pre-queued items from other pages + 2 running slots => 41 batches * 29s = 1189s head-of-line delay.
    // Current page of 20 accounts finishes between 1218s and 1479s; all 20 must be observed!
    const twentyAccounts = Array.from({ length: 20 }, (_, i) => ({
      id: i + 1,
      name: `ag-${i + 1}`,
      platform: 'antigravity',
      type: 'oauth',
      status: 'active',
      schedulable: true,
      concurrency: 2,
      group_ids: [10]
    }))
    listAccounts.mockResolvedValue({
      items: twentyAccounts,
      total: 20,
      page: 1,
      page_size: 20,
      pages: 1
    })

    const startTime = Date.now()
    const preQueueDelayMs = 41 * 29_000 // 80 queued + 2 running at 29s/batch = 1189s
    const readyAtMs: Record<number, number> = {}
    for (let i = 0; i < 20; i++) {
      readyAtMs[i + 1] = preQueueDelayMs + (Math.floor(i / 2) + 1) * 29_000
    }
    const observedFresh = new Set<number>()
    getBatchUsage.mockImplementation(async (ids: number[]) => {
      const elapsed = Date.now() - startTime
      const usage: Record<string, any> = {}
      for (const id of ids) {
        if (elapsed >= readyAtMs[id]) {
          observedFresh.add(id)
          usage[String(id)] = {
            source: 'active',
            updated_at: new Date().toISOString(),
            five_hour: null,
            seven_day: null,
            seven_day_sonnet: null,
            antigravity_quota: { 'claude-opus-4-6': { utilization: id, reset_time: '' } }
          }
        } else {
          usage[String(id)] = {
            source: 'snapshot',
            snapshot_refresh_state: 'refreshing',
            updated_at: new Date().toISOString(),
            five_hour: null,
            seven_day: null,
            seven_day_sonnet: null
          }
        }
      }
      return { usage, errors: {} }
    })

    const wrapper = mountView()
    await flushPromises()
    await vi.advanceTimersByTimeAsync(1)
    await flushPromises()

    // Advance virtual clock across the 1520s window.
    for (let step = 0; step < 51; step++) {
      await vi.advanceTimersByTimeAsync(30_000)
      await flushPromises()
    }
    expect(Array.from(observedFresh).sort((a, b) => a - b)).toEqual(
      Array.from({ length: 20 }, (_, i) => i + 1)
    )
    wrapper.unmount()

    // Scenario B: Queue stays permanently saturated ("deferred") until deferred budget exhausts ->
    // UI surfaces explicit retry hint error instead of silently freezing.
    vi.clearAllMocks()
    listAccounts.mockResolvedValue({
      items: [{ id: 99, name: 'ag-overflow', platform: 'antigravity', type: 'oauth', status: 'active', schedulable: true, concurrency: 2, group_ids: [10] }],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1
    })
    getBatchUsage.mockResolvedValue({
      usage: {
        '99': {
          source: 'snapshot',
          snapshot_refresh_state: 'deferred',
          updated_at: new Date().toISOString(),
          five_hour: null,
          seven_day: null,
          seven_day_sonnet: null
        }
      },
      errors: {}
    })

    const overflowWrapper = mountView()
    await flushPromises()
    await vi.advanceTimersByTimeAsync(1)
    await flushPromises()

    for (const delay of [2500, 5000, 10000, 15000, 30000, 30000, 30000, 30000, 30000, 30000]) {
      await vi.advanceTimersByTimeAsync(delay + 5)
      await flushPromises()
    }
    const cell = overflowWrapper.get('[data-test="usage-cell"]')
    expect(cell.attributes('data-error')).toBe('admin.accounts.usageSnapshotRetryHint')
    overflowWrapper.unmount()

    vi.useRealTimers()
  })

  it('does not schedule follow-up usage requests when an in-flight snapshot resolves after unmount', async () => {
    vi.useFakeTimers()
    let resolveInFlight!: (val: any) => void
    getBatchUsage.mockImplementationOnce(
      () => new Promise((resolve) => { resolveInFlight = resolve })
    )

    const wrapper = mountView()
    await flushPromises()
    await vi.advanceTimersByTimeAsync(1)
    await flushPromises()
    expect(getBatchUsage).toHaveBeenCalledTimes(1)

    // Unmount while first request is still in-flight, then resolve it with refreshing snapshot.
    wrapper.unmount()
    resolveInFlight({
      usage: {
        '2': {
          source: 'snapshot',
          snapshot_refresh_state: 'refreshing',
          updated_at: new Date().toISOString(),
          five_hour: null,
          seven_day: null,
          seven_day_sonnet: null
        }
      },
      errors: {}
    })
    await flushPromises()
    await vi.advanceTimersByTimeAsync(20000)
    await flushPromises()

    expect(getBatchUsage).toHaveBeenCalledTimes(1)
    vi.useRealTimers()
  })

  it('isolates snapshot vs forced usage batch queues and keeps toolbar refresh passive', async () => {
    vi.useFakeTimers()
    const wrapper = mountView()
    await flushPromises()
    await vi.advanceTimersByTimeAsync(1)
    await flushPromises()

    expect(getBatchUsage).toHaveBeenCalledTimes(1)
    expect(getBatchUsage).toHaveBeenCalledWith([1, 2], false, { snapshotOnly: true })

    // Queue one forced account alongside one passive account: force must NOT leak to the passive account.
    const cells = wrapper.findAllComponents(UsageCellStub)
    cells[0].props('requestBatchedUsage')(cells[0].props('account'), { force: true })
    cells[1].props('requestBatchedUsage')(cells[1].props('account'))
    await vi.advanceTimersByTimeAsync(1)
    await flushPromises()

    expect(getBatchUsage).toHaveBeenCalledWith([1], true, { snapshotOnly: false })
    expect(getBatchUsage).toHaveBeenCalledWith([2], false, { snapshotOnly: true })

    // Manual toolbar refresh refreshes the list without incrementing manualRefreshToken (no mass force-probe).
    const beforeToken = cells[0].props('manualRefreshToken')
    await wrapper.get('[data-test="manual-refresh"]').trigger('click')
    await flushPromises()
    expect(cells[0].props('manualRefreshToken')).toBe(beforeToken)

    vi.useRealTimers()
    wrapper.unmount()
  })

  it('toggles compact monitoring view (name/capacity/status/usage/actions) without overwriting custom hidden columns', async () => {
    const wrapper = mountView()
    await flushPromises()

    const initialCols = wrapper.findAll('[data-test="column-key"]').map(el => el.text())
    expect(initialCols.length).toBeGreaterThan(6)

    await wrapper.get('[data-testid="accounts-compact-view-toggle"]').trigger('click')
    await flushPromises()

    const compactCols = wrapper.findAll('[data-test="column-key"]').map(el => el.text())
    expect(compactCols).toEqual(['select', 'name', 'capacity', 'status', 'usage', 'actions'])
    expect(localStorage.getItem('account-compact-view')).toBe('1')

    await wrapper.get('[data-testid="accounts-compact-view-toggle"]').trigger('click')
    await flushPromises()
    const restoredCols = wrapper.findAll('[data-test="column-key"]').map(el => el.text())
    expect(restoredCols).toEqual(initialCols)
    wrapper.unmount()
  })
})
