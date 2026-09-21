import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const pollOrderStatus = vi.hoisted(() => vi.fn())
const cancelOrder = vi.hoisted(() => vi.fn())
const verifyOrder = vi.hoisted(() => vi.fn())
const resumeOrder = vi.hoisted(() => vi.fn())
const showError = vi.hoisted(() => vi.fn())
const showInfo = vi.hoisted(() => vi.fn())
const showSuccess = vi.hoisted(() => vi.fn())
const toCanvas = vi.hoisted(() => vi.fn())
const isMobileDevice = vi.hoisted(() => vi.fn(() => false))

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

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    showError,
    showInfo,
    showSuccess,
  }),
}))

vi.mock('@/api/payment', () => ({
  paymentAPI: {
    cancelOrder,
    verifyOrder,
    resumeOrder,
  },
}))

vi.mock('qrcode', () => ({
  default: {
    toCanvas,
  },
}))

vi.mock('@/utils/device', () => ({
  isMobileDevice,
}))

import PaymentStatusPanel from '../PaymentStatusPanel.vue'
import { PAYMENT_CANCELLATION_STORAGE_KEY } from '@/components/payment/paymentFlow'
import { formatPaymentAmount } from '../currency'

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
  expires_at: '2099-01-01T12:30:00Z',
  refund_amount: 0,
})

function alipayCheckoutFrameUrl(overrides: {
  host?: string
  qrPayMode?: string | number
  qrcodeWidth?: string | number
} = {}): string {
  const url = new URL(`https://${overrides.host || 'openapi.alipay.com'}/gateway.do`)
  url.searchParams.set('method', 'alipay.trade.page.pay')
  url.searchParams.set('biz_content', JSON.stringify({
    out_trade_no: 'sub2_42',
    qr_pay_mode: overrides.qrPayMode ?? '4',
    qrcode_width: overrides.qrcodeWidth ?? '224',
  }))
  url.searchParams.set('sign_type', 'RSA2')
  url.searchParams.set('sign', 'signed-payload')
  return url.toString()
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

describe('PaymentStatusPanel', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    window.localStorage.clear()
    pollOrderStatus.mockReset()
    cancelOrder.mockReset()
    verifyOrder.mockReset()
    resumeOrder.mockReset()
    showError.mockReset()
    showInfo.mockReset()
    showSuccess.mockReset()
    toCanvas.mockReset().mockResolvedValue(undefined)
    isMobileDevice.mockReset().mockReturnValue(false)
  })

  afterEach(() => {
    window.localStorage.clear()
    vi.useRealTimers()
  })

  it('keeps RECHARGING in the payment processing state', async () => {
    pollOrderStatus.mockResolvedValue(orderFactory('RECHARGING'))

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://qr.alipay.com/qr-42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()

    expect(pollOrderStatus).toHaveBeenCalledWith(42)
    expect(wrapper.text()).not.toContain('payment.result.success')
    expect(wrapper.emitted('success')).toBeUndefined()
  })

  it('does not embed a hosted Alipay page and keeps the normal QR surface', async () => {
    const checkoutFrameUrl = alipayCheckoutFrameUrl()
    pollOrderStatus.mockResolvedValue(orderFactory('PENDING'))
    const openSpy = vi.spyOn(window, 'open')

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://qr.alipay.com/alipay-42',
        payUrl: 'https://pay.totools.cn/checkout/alipay-42',
        checkoutFrameUrl,
        allowCheckoutFrame: true,
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()

    expect(wrapper.find('[data-test="alipay-checkout-frame"]').exists()).toBe(false)
    expect(toCanvas).toHaveBeenCalledWith(expect.any(HTMLCanvasElement), 'https://qr.alipay.com/alipay-42', expect.any(Object))
    expect(openSpy).not.toHaveBeenCalled()
    openSpy.mockRestore()
  })

  it('keeps a validated frame on the existing QR path without local modal opt-in', async () => {
    pollOrderStatus.mockResolvedValue(orderFactory('PENDING'))
    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://qr.alipay.com/alipay-42',
        checkoutFrameUrl: alipayCheckoutFrameUrl(),
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()

    expect(wrapper.find('[data-test="alipay-checkout-frame"]').exists()).toBe(false)
    expect(toCanvas).toHaveBeenCalledWith(expect.any(HTMLCanvasElement), 'https://qr.alipay.com/alipay-42', expect.any(Object))
  })

  it('rejects an invalid embedded URL and keeps the normal Alipay QR flow', async () => {
    pollOrderStatus.mockResolvedValue(orderFactory('PENDING'))
    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://qr.alipay.com/alipay-42',
        checkoutFrameUrl: alipayCheckoutFrameUrl({ host: 'checkout.example.invalid' }),
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()

    expect(wrapper.find('[data-test="alipay-checkout-frame"]').exists()).toBe(false)
    expect(toCanvas).toHaveBeenCalledWith(expect.any(HTMLCanvasElement), 'https://qr.alipay.com/alipay-42', expect.any(Object))
  })

  it('embeds a validated Alipay checkout frame when native QR is absent and allowCheckoutFrame is true', async () => {
    const checkoutFrameUrl = alipayCheckoutFrameUrl()
    pollOrderStatus.mockResolvedValue(orderFactory('PENDING'))
    const openSpy = vi.spyOn(window, 'open')
    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: '',
        payUrl: 'https://pay.totools.cn/checkout/alipay-42',
        checkoutFrameUrl,
        allowCheckoutFrame: true,
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()
    const iframe = wrapper.find('[data-test="alipay-checkout-frame"]')
    expect(iframe.exists()).toBe(true)
    expect(iframe.attributes('src')).toBe(checkoutFrameUrl)
    expect(toCanvas).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.qr.openPayWindow')
    expect(openSpy).not.toHaveBeenCalled()
    openSpy.mockRestore()
  })

  it('waits for the server result while the embedded checkout frame is open', async () => {
    const checkoutFrameUrl = alipayCheckoutFrameUrl()
    pollOrderStatus
      .mockResolvedValueOnce(orderFactory('PENDING'))
      .mockResolvedValueOnce(orderFactory('COMPLETED'))
    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: '',
        checkoutFrameUrl,
        allowCheckoutFrame: true,
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()
    expect(wrapper.find('[data-test="alipay-checkout-frame"]').exists()).toBe(true)
    expect(toCanvas).not.toHaveBeenCalled()

    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()

    expect(wrapper.emitted('success')).toHaveLength(1)
  })

  it('falls back to redirect waiting card without drawing canvas when allowCheckoutFrame is false', async () => {
    pollOrderStatus.mockResolvedValue(orderFactory('PENDING'))
    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: '',
        checkoutFrameUrl: alipayCheckoutFrameUrl(),
        allowCheckoutFrame: false,
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()

    expect(wrapper.find('[data-test="alipay-checkout-frame"]').exists()).toBe(false)
    expect(toCanvas).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.qr.openPayWindow')
    wrapper.unmount()
  })

  it('falls back to redirect waiting mode when iframe triggers an error', async () => {
    const checkoutFrameUrl = alipayCheckoutFrameUrl()
    pollOrderStatus.mockResolvedValue(orderFactory('PENDING'))
    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: '',
        checkoutFrameUrl,
        allowCheckoutFrame: true,
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()
    const iframe = wrapper.find('[data-test="alipay-checkout-frame"]')
    expect(iframe.exists()).toBe(true)

    await iframe.trigger('error')
    await flushPromises()

    expect(wrapper.find('[data-test="alipay-checkout-frame"]').exists()).toBe(false)
    expect(toCanvas).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.qr.openPayWindow')
  })

  it('does not use an Alipay checkout frame for WeChat QR payments', async () => {
    pollOrderStatus.mockResolvedValue(orderFactory('PENDING'))
    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'weixin://wxpay/bizpayurl?pr=unchanged',
        checkoutFrameUrl: alipayCheckoutFrameUrl(),
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'wxpay',
        orderType: 'balance',
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()

    expect(wrapper.find('[data-test="alipay-checkout-frame"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('payment.qr.scanWxpay')
    expect(toCanvas).toHaveBeenCalledWith(expect.any(HTMLCanvasElement), 'weixin://wxpay/bizpayurl?pr=unchanged', expect.any(Object))
  })

  it('uses the completed reset-card order currency and lets a long order number be copied', async () => {
    const orderNumber = 'SUB2-RESET-CARD-ORDER-NUMBER-THAT-IS-LONG-ENOUGH-TO-WRAP-WITHOUT-SQUEEZING-LABELS'
    pollOrderStatus.mockResolvedValue({
      ...orderFactory('COMPLETED'),
      amount: 37.02,
      pay_amount: 37.02,
      currency: 'CNY',
      order_type: 'reset_card',
      out_trade_no: orderNumber,
    })
    const clipboardDescriptor = Object.getOwnPropertyDescriptor(navigator, 'clipboard')
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        amount: 5,
        payAmount: 5,
        qrCode: '',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
        currency: 'USD',
      },
      global: { stubs: { Icon: true } },
    })
    await flushPromises()

    expect(wrapper.text()).toContain(formatPaymentAmount(37.02, 'CNY'))
    expect(wrapper.get('[data-test="payment-result-order-number"]').classes()).toContain('break-all')
    await wrapper.get('[data-test="copy-payment-result-order"]').trigger('click')
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith(orderNumber)

    wrapper.unmount()
    if (clipboardDescriptor) Object.defineProperty(navigator, 'clipboard', clipboardDescriptor)
    else delete (navigator as Navigator & { clipboard?: Clipboard }).clipboard
  })

  it('shows only the trusted CNY payment amount for a USD-priced subscription', async () => {
    pollOrderStatus.mockResolvedValue({
      ...orderFactory('COMPLETED'),
      amount: 12,
      pay_amount: 88,
      currency: 'CNY',
      order_type: 'subscription',
    })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: '',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'subscription',
      },
      global: { stubs: { Icon: true } },
    })
    await flushPromises()

    expect(wrapper.text()).toContain(formatPaymentAmount(88, 'CNY'))
    expect(wrapper.text()).not.toContain(formatPaymentAmount(12, 'CNY'))
    expect(wrapper.text()).not.toContain('payment.orders.baseAmount')
  })

  it('keeps the internal-credit marker only for a completed balance order', async () => {
    pollOrderStatus.mockResolvedValue({
      ...orderFactory('COMPLETED'),
      amount: 100,
      pay_amount: 108,
      currency: 'CNY',
      order_type: 'balance',
    })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: '',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'reset_card',
      },
      global: { stubs: { Icon: true } },
    })
    await flushPromises()

    expect(wrapper.text()).toContain('$100.00')
    expect(wrapper.text()).toContain(formatPaymentAmount(108, 'CNY'))
    wrapper.unmount()
  })

  it.each(['', 'not-a-date', '2026-09-19T00:00:00.000Z'])('checks an unavailable or elapsed deadline instead of rendering a %s countdown spinner', async (expiresAt) => {
    vi.setSystemTime(new Date('2026-09-19T00:01:00.000Z'))
    pollOrderStatus.mockResolvedValue(orderFactory('PENDING'))

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: '',
        expiresAt,
        paymentType: 'alipay',
        orderType: 'balance',
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()

    expect(wrapper.get('[data-test="payment-expiry-checking"]').exists()).toBe(true)
    expect(wrapper.findAll('button').some(button => button.text() === 'payment.qr.cancelOrder')).toBe(false)
    expect(wrapper.text()).not.toContain('NaN')
    expect(wrapper.emitted('settled')).toBeUndefined()
    wrapper.unmount()
  })

  it('closes the cashier immediately while cancellation runs in the background', async () => {
    const cancellationRequest = deferred<void>()
    pollOrderStatus.mockResolvedValue(orderFactory('PENDING'))
    cancelOrder.mockReturnValue(cancellationRequest.promise)

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://qr.alipay.com/qr-42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()
    const cancelButton = wrapper.findAll('button').find(button => button.text() === 'payment.qr.cancelOrder')
    await cancelButton?.trigger('click')
    await flushPromises()

    expect(cancelOrder).toHaveBeenCalledWith(42)
    expect(wrapper.emitted('settled')).toEqual([['cancelled']])
    expect(wrapper.emitted('done')).toEqual([[]])
    expect(wrapper.find('[data-test="payment-cancellation-pending"]').exists()).toBe(false)
    expect(wrapper.find('canvas').exists()).toBe(false)
    expect(window.localStorage.getItem(PAYMENT_CANCELLATION_STORAGE_KEY)).toBe('[42]')
    cancellationRequest.reject(new Error('background cancellation failure'))
    await flushPromises()
    expect(showError).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('keeps a recovered confirmation-pending order out of the old QR and popup controls', async () => {
    pollOrderStatus.mockResolvedValue(orderFactory('PENDING'))

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://stale.example.invalid/qr/42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        payUrl: 'https://pay.totools.cn/checkout/stale-42',
        initialConfirmationPending: true,
        orderType: 'balance',
      },
      global: { stubs: { Icon: true } },
    })
    await flushPromises()

    expect(wrapper.get('[data-test="payment-confirmation-pending"]').exists()).toBe(true)
    expect(wrapper.find('canvas').exists()).toBe(false)
    expect(wrapper.findAll('button').some(button => button.text() === 'payment.qr.openPayWindow')).toBe(false)
    wrapper.unmount()
  })

  it.each([
    ['PAYMENT_CANCELLATION_PENDING', '[data-test="payment-cancellation-pending"]'],
    ['PAYMENT_CONFIRMATION_PENDING', '[data-test="payment-confirmation-pending"]'],
  ])('never opens a cached checkout URL when resume returns %s', async (reason, pendingSelector) => {
    pollOrderStatus.mockResolvedValue(orderFactory('PENDING'))
    resumeOrder.mockRejectedValue({ reason })
    const close = vi.fn()
    const open = vi.spyOn(window, 'open').mockReturnValue({ closed: false, close } as unknown as Window)

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: '',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        payUrl: 'https://pay.totools.cn/checkout/stale-42',
        orderType: 'balance',
      },
      global: { stubs: { Icon: true } },
    })
    await flushPromises()

    const reopen = wrapper.findAll('button').find(button => button.text() === 'payment.qr.openPayWindow')
    await reopen?.trigger('click')
    await flushPromises()

    expect(resumeOrder).toHaveBeenCalledWith(42)
    expect(open).toHaveBeenCalledWith('', 'paymentPopup', expect.any(String))
    expect(open).not.toHaveBeenCalledWith('https://pay.totools.cn/checkout/stale-42', expect.anything(), expect.anything())
    expect(wrapper.get(pendingSelector).exists()).toBe(true)
    expect(close).toHaveBeenCalledTimes(1)
    open.mockRestore()
    wrapper.unmount()
  })

  it('opens only the fresh same-order resume URL after validating the response', async () => {
    pollOrderStatus.mockResolvedValue(orderFactory('PENDING'))
    resumeOrder.mockResolvedValue({
      data: {
        order_id: 42,
        status: 'PENDING',
        amount: 88,
        pay_amount: 88,
        fee_rate: 0,
        expires_at: '2099-01-01T12:30:00Z',
        payment_type: 'alipay',
        pay_url: 'https://pay.totools.cn/checkout/fresh-42',
      },
    })
    const popup = { closed: false, close: vi.fn(), location: { href: '' } }
    const open = vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: '',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        payUrl: 'https://pay.totools.cn/checkout/stale-42',
        orderType: 'balance',
      },
      global: { stubs: { Icon: true } },
    })
    await flushPromises()

    const reopen = wrapper.findAll('button').find(button => button.text() === 'payment.qr.openPayWindow')
    await reopen?.trigger('click')
    await flushPromises()

    expect(resumeOrder).toHaveBeenCalledWith(42)
    expect(popup.location.href).toBe('https://pay.totools.cn/checkout/fresh-42')
    expect(popup.close).not.toHaveBeenCalled()
    expect(open).not.toHaveBeenCalledWith('https://pay.totools.cn/checkout/stale-42', expect.anything(), expect.anything())
    open.mockRestore()
    wrapper.unmount()
  })

  it.each(['ok', 'cancel'])('invokes recovered WeChat JSAPI %s without treating its callback as payment proof', async (result) => {
    const originalBridge = Object.getOwnPropertyDescriptor(window, 'WeixinJSBridge')
    const invoke = vi.fn((_action, _payload, callback: (result: Record<string, unknown>) => void) => {
      callback({ err_msg: `get_brand_wcpay_request:${result}` })
    })
    Object.defineProperty(window, 'WeixinJSBridge', { configurable: true, value: { invoke } })
    pollOrderStatus.mockResolvedValue(orderFactory('PENDING'))

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: '',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'wxpay',
        orderType: 'balance',
        wechatJsapi: {
          appId: 'wx-preview',
          timeStamp: '1712345678',
          nonceStr: 'nonce-preview',
          package: 'prepay_id=preview',
          signType: 'RSA',
          paySign: 'signed-preview',
        },
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()

    expect(invoke).toHaveBeenCalledWith(
      'getBrandWCPayRequest',
      expect.objectContaining({ package: 'prepay_id=preview' }),
      expect.any(Function),
    )
    expect(wrapper.emitted('success')).toBeUndefined()
    expect(wrapper.emitted('settled')).toBeUndefined()
    if (result === 'cancel') {
      expect(showInfo).toHaveBeenCalledWith('payment.qr.paymentSheetDismissed')
      expect(showInfo).not.toHaveBeenCalledWith('payment.qr.cancelled')
    }
    wrapper.unmount()
    if (originalBridge) Object.defineProperty(window, 'WeixinJSBridge', originalBridge)
    else delete (window as Window & { WeixinJSBridge?: unknown }).WeixinJSBridge
  })

  it('immediately settles a trusted status-only replay without QR or hosted launch material', async () => {
    pollOrderStatus.mockResolvedValue({
      ...orderFactory('COMPLETED'),
      id: 902,
      order_type: 'reset_card',
    })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 902,
        qrCode: '',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'wxpay',
        orderType: 'reset_card',
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()

    expect(pollOrderStatus).toHaveBeenCalledWith(902)
    expect(wrapper.emitted('success')).toHaveLength(1)
    expect(wrapper.emitted('settled')).toEqual([['success']])
    expect(wrapper.text()).toContain('payment.result.success')
  })

  it('does not turn a paid fulfillment failure into an unpaid expiry', async () => {
    pollOrderStatus.mockResolvedValue({
      ...orderFactory('FAILED'),
      paid_at: '2026-04-20T12:01:00Z',
      payment_status: 'PAID',
      fulfillment_status: 'FAILED',
    })
    const wrapper = mount(PaymentStatusPanel, {
      props: { orderId: 42, qrCode: 'https://qr.alipay.com/qr-42', expiresAt: '2099-01-01T12:30:00Z', paymentType: 'alipay', orderType: 'balance' },
      global: { stubs: { Icon: true } },
    })
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()
    expect(wrapper.text()).not.toContain('payment.qr.expired')
    expect(wrapper.emitted('settled')).toBeUndefined()
  })

  it('queries once more at countdown zero and keeps a paid order recoverable', async () => {
    vi.setSystemTime(new Date('2026-09-13T00:00:00.000Z'))
    pollOrderStatus
      .mockResolvedValueOnce(orderFactory('PENDING'))
      .mockResolvedValueOnce({
        ...orderFactory('PAID'),
        paid_at: '2026-09-13T00:00:01.000Z',
        payment_status: 'PAID',
        fulfillment_status: 'PENDING',
      })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
        expiresAt: '2026-09-13T00:00:01.000Z',
        paymentType: 'card',
        orderType: 'balance',
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(1000)
    await flushPromises()

    expect(pollOrderStatus).toHaveBeenCalledTimes(2)
    expect(wrapper.emitted('settled')).toBeUndefined()
    expect(wrapper.emitted('success')).toBeUndefined()
    expect(wrapper.text()).toContain('payment.result.paymentReceivedProcessing')
  })

  it('settles expired only after the final server query explicitly reports EXPIRED', async () => {
    vi.setSystemTime(new Date('2026-09-13T00:00:00.000Z'))
    pollOrderStatus
      .mockResolvedValueOnce(orderFactory('PENDING'))
      .mockResolvedValueOnce(orderFactory('EXPIRED'))

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
        expiresAt: '2026-09-13T00:00:01.000Z',
        paymentType: 'card',
        orderType: 'balance',
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()
    expect(wrapper.emitted('settled')).toBeUndefined()

    await vi.advanceTimersByTimeAsync(1000)
    await flushPromises()

    expect(pollOrderStatus).toHaveBeenCalledTimes(2)
    expect(wrapper.emitted('settled')).toEqual([['expired']])
    expect(wrapper.text()).toContain('payment.qr.expired')
  })

  it('shows reopen button in QR mode and replaces its URL through authenticated resume', async () => {
    resumeOrder.mockResolvedValue({
      data: {
        order_id: 42,
        status: 'PENDING',
        amount: 88,
        pay_amount: 88,
        fee_rate: 0,
        expires_at: '2099-01-01T12:30:00Z',
        payment_type: 'alipay',
        payment_mode: 'qrcode',
        qr_code: 'https://qr.alipay.com/42-fresh',
        pay_url: 'https://pay.totools.cn/checkout/42-fresh',
      },
    })
    const popup = { closed: false, close: vi.fn(), location: { href: '' } }
    const openSpy = vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://qr.alipay.com/42',
        payUrl: 'https://pay.totools.cn/checkout/42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()
    expect(wrapper.text()).toContain('payment.qr.openPayWindow')

    await wrapper.get('button.btn.btn-secondary.text-sm').trigger('click')
    await flushPromises()
    expect(resumeOrder).toHaveBeenCalledWith(42)
    expect(openSpy).toHaveBeenCalledWith('', 'paymentPopup', expect.any(String))
    expect(popup.location.href).toBe('https://pay.totools.cn/checkout/42-fresh')

    openSpy.mockRestore()
  })

  it('uses generic QR copy for custom methods that contain built-in names', async () => {
    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'card_alipay',
        orderType: 'balance',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('payment.qr.scanToPay')
    expect(wrapper.text()).not.toContain('payment.qr.scanAlipay')
  })

  it('actively verifies a stuck pending order and settles it when upstream confirms payment', async () => {
    pollOrderStatus.mockResolvedValue(orderFactory('PENDING'))
    verifyOrder.mockResolvedValue({
      data: orderFactory('COMPLETED'),
    })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'wxpay',
        orderType: 'balance',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()

    expect(pollOrderStatus).toHaveBeenCalledWith(42)
    expect(verifyOrder).toHaveBeenCalledWith('sub2_20260420abcd1234')
    expect(wrapper.text()).toContain('payment.result.success')
    expect(wrapper.emitted('success')).toHaveLength(1)
  })

  it('actively verifies a pending mobile Alipay precreate order', async () => {
    const originalLocation = window.location
    const originalHidden = Object.getOwnPropertyDescriptor(document, 'hidden')
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { assign: vi.fn() },
    })
    Object.defineProperty(document, 'hidden', {
      configurable: true,
      get: () => false,
    })
    pollOrderStatus.mockResolvedValue(orderFactory('PENDING'))
    verifyOrder.mockResolvedValue({ data: orderFactory('COMPLETED') })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        amount: 88,
        payAmount: 88,
        qrCode: 'https://qr.alipay.com/dynamic-order-42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
        outTradeNo: 'sub2_20260420abcd1234',
        mobileAlipayDeepLink: true,
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()

    expect(verifyOrder).toHaveBeenCalledWith('sub2_20260420abcd1234')
    expect(wrapper.emitted('success')).toHaveLength(1)

    wrapper.unmount()
    Object.defineProperty(window, 'location', { configurable: true, value: originalLocation })
    if (originalHidden) Object.defineProperty(document, 'hidden', originalHidden)
  })

  it('actively verifies a pending desktop Alipay order', async () => {
    pollOrderStatus.mockResolvedValue(orderFactory('PENDING'))
    verifyOrder.mockResolvedValue({ data: orderFactory('COMPLETED') })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        amount: 88,
        payAmount: 88,
        qrCode: 'https://qr.alipay.com/desktop-order-42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
        outTradeNo: 'sub2_20260420abcd1234',
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()

    expect(verifyOrder).toHaveBeenCalledWith('sub2_20260420abcd1234')
    expect(wrapper.emitted('success')).toHaveLength(1)

    wrapper.unmount()
  })

  it('keeps the QR fallback hidden until the Alipay app launch times out', async () => {
    const originalLocation = window.location
    const originalHidden = Object.getOwnPropertyDescriptor(document, 'hidden')
    const assign = vi.fn()
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { assign },
    })
    Object.defineProperty(document, 'hidden', {
      configurable: true,
      get: () => false,
    })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        amount: 88,
        payAmount: 88,
        qrCode: 'https://qr.alipay.com/dynamic-order-42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
        outTradeNo: 'sub2_20260420abcd1234',
        mobileAlipayDeepLink: true,
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()
    expect(assign).toHaveBeenCalledWith(expect.stringContaining('alipays://platformapi/startapp?saId=10000007&qrcode='))
    expect(wrapper.find('[data-test="alipay-qr-fallback"]').exists()).toBe(false)

    await vi.advanceTimersByTimeAsync(2200)
    await flushPromises()

    expect(wrapper.find('[data-test="alipay-qr-fallback"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('payment.qr.saveQRCode')
    expect(wrapper.text()).toContain('sub2_20260420abcd1234')
    expect(toCanvas).toHaveBeenCalledWith(expect.any(HTMLCanvasElement), 'https://qr.alipay.com/dynamic-order-42', expect.any(Object))

    wrapper.unmount()
    Object.defineProperty(window, 'location', { configurable: true, value: originalLocation })
    if (originalHidden) Object.defineProperty(document, 'hidden', originalHidden)
  })

  it('does not show the QR fallback after the page enters the background', async () => {
    const originalLocation = window.location
    const originalHidden = Object.getOwnPropertyDescriptor(document, 'hidden')
    let hidden = false
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { assign: vi.fn() },
    })
    Object.defineProperty(document, 'hidden', {
      configurable: true,
      get: () => hidden,
    })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        amount: 88,
        payAmount: 88,
        qrCode: 'https://qr.alipay.com/dynamic-order-42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
        outTradeNo: 'sub2_20260420abcd1234',
        mobileAlipayDeepLink: true,
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()
    hidden = true
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(2200)
    await flushPromises()

    expect(wrapper.find('[data-test="alipay-qr-fallback"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('payment.qr.alipayContinueInApp')

    wrapper.unmount()
    Object.defineProperty(window, 'location', { configurable: true, value: originalLocation })
    if (originalHidden) Object.defineProperty(document, 'hidden', originalHidden)
  })

  it('resets a keep-mounted JSAPI shell into its QR fallback without allowing the old poll to settle it', async () => {
    const stalePoll = deferred<ReturnType<typeof orderFactory>>()
    pollOrderStatus.mockImplementation((orderId: number) => {
      if (orderId === 101) return stalePoll.promise
      return Promise.resolve({ ...orderFactory('PENDING'), id: orderId, out_trade_no: 'sub2_qr_102' })
    })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 101,
        qrCode: '',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'wxpay',
        orderType: 'balance',
      },
      global: { stubs: { Icon: true } },
    })
    await flushPromises()

    await wrapper.setProps({
      orderId: 102,
      qrCode: 'weixin://wxpay/bizpayurl?pr=qr-fallback',
      outTradeNo: 'sub2_qr_102',
    })
    await flushPromises()

    expect(pollOrderStatus).toHaveBeenCalledWith(102)
    expect(wrapper.text()).toContain('payment.qr.scanWxpay')

    stalePoll.resolve({ ...orderFactory('COMPLETED'), id: 101, out_trade_no: 'sub2_jsapi_101' })
    await flushPromises()

    expect(wrapper.emitted('success')).toBeUndefined()
    expect(wrapper.emitted('settled')).toBeUndefined()
    expect(wrapper.text()).toContain('payment.qr.scanWxpay')
  })

  it('ignores a deferred provider verification after the panel changes checkout sessions', async () => {
    const staleVerify = deferred<{ data: ReturnType<typeof orderFactory> }>()
    pollOrderStatus.mockImplementation((orderId: number) => Promise.resolve({
      ...orderFactory('PENDING'),
      id: orderId,
      out_trade_no: orderId === 42 ? 'sub2_old_42' : 'sub2_new_43',
    }))
    verifyOrder.mockImplementation((outTradeNo: string) => {
      if (outTradeNo === 'sub2_old_42') return staleVerify.promise
      return Promise.resolve({ data: { ...orderFactory('PENDING'), id: 43, out_trade_no: 'sub2_new_43' } })
    })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'weixin://wxpay/bizpayurl?pr=old',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'wxpay',
        orderType: 'balance',
      },
      global: { stubs: { Icon: true } },
    })
    await flushPromises()
    expect(verifyOrder).toHaveBeenCalledWith('sub2_old_42')

    await wrapper.setProps({
      orderId: 43,
      qrCode: 'weixin://wxpay/bizpayurl?pr=new',
      outTradeNo: 'sub2_new_43',
    })
    await flushPromises()

    staleVerify.resolve({ data: { ...orderFactory('COMPLETED'), id: 42, out_trade_no: 'sub2_old_42' } })
    await flushPromises()

    expect(wrapper.emitted('success')).toBeUndefined()
    expect(wrapper.emitted('settled')).toBeUndefined()
    expect(wrapper.text()).toContain('payment.qr.scanWxpay')
  })
})
