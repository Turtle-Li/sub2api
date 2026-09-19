import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'

const routeState = vi.hoisted(() => ({
  query: {} as Record<string, unknown>,
}))

const routerPush = vi.hoisted(() => vi.fn())
const pollOrderStatus = vi.hoisted(() => vi.fn())
const verifyOrder = vi.hoisted(() => vi.fn())
const verifyOrderPublic = vi.hoisted(() => vi.fn())
const resolveOrderPublicByResumeToken = vi.hoisted(() => vi.fn())
const refreshUser = vi.hoisted(() => vi.fn())

vi.mock('vue-router', async () => {
  const actual = await vi.importActual<typeof import('vue-router')>('vue-router')
  return {
    ...actual,
    useRoute: () => routeState,
    useRouter: () => ({ push: routerPush }),
  }
})

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

vi.mock('@/stores/payment', () => ({
  usePaymentStore: () => ({
    pollOrderStatus,
  }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    refreshUser,
  }),
}))

vi.mock('@/api/payment', () => ({
  paymentAPI: {
    verifyOrder,
    verifyOrderPublic,
    resolveOrderPublicByResumeToken,
  },
}))

import PaymentResultView from '../PaymentResultView.vue'
import {
  PAYMENT_RECOVERY_STORAGE_KEY,
  RESET_CARD_CHECKOUT_ATTEMPT_STORAGE_KEY,
} from '@/components/payment/paymentFlow'
import { formatPaymentAmount } from '@/components/payment/currency'

enableAutoUnmount(afterEach)

const orderFactory = (status: string) => ({
  id: 42,
  user_id: 9,
  amount: 88,
  pay_amount: 88,
  fee_rate: 0,
  payment_type: 'alipay',
  out_trade_no: 'sub2_20260420abcd1234',
  status,
  order_type: 'balance',
  created_at: '2026-04-20T12:00:00Z',
  expires_at: '2026-04-20T12:30:00Z',
  refund_amount: 0,
})

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

const recoverySnapshotFactory = (resumeToken: string) => ({
  orderId: 42,
  amount: 88,
  qrCode: '',
  expiresAt: '2099-01-01T00:10:00.000Z',
  paymentType: 'alipay',
  payUrl: 'https://pay.example.com/session/42',
  outTradeNo: 'sub2_20260420abcd1234',
  clientSecret: '',
  intentId: '',
  currency: '',
  countryCode: '',
  paymentEnv: '',
  payAmount: 88,
  orderType: 'balance',
  paymentMode: 'popup',
  resumeToken,
  createdAt: Date.UTC(2099, 0, 1, 0, 0, 0),
})

describe('PaymentResultView', () => {
  beforeEach(() => {
    routeState.query = {}
    routerPush.mockReset()
    pollOrderStatus.mockReset()
    verifyOrder.mockReset()
    verifyOrderPublic.mockReset()
    resolveOrderPublicByResumeToken.mockReset()
    refreshUser.mockReset()
    refreshUser.mockResolvedValue({})
    window.localStorage.clear()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('restores the current local order after the unified payment fixed return URL', async () => {
    routeState.query = {}
    window.localStorage.setItem(
      PAYMENT_RECOVERY_STORAGE_KEY,
      JSON.stringify(recoverySnapshotFactory('resume-fixed-return')),
    )
    pollOrderStatus.mockResolvedValueOnce(orderFactory('PENDING'))

    const wrapper = mount(PaymentResultView, {
      global: { stubs: { OrderStatusBadge: true } },
    })

    await flushPromises()

    expect(pollOrderStatus).toHaveBeenCalledWith(42)
    expect(resolveOrderPublicByResumeToken).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.result.processing')
  })

  it('restores the local order when Alipay appends matching browser-return parameters', async () => {
    routeState.query = {
      app_id: '9021000167662176',
      method: 'alipay.trade.page.pay.return',
      out_trade_no: 'sub2_20260420abcd1234',
      trade_no: '2026000000000000',
      total_amount: '88.00',
      sign: 'untrusted-browser-signature',
      sign_type: 'RSA2',
    }
    window.localStorage.setItem(
      PAYMENT_RECOVERY_STORAGE_KEY,
      JSON.stringify(recoverySnapshotFactory('resume-alipay-return')),
    )
    pollOrderStatus.mockResolvedValueOnce(orderFactory('PENDING'))

    const wrapper = mount(PaymentResultView, {
      global: { stubs: { OrderStatusBadge: true } },
    })

    await flushPromises()

    expect(pollOrderStatus).toHaveBeenCalledWith(42)
    expect(verifyOrderPublic).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.result.processing')
  })

  it('ignores a provider out_trade_no and resumes the active local checkout', async () => {
    routeState.query = {
      method: 'alipay.trade.page.pay.return',
      out_trade_no: 'sub2_other_order',
      sign: 'untrusted-browser-signature',
      sign_type: 'RSA2',
    }
    window.localStorage.setItem(
      PAYMENT_RECOVERY_STORAGE_KEY,
      JSON.stringify(recoverySnapshotFactory('resume-alipay-mismatch')),
    )
    pollOrderStatus.mockResolvedValueOnce(orderFactory('PENDING'))

    const wrapper = mount(PaymentResultView, {
      global: { stubs: { OrderStatusBadge: true } },
    })

    await flushPromises()

    expect(pollOrderStatus).toHaveBeenCalledWith(42)
    expect(verifyOrderPublic).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.result.processing')
  })

  it('does not treat a browser return or embedded checkout frame as payment completion', async () => {
    routeState.query = {
      resume_token: 'resume-42',
      order_id: '999',
      status: 'success',
    }
    window.localStorage.setItem(PAYMENT_RECOVERY_STORAGE_KEY, JSON.stringify({
      orderId: 42,
      amount: 88,
      qrCode: '',
      expiresAt: '2099-01-01T00:10:00.000Z',
      paymentType: 'alipay',
      payUrl: 'https://pay.example.com/session/42',
      checkoutFrameUrl: 'https://openapi.alipay.com/gateway.do?method=alipay.trade.page.pay&biz_content=%7B%22qr_pay_mode%22%3A%224%22%2C%22qrcode_width%22%3A%22224%22%7D&sign_type=RSA2&sign=signed',
      outTradeNo: 'sub2_20260420abcd1234',
      clientSecret: '',
      intentId: '',
      currency: '',
      countryCode: '',
      paymentEnv: '',
      payAmount: 88,
      orderType: 'balance',
      paymentMode: 'redirect',
      resumeToken: 'resume-42',
      createdAt: Date.UTC(2099, 0, 1, 0, 0, 0),
    }))
    resolveOrderPublicByResumeToken.mockResolvedValue({
      data: orderFactory('PENDING'),
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledWith('resume-42')
    expect(pollOrderStatus).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.result.processing')
    expect(wrapper.text()).not.toContain('payment.result.success')
    expect(wrapper.text()).not.toContain('payment.result.failed')
  })

  it('keeps a provider success return in confirmation state when the first order lookup is transiently unavailable', async () => {
    routeState.query = {
      order_id: '42',
      status: 'success',
    }
    pollOrderStatus.mockRejectedValueOnce(new Error('temporary network failure'))

    const wrapper = mount(PaymentResultView, {
      global: { stubs: { OrderStatusBadge: true } },
    })

    await flushPromises()

    expect(pollOrderStatus).toHaveBeenCalledWith(42)
    expect(wrapper.text()).toContain('payment.result.processing')
    expect(wrapper.text()).not.toContain('payment.result.failed')
  })

  it('retries a known route order after an initial empty lookup', async () => {
    vi.useFakeTimers()
    routeState.query = { order_id: '42' }
    pollOrderStatus
      .mockResolvedValueOnce(null)
      .mockResolvedValueOnce(orderFactory('COMPLETED'))

    const wrapper = mount(PaymentResultView, {
      global: { stubs: { OrderStatusBadge: true } },
    })

    await flushPromises()
    expect(pollOrderStatus).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('payment.result.processing')

    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()

    expect(pollOrderStatus).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('payment.result.success')
  })

  it('retries a signed resume token after a transient lookup failure', async () => {
    vi.useFakeTimers()
    routeState.query = { resume_token: 'resume-retry' }
    resolveOrderPublicByResumeToken
      .mockRejectedValueOnce(new Error('temporary network failure'))
      .mockResolvedValueOnce({ data: orderFactory('COMPLETED') })

    const wrapper = mount(PaymentResultView, {
      global: { stubs: { OrderStatusBadge: true } },
    })

    await flushPromises()
    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('payment.result.processing')

    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()

    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('payment.result.success')
  })

  it('distinguishes confirmed payment from failed fulfillment', async () => {
    routeState.query = { resume_token: 'resume-fulfillment-failed' }
    window.localStorage.setItem(
      PAYMENT_RECOVERY_STORAGE_KEY,
      JSON.stringify(recoverySnapshotFactory('resume-fulfillment-failed')),
    )
    resolveOrderPublicByResumeToken.mockResolvedValue({
      data: {
        ...orderFactory('FAILED'),
        paid_at: '2026-04-20T12:01:00Z',
        payment_status: 'PAID',
        fulfillment_status: 'FAILED',
      },
    })
    const wrapper = mount(PaymentResultView, { global: { stubs: { OrderStatusBadge: true } } })
    await flushPromises()
    expect(wrapper.text()).toContain('payment.result.paidButFulfillmentFailed')
    expect(wrapper.text()).not.toContain('payment.result.failed')
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).not.toBeNull()
  })

  it('keeps PAID in payment processing until the server reports COMPLETED', async () => {
    routeState.query = { resume_token: 'resume-paid' }
    window.localStorage.setItem(
      PAYMENT_RECOVERY_STORAGE_KEY,
      JSON.stringify(recoverySnapshotFactory('resume-paid')),
    )
    resolveOrderPublicByResumeToken.mockResolvedValue({
      data: {
        ...orderFactory('PAID'),
        paid_at: '2026-04-20T12:01:00Z',
        payment_status: 'PAID',
        fulfillment_status: 'FULFILLED',
      },
    })

    const wrapper = mount(PaymentResultView, { global: { stubs: { OrderStatusBadge: true } } })
    await flushPromises()

    expect(wrapper.text()).toContain('payment.result.processing')
    expect(wrapper.text()).not.toContain('payment.result.success')
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).not.toBeNull()
  })

  it('does not turn a confirmed payment into failure when its automatic refresh budget is exhausted', async () => {
    vi.useFakeTimers()
    routeState.query = { resume_token: 'resume-paid-retry-budget' }
    window.localStorage.setItem(
      PAYMENT_RECOVERY_STORAGE_KEY,
      JSON.stringify(recoverySnapshotFactory('resume-paid-retry-budget')),
    )
    resolveOrderPublicByResumeToken.mockResolvedValue({
      data: {
        ...orderFactory('RECHARGING'),
        paid_at: '2026-04-20T12:01:00Z',
        payment_status: 'PAID',
        fulfillment_status: 'PENDING',
      },
    })

    const wrapper = mount(PaymentResultView, { global: { stubs: { OrderStatusBadge: true } } })
    await flushPromises()
    await vi.advanceTimersByTimeAsync(15 * 2000)
    await flushPromises()

    expect(wrapper.text()).toContain('payment.result.processing')
    expect(wrapper.text()).not.toContain('payment.result.failed')
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).not.toBeNull()
  })

  it('prefers the public resume-token result over a stale restored DB snapshot', async () => {
    routeState.query = {
      resume_token: 'resume-authoritative',
      order_id: '42',
      status: 'success',
    }
    window.localStorage.setItem(PAYMENT_RECOVERY_STORAGE_KEY, JSON.stringify({
      orderId: 42,
      amount: 88,
      qrCode: '',
      expiresAt: '2099-01-01T00:10:00.000Z',
      paymentType: 'alipay',
      payUrl: 'https://pay.example.com/session/42',
      outTradeNo: 'sub2_20260420abcd1234',
      clientSecret: '',
      intentId: '',
      currency: '',
      countryCode: '',
      paymentEnv: '',
      payAmount: 88,
      orderType: 'balance',
      paymentMode: 'popup',
      resumeToken: 'resume-authoritative',
      createdAt: Date.UTC(2099, 0, 1, 0, 0, 0),
    }))
    resolveOrderPublicByResumeToken.mockResolvedValue({
      data: {
        ...orderFactory('COMPLETED'),
        amount: 100,
        pay_amount: 103,
        fee_rate: 3,
      },
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(pollOrderStatus).not.toHaveBeenCalled()
    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledWith('resume-authoritative')
    expect(refreshUser).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('payment.result.success')
    expect(wrapper.text()).toContain('103.00')
    expect(wrapper.text()).toContain('100.00')
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBeNull()
  })

  it('waits for completed fulfillment before refreshing the user balance', async () => {
    vi.useFakeTimers()
    routeState.query = {
      resume_token: 'resume-77',
    }
    window.localStorage.setItem(
      PAYMENT_RECOVERY_STORAGE_KEY,
      JSON.stringify(recoverySnapshotFactory('resume-77')),
    )
    resolveOrderPublicByResumeToken
      .mockResolvedValueOnce({
        data: orderFactory('PENDING'),
      })
      .mockResolvedValueOnce({
        data: orderFactory('COMPLETED'),
      })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledTimes(1)
    expect(refreshUser).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.result.processing')
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).not.toBeNull()

    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()

    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledTimes(2)
    expect(refreshUser).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('payment.result.success')
    expect(wrapper.text()).not.toContain('payment.result.failed')
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBeNull()
  })

  it('clears a reset-card checkout attempt only after the server reports its terminal order', async () => {
    routeState.query = { resume_token: 'resume-reset-card' }
    window.localStorage.setItem(RESET_CARD_CHECKOUT_ATTEMPT_STORAGE_KEY, JSON.stringify({
      fingerprint: 'reset-card-fingerprint',
      idempotencyKey: 'reset-card-payment-1',
      orderId: 42,
    }))
    resolveOrderPublicByResumeToken.mockResolvedValue({
      data: { ...orderFactory('COMPLETED'), order_type: 'reset_card' },
    })

    mount(PaymentResultView, { global: { stubs: { OrderStatusBadge: true } } })
    await flushPromises()

    expect(window.localStorage.getItem(RESET_CARD_CHECKOUT_ATTEMPT_STORAGE_KEY)).toBeNull()
  })

  it('keeps the successful result when refreshing the user balance fails', async () => {
    routeState.query = {
      resume_token: 'resume-refresh-failure',
    }
    resolveOrderPublicByResumeToken.mockResolvedValue({
      data: orderFactory('COMPLETED'),
    })
    refreshUser.mockRejectedValueOnce(new Error('profile refresh failed'))

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(refreshUser).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('payment.result.success')
    expect(wrapper.text()).not.toContain('payment.result.failed')
  })

  it('falls back to order_id polling when resume-token recovery fails without clearing a different active order', async () => {
    routeState.query = {
      resume_token: 'resume-fail',
      order_id: '77',
    }
    window.localStorage.setItem(
      PAYMENT_RECOVERY_STORAGE_KEY,
      JSON.stringify({
        ...recoverySnapshotFactory('resume-fail'),
        orderId: 42,
      }),
    )
    resolveOrderPublicByResumeToken.mockRejectedValueOnce(new Error('resume failed'))
    pollOrderStatus.mockResolvedValueOnce({
      ...orderFactory('COMPLETED'),
      id: 77,
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledWith('resume-fail')
    expect(pollOrderStatus).toHaveBeenCalledWith(77)
    expect(verifyOrderPublic).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.result.success')
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).not.toBeNull()
  })

  it('falls back to public out_trade_no verification when resume_token recovery fails in legacy return flows', async () => {
    routeState.query = {
      resume_token: 'resume-fail',
      out_trade_no: 'legacy-should-not-run',
      trade_status: 'TRADE_SUCCESS',
    }
    resolveOrderPublicByResumeToken.mockRejectedValueOnce(new Error('resume failed'))
    verifyOrderPublic.mockResolvedValueOnce({
      data: {
        ...orderFactory('COMPLETED'),
        out_trade_no: 'legacy-should-not-run',
      },
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledWith('resume-fail')
    expect(verifyOrderPublic).toHaveBeenCalledWith('legacy-should-not-run')
    expect(pollOrderStatus).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.result.success')
  })

  it('uses the active local recovery snapshot for a fixed return with provider metadata only', async () => {
    routeState.query = {
      trade_status: 'TRADE_SUCCESS',
    }
    window.localStorage.setItem(
      PAYMENT_RECOVERY_STORAGE_KEY,
      JSON.stringify(recoverySnapshotFactory('resume-stale')),
    )
    pollOrderStatus.mockResolvedValueOnce(orderFactory('PENDING'))

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(resolveOrderPublicByResumeToken).not.toHaveBeenCalled()
    expect(verifyOrderPublic).not.toHaveBeenCalled()
    expect(pollOrderStatus).toHaveBeenCalledWith(42)
    expect(wrapper.text()).toContain('payment.result.processing')
    expect(wrapper.text()).toContain('sub2_20260420abcd1234')
  })

  it('uses public out_trade_no verification when no signed resume context is available', async () => {
    routeState.query = {
      out_trade_no: 'legacy-123',
      trade_status: 'TRADE_SUCCESS',
    }
    verifyOrder.mockRejectedValue(new Error('auth required'))
    verifyOrderPublic.mockResolvedValue({
      data: orderFactory('COMPLETED'),
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(verifyOrder).toHaveBeenCalledWith('legacy-123')
    expect(verifyOrderPublic).toHaveBeenCalledWith('legacy-123')
    expect(pollOrderStatus).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.result.success')
  })

  it('renders the minimal public out_trade_no verification result without payment_type', async () => {
    routeState.query = {
      out_trade_no: 'legacy-minimal',
      trade_status: 'TRADE_SUCCESS',
    }
    verifyOrder.mockRejectedValue(new Error('auth required'))
    verifyOrderPublic.mockResolvedValue({
      data: {
        out_trade_no: 'legacy-minimal',
        status: 'PAID',
        paid: true,
        created_at: '2026-04-20T12:00:00Z',
        expires_at: '2026-04-20T12:30:00Z',
      },
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('payment.result.processing')
    expect(wrapper.text()).toContain('legacy-minimal')
    expect(wrapper.text()).not.toContain('payment.orders.paymentMethod')
  })

  it('prefers authenticated order verification before falling back to public lookup', async () => {
    routeState.query = {
      out_trade_no: 'auth-verify-123',
      trade_status: 'TRADE_SUCCESS',
    }
    verifyOrder.mockResolvedValue({
      data: orderFactory('COMPLETED'),
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(verifyOrder).toHaveBeenCalledWith('auth-verify-123')
    expect(verifyOrderPublic).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.result.success')
  })

  it('does not use public out_trade_no verification for bare order numbers without legacy return markers', async () => {
    routeState.query = {
      out_trade_no: 'legacy-bare',
    }

    mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(verifyOrderPublic).not.toHaveBeenCalled()
  })

  it('shows unknown and never looks up a raw central-provider out_trade_no without trusted local recovery', async () => {
    routeState.query = {
      method: 'alipay.trade.page.pay.return',
      app_id: 'provider-app',
      out_trade_no: 'provider-channel-order',
      trade_status: 'TRADE_SUCCESS',
    }

    const wrapper = mount(PaymentResultView, {
      global: { stubs: { OrderStatusBadge: true } },
    })
    await flushPromises()

    expect(resolveOrderPublicByResumeToken).not.toHaveBeenCalled()
    expect(pollOrderStatus).not.toHaveBeenCalled()
    expect(verifyOrder).not.toHaveBeenCalled()
    expect(verifyOrderPublic).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.result.unknown')
  })

  it('drops a deferred initialization result after unmount', async () => {
    const pending = deferred<{ data: ReturnType<typeof orderFactory> }>()
    routeState.query = { resume_token: 'resume-disposed' }
    resolveOrderPublicByResumeToken.mockReturnValueOnce(pending.promise)

    const wrapper = mount(PaymentResultView, {
      global: { stubs: { OrderStatusBadge: true } },
    })
    await flushPromises()
    wrapper.unmount()

    pending.resolve({ data: orderFactory('COMPLETED') })
    await flushPromises()

    expect(refreshUser).not.toHaveBeenCalled()
  })

  it('drops a deferred manual refresh after unmount', async () => {
    const pending = deferred<ReturnType<typeof orderFactory>>()
    routeState.query = { order_id: '42' }
    pollOrderStatus
      .mockResolvedValueOnce(orderFactory('PENDING'))
      .mockReturnValueOnce(pending.promise)

    const wrapper = mount(PaymentResultView, {
      global: { stubs: { OrderStatusBadge: true } },
    })
    await flushPromises()

    const refreshButton = wrapper.findAll('button')
      .find(button => button.text() === 'payment.qr.refreshStatus')
    expect(refreshButton).toBeDefined()
    await refreshButton!.trigger('click')
    await flushPromises()

    wrapper.unmount()
    pending.resolve(orderFactory('COMPLETED'))
    await flushPromises()

    expect(refreshUser).not.toHaveBeenCalled()
  })

  it('resolves order by resume token when local recovery snapshot is missing', async () => {
    routeState.query = {
      resume_token: 'resume-77',
    }
    resolveOrderPublicByResumeToken.mockResolvedValue({
      data: orderFactory('COMPLETED'),
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(resolveOrderPublicByResumeToken).toHaveBeenCalledWith('resume-77')
    expect(wrapper.text()).toContain('payment.result.success')
  })

  it('uses the currency returned by the order API when rendering amounts', async () => {
    routeState.query = {
      resume_token: 'resume-hkd',
    }
    resolveOrderPublicByResumeToken.mockResolvedValue({
      data: {
        ...orderFactory('PAID'),
        currency: 'HKD',
        amount: 100,
        pay_amount: 103,
        fee_rate: 3,
      },
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain(formatPaymentAmount(103, 'HKD'))
  })

  it('wraps and copies a long order number while preserving a reset-card CNY result', async () => {
    const orderNumber = 'SUB2-RESET-CARD-ORDER-NUMBER-THAT-IS-LONG-ENOUGH-TO-WRAP-WITHOUT-SQUEEZING-LABELS'
    routeState.query = { resume_token: 'resume-reset-card-long-order' }
    resolveOrderPublicByResumeToken.mockResolvedValue({
      data: {
        ...orderFactory('COMPLETED'),
        order_type: 'reset_card',
        amount: 37.02,
        pay_amount: 37.02,
        currency: 'CNY',
        out_trade_no: orderNumber,
      },
    })
    const clipboardDescriptor = Object.getOwnPropertyDescriptor(navigator, 'clipboard')
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })

    const wrapper = mount(PaymentResultView, { global: { stubs: { OrderStatusBadge: true, Icon: true } } })
    await flushPromises()

    expect(wrapper.text()).toContain(formatPaymentAmount(37.02, 'CNY'))
    expect(wrapper.get('[data-test="payment-result-page-order-number"]').classes()).toContain('break-all')
    await wrapper.get('[data-test="copy-payment-result-page-order"]').trigger('click')
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith(orderNumber)

    wrapper.unmount()
    if (clipboardDescriptor) Object.defineProperty(navigator, 'clipboard', clipboardDescriptor)
    else delete (navigator as Navigator & { clipboard?: Clipboard }).clipboard
  })

  it('does not relabel a USD subscription price as a CNY settlement amount', async () => {
    routeState.query = { resume_token: 'resume-subscription-cny-settlement' }
    resolveOrderPublicByResumeToken.mockResolvedValue({
      data: {
        ...orderFactory('COMPLETED'),
        order_type: 'subscription',
        amount: 12,
        pay_amount: 88,
        currency: 'CNY',
      },
    })

    const wrapper = mount(PaymentResultView, { global: { stubs: { OrderStatusBadge: true } } })
    await flushPromises()

    expect(wrapper.text()).toContain(formatPaymentAmount(88, 'CNY'))
    expect(wrapper.text()).not.toContain(formatPaymentAmount(12, 'CNY'))
    expect(wrapper.text()).not.toContain('payment.orders.baseAmount')
  })

  it('normalizes aliased payment methods before rendering the label', async () => {
    routeState.query = {
      resume_token: 'resume-88',
    }
    resolveOrderPublicByResumeToken.mockResolvedValueOnce({
      data: {
        ...orderFactory('PAID'),
        payment_type: 'alipay_direct',
      },
    })

    const wrapper = mount(PaymentResultView, {
      global: {
        stubs: {
          OrderStatusBadge: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('payment.methods.alipay')
    expect(wrapper.text()).not.toContain('payment.methods.alipay_direct')
  })
})
