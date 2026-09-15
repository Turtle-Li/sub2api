import { flushPromises, mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import AdminPaymentPlansView from '../AdminPaymentPlansView.vue'

const { getPlans, getConfig, getGroups } = vi.hoisted(() => ({
  getPlans: vi.fn(),
  getConfig: vi.fn(),
  getGroups: vi.fn(),
}))

vi.mock('@/api/admin/payment', () => ({
  adminPaymentAPI: {
    getPlans,
    getConfig,
  },
}))

vi.mock('@/api/admin', () => ({
  default: {
    groups: {
      getAll: getGroups,
    },
  },
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

const DataTableStub = {
  props: ['data'],
  template: `
    <div>
      <div v-for="row in data" :key="row.id">
        <slot name="cell-price" :value="row.price" :row="row" />
        <slot name="cell-validity_days" :value="row.validity_days" :row="row" />
        <slot name="cell-reset_card_delivery" :row="row" />
      </div>
    </div>
  `,
}

describe('AdminPaymentPlansView', () => {
  beforeEach(() => {
    getGroups.mockResolvedValue([])
    getConfig.mockResolvedValue({ data: {} })
    getPlans.mockResolvedValue({
      data: [
        {
          id: 1,
          name: 'CNY plan',
          group_id: 1,
          price: 499,
          original_price: 599,
          currency: 'CNY',
          validity_days: 30,
          validity_unit: 'day',
          sort_order: 0,
          for_sale: true,
          features: [],
          entitlements: {
            balance_bonus: 0,
            reset_card_count: 2,
            reset_card_delivery_mode: 'monthly',
            reset_card_issue_count: 6,
            reset_card_expiry_days: 14,
            reset_card_expiry_unit: 'day',
            concurrency: 0,
          },
        },
        {
          id: 2,
          name: 'Legacy plan',
          group_id: 1,
          price: 10,
          original_price: 0,
          currency: '',
          validity_days: 30,
          validity_unit: 'day',
          sort_order: 0,
          for_sale: true,
          features: [],
        },
      ],
    })
  })

  it('uses the configured currency symbol and keeps legacy prices in USD', async () => {
    const wrapper = mount(AdminPaymentPlansView, {
      global: {
        plugins: [createPinia()],
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          DataTable: DataTableStub,
          ConfirmDialog: true,
          GroupBadge: true,
          Icon: true,
          PlanEditDialog: true,
          AdminRechargeCatalogPanel: { template: '<div data-testid="recharge-catalog" />' },
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('¥499.00CNY')
    expect(wrapper.text()).toContain('¥599.00')
    expect(wrapper.text()).toContain('$10.00')
    expect(wrapper.text()).toContain('30 payment.validityUnits.dayMany')
    expect(wrapper.text()).toContain('payment.entitlements.monthlyResetCards')
    expect(wrapper.find('[data-testid="monthly-reset-cards-toggle"]').exists()).toBe(false)
  })

  it('keeps subscription and balance products in one catalogue entry', async () => {
    const wrapper = mount(AdminPaymentPlansView, {
      global: {
        plugins: [createPinia()],
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          DataTable: DataTableStub,
          ConfirmDialog: true,
          GroupBadge: true,
          Icon: true,
          PlanEditDialog: true,
          AdminRechargeCatalogPanel: { template: '<div data-testid="recharge-catalog" />' },
        },
      },
    })
    await flushPromises()

    expect(wrapper.text()).toContain('payment.admin.subscriptionProducts')
    expect(wrapper.text()).toContain('payment.admin.balanceProducts')
    expect(wrapper.find('[data-testid="recharge-catalog"]').exists()).toBe(false)

    const balanceTab = wrapper.findAll('[role="tab"]').find(tab => tab.text().includes('payment.admin.balanceProducts'))
    await balanceTab!.trigger('click')
    expect(wrapper.find('[data-testid="recharge-catalog"]').exists()).toBe(true)
  })

  it('keeps monthly delivery and compatibility as built-in product behavior', async () => {
    const wrapper = mount(AdminPaymentPlansView, {
      global: {
        plugins: [createPinia()],
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          DataTable: DataTableStub,
          ConfirmDialog: true,
          GroupBadge: true,
          Icon: true,
          PlanEditDialog: true,
          AdminRechargeCatalogPanel: true,
        },
      },
    })
    await flushPromises()

    expect(wrapper.find('[data-testid="monthly-reset-cards-toggle"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('payment.admin.resetCardTierPolicy')
  })
})
