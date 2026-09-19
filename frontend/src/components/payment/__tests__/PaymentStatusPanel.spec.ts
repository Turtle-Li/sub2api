import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const pollOrderStatus = vi.hoisted(() => vi.fn())
const cancelOrder = vi.hoisted(() => vi.fn())
const verifyOrder = vi.hoisted(() => vi.fn())
const resumeOrder = vi.hoisted(() => vi.fn())
const showError = vi.hoisted(() => vi.fn())
const showInfo = vi.hoisted(() => vi.fn())
const toCanvas = vi.hoisted(() => vi.fn())

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

import PaymentStatusPanel from '../PaymentStatusPanel.vue'

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
    pollOrderStatus.mockReset()
    cancelOrder.mockReset()
    verifyOrder.mockReset()
    resumeOrder.mockReset()
    showError.mockReset()
    showInfo.mockReset()
    toCanvas.mockReset().mockResolvedValue(undefined)
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('keeps RECHARGING in the payment processing state', async () => {
    pollOrderStatus.mockResolvedValue(orderFactory('RECHARGING'))

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
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

  it('locks the cashier into a cancellation-pending state after central close acceptance', async () => {
    let cancellationPending = false
    pollOrderStatus.mockImplementation(() => Promise.resolve({
      ...orderFactory('PENDING'),
      cancellation_pending: cancellationPending,
    }))
    cancelOrder.mockImplementation(async () => {
      cancellationPending = true
      throw { reason: 'PAYMENT_CANCELLATION_PENDING' }
    })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
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

    expect(wrapper.get('[data-test="payment-cancellation-pending"]').exists()).toBe(true)
    expect(wrapper.find('canvas').exists()).toBe(false)
    expect(showError).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('keeps polling instead of reporting a terminal outcome while confirmation is pending', async () => {
    pollOrderStatus.mockResolvedValue(orderFactory('PENDING'))
    cancelOrder.mockRejectedValue({ reason: 'PAYMENT_CONFIRMATION_PENDING' })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
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

    expect(wrapper.get('[data-test="payment-confirmation-pending"]').exists()).toBe(true)
    expect(wrapper.emitted('settled')).toBeUndefined()
    expect(wrapper.emitted('success')).toBeUndefined()
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
        payUrl: 'https://stale.example.invalid/checkout/42',
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
        payUrl: 'https://stale.example.invalid/checkout/42',
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
    expect(open).not.toHaveBeenCalledWith('https://stale.example.invalid/checkout/42', expect.anything(), expect.anything())
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
        pay_url: 'https://fresh.example.invalid/checkout/42',
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
        payUrl: 'https://stale.example.invalid/checkout/42',
        orderType: 'balance',
      },
      global: { stubs: { Icon: true } },
    })
    await flushPromises()

    const reopen = wrapper.findAll('button').find(button => button.text() === 'payment.qr.openPayWindow')
    await reopen?.trigger('click')
    await flushPromises()

    expect(resumeOrder).toHaveBeenCalledWith(42)
    expect(popup.location.href).toBe('https://fresh.example.invalid/checkout/42')
    expect(popup.close).not.toHaveBeenCalled()
    expect(open).not.toHaveBeenCalledWith('https://stale.example.invalid/checkout/42', expect.anything(), expect.anything())
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
      props: { orderId: 42, qrCode: 'https://pay.example.com/qr/42', expiresAt: '2099-01-01T12:30:00Z', paymentType: 'alipay', orderType: 'balance' },
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
        qr_code: 'https://pay.example.com/qr/42-fresh',
        pay_url: 'https://pay.example.com/session/42-fresh',
      },
    })
    const popup = { closed: false, close: vi.fn(), location: { href: '' } }
    const openSpy = vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
        payUrl: 'https://pay.example.com/session/42',
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
    expect(popup.location.href).toBe('https://pay.example.com/session/42-fresh')

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
