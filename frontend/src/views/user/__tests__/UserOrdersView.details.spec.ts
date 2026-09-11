import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import UserOrdersView from '../UserOrdersView.vue'

const { getOrder, getMyOrders, showError } = vi.hoisted(() => ({ getOrder: vi.fn(), getMyOrders: vi.fn(), showError: vi.fn() }))
vi.mock('@/api/payment', () => ({ paymentAPI: { getOrder, getMyOrders, getRefundEligibleProviders: vi.fn().mockResolvedValue({ data: { provider_instance_ids: [] } }) } }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showError, showSuccess: vi.fn() }) }))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn() }) }))
vi.mock('vue-i18n', async (importOriginal) => ({ ...(await importOriginal<typeof import('vue-i18n')>()), useI18n: () => ({ t: (key: string) => key }) }))
const order = { id: 51, user_id: 7, status: 'PAID', order_type: 'balance', amount: 100, pay_amount: 100, refund_amount: 0, currency: 'CNY', out_trade_no: 'ORDER-51', created_at: '2026-09-10T00:00:00Z' }

function setup() {
  getMyOrders.mockResolvedValue({ data: { items: [order], total: 1 } })
  return mount(UserOrdersView, { global: { stubs: {
    AppLayout: { template: '<main><slot /></main>' },
    OrderTable: { props: ['orders'], template: '<div data-test="orders"><slot v-for="row in orders" name="actions" :row="row" /></div>' },
    BaseDialog: { props: ['show'], template: '<section v-if="show"><button data-test="close" @click="$emit(\'close\')">Close</button><slot /></section>' },
    Pagination: true, Select: true, Icon: true, InvoiceRequestDialog: true, OrderPurchaseSnapshot: true, OrderLifecycleBadge: true,
  } } })
}

describe('UserOrdersView detail request ownership', () => {
  it.each([false, true])('ignores a stale same-order response after close/reopen (error=%s)', async (rejectOld) => {
    getOrder.mockReset(); showError.mockReset()
    let finishOld!: (value: unknown) => void
    let failOld!: (reason: unknown) => void
    let finishNew!: (value: unknown) => void
    getOrder.mockReturnValueOnce(new Promise((resolve, reject) => { finishOld = resolve; failOld = reject }))
      .mockReturnValueOnce(new Promise((resolve) => { finishNew = resolve }))
    const wrapper = setup()
    await flushPromises()
    await wrapper.get('[data-test="orders"] button').trigger('click')
    await wrapper.get('[data-test="close"]').trigger('click')
    await wrapper.get('[data-test="orders"] button').trigger('click')
    finishNew({ data: { ...order, status: 'COMPLETED' } })
    await flushPromises()
    if (rejectOld) failOld(new Error('old request failed'))
    else finishOld({ data: order })
    await flushPromises()
    expect(wrapper.findComponent({ name: 'OrderPurchaseSnapshot' }).props('order').status).toBe('COMPLETED')
    expect(showError).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
