import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import SubscriptionsView from '../SubscriptionsView.vue'

const { listSubscriptions, getAllGroups } = vi.hoisted(() => ({
  listSubscriptions: vi.fn(),
  getAllGroups: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    subscriptions: {
      list: listSubscriptions,
      assign: vi.fn(),
      extend: vi.fn(),
      revoke: vi.fn(),
      restore: vi.fn(),
      resetQuota: vi.fn(),
      grantResetCards: vi.fn(),
      grantResetCardsToSubscription: vi.fn()
    },
    groups: {
      getAll: getAllGroups
    },
    usage: {
      searchUsers: vi.fn()
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showWarning: vi.fn()
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) =>
        key === 'admin.subscriptions.remainingResetCount'
          ? `Resets remaining: ${params?.count ?? 0}`
          : key
    })
  }
})

const DataTableStub = {
  props: ['data'],
  template: `
    <div>
      <div v-for="row in data" :key="row.id" data-test="subscription-row">
        <slot name="cell-usage" :row="row" />
      </div>
    </div>
  `
}

describe('admin SubscriptionsView reset counts', () => {
  beforeEach(() => {
    localStorage.clear()
    listSubscriptions.mockReset()
    getAllGroups.mockReset()

    listSubscriptions.mockResolvedValue({
      items: [
        {
          id: 1,
          user_id: 10,
          group_id: 20,
          status: 'active',
          starts_at: '2026-07-01T00:00:00Z',
          expires_at: '2026-08-01T00:00:00Z',
          daily_usage_usd: 1,
          weekly_usage_usd: 2,
          monthly_usage_usd: 3,
          daily_window_start: null,
          weekly_window_start: null,
          monthly_window_start: null,
          created_at: '2026-07-01T00:00:00Z',
          updated_at: '2026-07-01T00:00:00Z',
          reset_card_count: 4,
          group: {
            daily_limit_usd: 10,
            weekly_limit_usd: null,
            monthly_limit_usd: null
          }
        },
        {
          id: 2,
          user_id: 11,
          group_id: 21,
          status: 'active',
          starts_at: '2026-07-01T00:00:00Z',
          expires_at: '2026-08-01T00:00:00Z',
          daily_usage_usd: 0,
          weekly_usage_usd: 0,
          monthly_usage_usd: 0,
          daily_window_start: null,
          weekly_window_start: null,
          monthly_window_start: null,
          created_at: '2026-07-01T00:00:00Z',
          updated_at: '2026-07-01T00:00:00Z',
          group: {
            daily_limit_usd: null,
            weekly_limit_usd: null,
            monthly_limit_usd: null
          }
        }
      ],
      total: 2,
      page: 1,
      page_size: 20,
      pages: 1
    })
    getAllGroups.mockResolvedValue([])
  })

  it('shows each subscription remaining reset count in the usage cell', async () => {
    const wrapper = mount(SubscriptionsView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          TablePageLayout: {
            template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>'
          },
          DataTable: DataTableStub,
          Pagination: true,
          BaseDialog: true,
          ConfirmDialog: true,
          EmptyState: true,
          Select: true,
          GroupBadge: true,
          GroupOptionItem: true,
          Icon: true,
          RouterLink: true,
          Teleport: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.findAll('[data-test="remaining-reset-count"]').map((item) => item.text()))
      .toEqual(['Resets remaining: 4', 'Resets remaining: 0'])
  })
})
