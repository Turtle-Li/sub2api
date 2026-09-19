import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const {
  cancelOrder,
  getMyOrders,
  getOrder,
  getRefundEligibleProviders,
  resumeOrder,
  showError,
  showInfo,
  showSuccess,
  verifyOrder,
} = vi.hoisted(() => ({
  cancelOrder: vi.fn(),
  getMyOrders: vi.fn(),
  getOrder: vi.fn(),
  getRefundEligibleProviders: vi.fn(),
  resumeOrder: vi.fn(),
  showError: vi.fn(),
  showInfo: vi.fn(),
  showSuccess: vi.fn(),
  verifyOrder: vi.fn(),
}))

vi.mock('@/api/payment', () => ({
  paymentAPI: {
    cancelOrder,
    getMyOrders,
    getOrder,
    getRefundEligibleProviders,
    resumeOrder,
    verifyOrder,
  },
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    cachedPublicSettings: { payment_enabled: true, payment_entry_enabled: true },
    showError,
    showInfo,
    showSuccess,
  }),
}))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ cachedPublicSettings: { payment_enabled: true, payment_entry_enabled: true } }),
}))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn() }) }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key }),
}))

import UserOrdersView from '../UserOrdersView.vue'

const start = new Date('2026-09-19T00:00:00.000Z')

function pendingOrder(expiresAt: string, overrides: Record<string, unknown> = {}) {
  return {
    id: 51,
    user_id: 7,
    amount: 88,
    pay_amount: 88,
    fee_rate: 0,
    refund_amount: 0,
    currency: 'CNY',
    payment_type: 'alipay',
    out_trade_no: 'SUB2-PENDING-51',
    status: 'PENDING',
    order_type: 'balance',
    created_at: '2026-09-18T23:55:00.000Z',
    expires_at: expiresAt,
    ...overrides,
  }
}

function mountView() {
  return mount(UserOrdersView, {
    global: {
      stubs: {
        AppLayout: { template: '<main><slot /></main>' },
        OrderTable: {
          props: ['orders'],
          template: '<div data-test="orders"><template v-for="row in orders" :key="row.id"><slot name="actions" :row="row" /></template></div>',
        },
        BaseDialog: {
          props: ['show'],
          template: '<section v-if="show" data-test="dialog"><slot /><slot name="footer" /></section>',
        },
        PaymentStatusPanel: {
          name: 'PaymentStatusPanel',
          props: ['orderId', 'qrCode', 'payUrl', 'wechatJsapi'],
          template: '<div data-test="resume-panel" :data-order-id="orderId" :data-qr-code="qrCode" :data-pay-url="payUrl" />',
        },
        Icon: true,
        InvoiceRequestDialog: true,
        OrderLifecycleBadge: true,
        OrderPurchaseSnapshot: true,
        Pagination: true,
        Select: true,
      },
    },
  })
}

describe('UserOrdersView pending payment lifecycle', () => {
  let rows: Array<Record<string, unknown>>

  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(start)
    rows = []
    cancelOrder.mockReset()
    getMyOrders.mockReset().mockImplementation(() => Promise.resolve({ data: { items: rows, total: rows.length } }))
    getOrder.mockReset().mockImplementation((orderId: number) => Promise.resolve({ data: rows.find(row => row.id === orderId) }))
    getRefundEligibleProviders.mockReset().mockResolvedValue({ data: { provider_instance_ids: [] } })
    resumeOrder.mockReset()
    verifyOrder.mockReset()
    showError.mockReset()
    showInfo.mockReset()
    showSuccess.mockReset()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('counts down locally, then asks the server to verify instead of declaring the order expired', async () => {
    const order = pendingOrder(new Date(start.getTime() + 2_000).toISOString())
    rows = [order]
    verifyOrder.mockResolvedValue({ data: order })

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-test="order-countdown-51"]').text()).toContain('00:02')

    await vi.advanceTimersByTimeAsync(1_000)
    await flushPromises()
    expect(wrapper.get('[data-test="order-countdown-51"]').text()).toContain('00:01')

    await vi.advanceTimersByTimeAsync(1_000)
    await flushPromises()

    expect(verifyOrder).toHaveBeenCalledWith('SUB2-PENDING-51')
    expect(wrapper.get('[data-test="order-expiry-checking-51"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="continue-payment-51"]').exists()).toBe(false)
    expect(showSuccess).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('reopens only the server-returned launch material for the original pending order', async () => {
    const order = pendingOrder(new Date(start.getTime() + 60_000).toISOString())
    rows = [order]
    resumeOrder.mockResolvedValue({
      data: {
        order_id: 51,
        status: 'PENDING',
        amount: 88,
        pay_amount: 88,
        fee_rate: 0,
        currency: 'CNY',
        payment_type: 'alipay',
        payment_mode: 'qrcode',
        out_trade_no: 'SUB2-PENDING-51',
        qr_code: 'https://qr.example.test/original-51',
        pay_url: 'https://pay.example.test/original-51',
        expires_at: order.expires_at,
      },
    })

    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="continue-payment-51"]').trigger('click')
    await flushPromises()

    expect(resumeOrder).toHaveBeenCalledWith(51)
    expect(wrapper.get('[data-test="resume-panel"]').attributes('data-order-id')).toBe('51')
    expect(wrapper.get('[data-test="resume-panel"]').attributes('data-qr-code')).toBe('https://qr.example.test/original-51')
    expect(wrapper.get('[data-test="resume-panel"]').attributes('data-pay-url')).toBe('https://pay.example.test/original-51')
    wrapper.unmount()
  })

  it('does not reopen a historical checkout URL when resume reports payment confirmation pending', async () => {
    const order = pendingOrder(new Date(start.getTime() + 60_000).toISOString())
    rows = [order]
    resumeOrder.mockRejectedValue({ reason: 'PAYMENT_CONFIRMATION_PENDING' })

    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="continue-payment-51"]').trigger('click')
    await flushPromises()

    expect(resumeOrder).toHaveBeenCalledWith(51)
    expect(wrapper.find('[data-test="resume-panel"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="order-confirmation-pending-51"]').exists()).toBe(true)
    expect(showError).not.toHaveBeenCalled()
    expect(showSuccess).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('locks payment actions and polls after a cancellation request was accepted centrally', async () => {
    const order = pendingOrder(new Date(start.getTime() + 60_000).toISOString())
    rows = [order]
    cancelOrder.mockImplementation(async () => {
      rows = [{ ...order, cancellation_pending: true }]
      throw { reason: 'PAYMENT_CANCELLATION_PENDING' }
    })

    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="cancel-order-51"]').trigger('click')
    await wrapper.get('[data-test="confirm-cancel-order"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-test="order-cancellation-pending-51"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="continue-payment-51"]').exists()).toBe(false)
    expect(showSuccess).not.toHaveBeenCalled()
    expect(showError).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('shows an informational confirmation state when cancellation status is uncertain', async () => {
    const order = pendingOrder(new Date(start.getTime() + 60_000).toISOString())
    rows = [order]
    cancelOrder.mockRejectedValue({ reason: 'PAYMENT_CONFIRMATION_PENDING' })

    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="cancel-order-51"]').trigger('click')
    await wrapper.get('[data-test="confirm-cancel-order"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-test="order-confirmation-pending-51"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="continue-payment-51"]').exists()).toBe(false)
    expect(showSuccess).not.toHaveBeenCalled()
    expect(showError).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
