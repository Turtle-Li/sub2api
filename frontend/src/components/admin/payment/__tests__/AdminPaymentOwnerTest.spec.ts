import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import AdminPaymentOwnerTest from '../AdminPaymentOwnerTest.vue'

const { createOwnerTestOrder, createIdempotencyKey, stepUpRun, isStepUpCancelled, toCanvas } = vi.hoisted(() => ({
  createOwnerTestOrder: vi.fn(),
  createIdempotencyKey: vi.fn(),
  stepUpRun: vi.fn(),
  isStepUpCancelled: vi.fn(),
  toCanvas: vi.fn(),
}))

vi.mock('@/api/admin/payment', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/api/admin/payment')>(),
  createOwnerTestOrder,
}))
vi.mock('@/utils/idempotency', () => ({ createIdempotencyKey }))
vi.mock('@/composables/useStepUp', () => ({
  useStepUp: () => ({ run: stepUpRun }),
  isStepUpCancelled,
}))
vi.mock('qrcode', () => ({ default: { toCanvas } }))

const createdOrder = {
  order_id: 42,
  status: 'PENDING',
  pay_url: 'https://pay.example.test/checkout/42',
  amount: 0.01,
  pay_amount: 0.01,
  fee_rate: 0,
  expires_at: '2026-09-09T01:00:00Z',
}

function render() {
  return mount(AdminPaymentOwnerTest, {
    global: {
      plugins: [createI18n({ legacy: false, locale: 'en', messages: {} })],
      stubs: { TotpStepUpDialog: true },
    },
  })
}

describe('AdminPaymentOwnerTest', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    stepUpRun.mockImplementation((action: () => Promise<unknown>) => action())
    isStepUpCancelled.mockReturnValue(false)
    createIdempotencyKey.mockImplementation((prefix: string) => `${prefix}-generated-key-000000000000`)
    toCanvas.mockResolvedValue(undefined)
  })

  it('offers both channels and both small amounts, then shows a checkout link without opening it', async () => {
    createIdempotencyKey
      .mockReturnValueOnce('owner-payment-test-alipay-key-0001')
      .mockReturnValueOnce('owner-payment-test-wxpay-key-0002')
    createOwnerTestOrder.mockResolvedValue(createdOrder)
    const popup = vi.spyOn(window, 'open')
    const wrapper = render()

    expect(wrapper.get('[data-testid="owner-payment-test-amount-1"]').attributes('aria-pressed')).toBe('true')
    await wrapper.get('[data-testid="owner-payment-test-amount-2"]').trigger('click')
    await wrapper.get('[data-testid="owner-payment-test-alipay"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="owner-payment-test-checkout"]').text()).toBe('Open checkout')
    await wrapper.get('[data-testid="owner-payment-test-amount-1"]').trigger('click')
    await wrapper.get('[data-testid="owner-payment-test-wxpay"]').trigger('click')
    await flushPromises()

    expect(createOwnerTestOrder).toHaveBeenNthCalledWith(1, { amount_fen: 2, payment_type: 'alipay' }, 'owner-payment-test-alipay-key-0001')
    expect(createOwnerTestOrder).toHaveBeenNthCalledWith(2, { amount_fen: 1, payment_type: 'wxpay' }, 'owner-payment-test-wxpay-key-0002')
    expect(wrapper.get('[data-testid="owner-payment-test-result"]').text()).toContain('#42')
    expect(wrapper.get('[data-testid="owner-payment-test-result"]').text()).toContain('PENDING')
    expect(wrapper.get('[data-testid="owner-payment-test-checkout"]').attributes('href')).toBe(createdOrder.pay_url)
    expect(wrapper.get('[data-testid="owner-payment-test-checkout"]').text()).toBe('Open checkout')
    expect(wrapper.emitted('created')).toEqual([[createdOrder], [createdOrder]])
    expect(popup).not.toHaveBeenCalled()
    expect(wrapper.text().toLowerCase()).not.toContain('paid')
    popup.mockRestore()
    wrapper.unmount()
  })

  it('retries the immutable MFA intent and idempotency key while the controls are locked', async () => {
    let resumeMFA: (() => void) | undefined
    createIdempotencyKey.mockReturnValue('owner-payment-test-mfa-key-0001')
    stepUpRun.mockImplementation(async (action: () => Promise<unknown>) => {
      try {
        return await action()
      } catch (error) {
        if ((error as { code?: string }).code !== 'STEP_UP_REQUIRED') throw error
        await new Promise<void>((resolve) => { resumeMFA = resolve })
        return action()
      }
    })
    createOwnerTestOrder
      .mockRejectedValueOnce({ status: 403, code: 'STEP_UP_REQUIRED' })
      .mockResolvedValueOnce(createdOrder)
    const wrapper = render()

    await wrapper.get('[data-testid="owner-payment-test-alipay"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="owner-payment-test-amount-2"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="owner-payment-test-wxpay"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="owner-payment-test-amount-2"]').trigger('click')
    expect(createOwnerTestOrder).toHaveBeenCalledTimes(1)

    resumeMFA!()
    await flushPromises()

    expect(createOwnerTestOrder).toHaveBeenCalledTimes(2)
    expect(createOwnerTestOrder).toHaveBeenNthCalledWith(1, { amount_fen: 1, payment_type: 'alipay' }, 'owner-payment-test-mfa-key-0001')
    expect(createOwnerTestOrder).toHaveBeenNthCalledWith(2, { amount_fen: 1, payment_type: 'alipay' }, 'owner-payment-test-mfa-key-0001')
    wrapper.unmount()
  })

  it('renders a WeChat Native response locally and does not expose its unavailable checkout URL', async () => {
    const wechatOrder = {
      ...createdOrder,
      pay_url: 'https://central.example.test/payment/checkout/42',
      qr_code: 'weixin://wxpay/bizpayurl?pr=owner-test-native',
    }
    createIdempotencyKey.mockReturnValue('owner-payment-test-wechat-qr-key-0001')
    createOwnerTestOrder.mockResolvedValue(wechatOrder)
    const wrapper = render()

    await wrapper.get('[data-testid="owner-payment-test-wxpay"]').trigger('click')
    await flushPromises()
    await flushPromises()

    expect(wrapper.find('[data-testid="owner-payment-test-qr"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="owner-payment-test-checkout"]').exists()).toBe(false)
    expect(toCanvas).toHaveBeenCalledWith(
      expect.any(HTMLCanvasElement),
      wechatOrder.qr_code,
      expect.objectContaining({ width: 192, margin: 2, errorCorrectionLevel: 'M' }),
    )
    wrapper.unmount()
  })

  it('retains an unknown intent key across direct retries and choice switches', async () => {
    createIdempotencyKey
      .mockReturnValueOnce('owner-payment-test-retry-key-0001')
      .mockReturnValueOnce('owner-payment-test-changed-key-0002')
    createOwnerTestOrder
      .mockRejectedValueOnce(new Error('network timeout'))
      .mockRejectedValueOnce(new Error('network timeout'))
      .mockResolvedValueOnce(createdOrder)
      .mockResolvedValueOnce(createdOrder)
    const wrapper = render()

    await wrapper.get('[data-testid="owner-payment-test-alipay"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('same request key')
    await wrapper.get('[data-testid="owner-payment-test-alipay"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="owner-payment-test-amount-2"]').trigger('click')
    await wrapper.get('[data-testid="owner-payment-test-wxpay"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="owner-payment-test-amount-1"]').trigger('click')
    await wrapper.get('[data-testid="owner-payment-test-alipay"]').trigger('click')
    await flushPromises()

    expect(createOwnerTestOrder).toHaveBeenNthCalledWith(1, { amount_fen: 1, payment_type: 'alipay' }, 'owner-payment-test-retry-key-0001')
    expect(createOwnerTestOrder).toHaveBeenNthCalledWith(2, { amount_fen: 1, payment_type: 'alipay' }, 'owner-payment-test-retry-key-0001')
    expect(createOwnerTestOrder).toHaveBeenNthCalledWith(3, { amount_fen: 2, payment_type: 'wxpay' }, 'owner-payment-test-changed-key-0002')
    expect(createOwnerTestOrder).toHaveBeenNthCalledWith(4, { amount_fen: 1, payment_type: 'alipay' }, 'owner-payment-test-retry-key-0001')
    wrapper.unmount()
  })

  it('does not show an error or checkout result when the MFA prompt is cancelled', async () => {
    const cancelled = new Error('step-up cancelled')
    createIdempotencyKey.mockReturnValue('owner-payment-test-cancel-key-0001')
    stepUpRun.mockImplementation(async (action: () => Promise<unknown>) => {
      try {
        await action()
      } catch {
        throw cancelled
      }
      throw new Error('expected step-up challenge')
    })
    isStepUpCancelled.mockImplementation((error: unknown) => error === cancelled)
    createOwnerTestOrder.mockRejectedValue({ status: 403, code: 'STEP_UP_REQUIRED' })
    const wrapper = render()

    await wrapper.get('[data-testid="owner-payment-test-alipay"]').trigger('click')
    await flushPromises()

    expect(createOwnerTestOrder).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="owner-payment-test-result"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="owner-payment-test-alipay"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })
})
