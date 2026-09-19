import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import UserOrdersView from '../UserOrdersView.vue'
import { formatPaymentAmount } from '@/components/payment/currency'

const { getOrder, getMyOrders, getRefundEligibleProviders, showError, appStoreState } = vi.hoisted(() => ({
  getOrder: vi.fn(),
  getMyOrders: vi.fn(),
  getRefundEligibleProviders: vi.fn().mockResolvedValue({ data: { provider_instance_ids: [] } }),
  showError: vi.fn(),
  appStoreState: { cachedPublicSettings: null as null | Record<string, unknown> },
}))
vi.mock('@/api/payment', () => ({ paymentAPI: { getOrder, getMyOrders, getRefundEligibleProviders } }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showError, showSuccess: vi.fn(), cachedPublicSettings: appStoreState.cachedPublicSettings }) }))
// featureFlags.ts reads the store via '@/stores/app'; both paths must agree.
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ cachedPublicSettings: appStoreState.cachedPublicSettings }) }))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn() }) }))
vi.mock('vue-i18n', async (importOriginal) => ({ ...(await importOriginal<typeof import('vue-i18n')>()), useI18n: () => ({ t: (key: string) => key }) }))
const order = { id: 51, user_id: 7, status: 'PAID', order_type: 'balance', amount: 100, pay_amount: 100, refund_amount: 0, currency: 'CNY', out_trade_no: 'ORDER-51', created_at: '2026-09-10T00:00:00Z' }

function setup(rows = [order]) {
  getMyOrders.mockResolvedValue({ data: { items: rows, total: rows.length } })
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

  it('groups a long order detail into purchase, money, status, and timeline sections', async () => {
    getOrder.mockReset().mockResolvedValue({
      data: {
        ...order,
        status: 'COMPLETED',
        out_trade_no: 'ORDER-51-THIS-IS-A-LONG-ORDER-NUMBER-THAT-MUST-WRAP-SAFELY',
      },
    })
    const wrapper = setup()
    await flushPromises()

    await wrapper.get('[data-test="orders"] button').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-test="order-detail-purchase"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="order-detail-financials"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="order-detail-status"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="order-detail-timeline"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="detail-order-number"]').classes()).toContain('break-all')
    wrapper.unmount()
  })

  it('keeps a subscription plan price in its explicit USD snapshot instead of relabeling it as CNY', async () => {
    getOrder.mockReset().mockResolvedValue({
      data: {
        ...order,
        status: 'COMPLETED',
        order_type: 'subscription',
        amount: 12,
        pay_amount: 88,
        currency: 'CNY',
        product_snapshot: { price: 12, currency: 'USD' },
      },
    })
    const wrapper = setup()
    await flushPromises()

    await wrapper.get('[data-test="orders"] button').trigger('click')
    await flushPromises()

    const financials = wrapper.get('[data-test="order-detail-financials"]')
    expect(financials.text()).toContain(formatPaymentAmount(88, 'CNY'))
    expect(financials.text()).toContain(formatPaymentAmount(12, 'USD'))
    expect(financials.text()).not.toContain(formatPaymentAmount(12, 'CNY'))
    wrapper.unmount()
  })
})

describe('UserOrdersView purchase entry CTA', () => {
  it.each([
    ['hidden when the entry switch is off', { payment_enabled: true, payment_entry_enabled: false }, false],
    ['shown when the entry switch is on', { payment_enabled: true, payment_entry_enabled: true }, true],
    ['shown when the entry switch is missing (defaults on)', { payment_enabled: true }, true],
    ['hidden while payment itself is disabled', { payment_enabled: false }, false],
  ])('recharge CTA %s', async (_name, settings, expectedVisible) => {
    appStoreState.cachedPublicSettings = settings as Record<string, unknown>
    const wrapper = setup()
    await flushPromises()

    const cta = wrapper.findAll('button').find((b) => b.text() === 'payment.result.backToRecharge')
    expect(cta !== undefined).toBe(expectedVisible)
    // Order history stays accessible regardless of the entry switch.
    expect(wrapper.get('[data-test="orders"]').exists()).toBe(true)

    wrapper.unmount()
    appStoreState.cachedPublicSettings = null
  })
})


describe('UserOrdersView single successful refund policy', () => {
  it.each([
    ['COMPLETED', 0, true],
    ['COMPLETED', 1, false],
    ['PARTIALLY_REFUNDED', 1, false],
    ['PARTIALLY_REFUNDED', 0, false],
    ['REFUNDED', 5, false],
  ])('refund entry for %s with settled amount %s is %s', async (status, refunded, visible) => {
    getRefundEligibleProviders.mockResolvedValue({ data: { provider_instance_ids: ['provider-1'] } })
    const row = { ...order, status, refund_amount: refunded, provider_instance_id: 'provider-1' }
    const wrapper = setup([row])
    await flushPromises()
    expect(wrapper.findAll('button').some(button => button.text() === 'payment.orders.requestRefund')).toBe(visible)
    wrapper.unmount()
    getRefundEligibleProviders.mockResolvedValue({ data: { provider_instance_ids: [] } })
  })

  it('uses the settled payment amount in a USD-priced subscription refund prompt', async () => {
    getRefundEligibleProviders.mockResolvedValue({ data: { provider_instance_ids: ['provider-1'] } })
    const row = {
      ...order,
      status: 'COMPLETED',
      order_type: 'subscription',
      amount: 12,
      pay_amount: 88,
      currency: 'CNY',
      provider_instance_id: 'provider-1',
    }
    const wrapper = setup([row])
    await flushPromises()

    const refundButton = wrapper.findAll('button').find(button => button.text() === 'payment.orders.requestRefund')
    expect(refundButton).toBeDefined()
    await refundButton?.trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain(formatPaymentAmount(88, 'CNY'))
    expect(wrapper.text()).not.toContain('$12.00')
    wrapper.unmount()
    getRefundEligibleProviders.mockResolvedValue({ data: { provider_instance_ids: [] } })
  })

  it.each([
    ['ISSUED', false],
    ['REJECTED', true],
  ])('treats an %s invoice as refund-visible=%s', async (invoiceStatus, visible) => {
    getRefundEligibleProviders.mockResolvedValue({ data: { provider_instance_ids: ['provider-1'] } })
    const row = {
      ...order,
      status: 'COMPLETED',
      provider_instance_id: 'provider-1',
      invoice: { status: invoiceStatus },
    }
    const wrapper = setup([row])
    await flushPromises()

    expect(wrapper.findAll('button').some(button => button.text() === 'payment.orders.requestRefund')).toBe(visible)
    wrapper.unmount()
    getRefundEligibleProviders.mockResolvedValue({ data: { provider_instance_ids: [] } })
  })
})
