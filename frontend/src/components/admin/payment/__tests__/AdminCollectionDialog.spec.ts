import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AdminCollectionDialog from '../AdminCollectionDialog.vue'

const { createCollectionOrder, getOrder } = vi.hoisted(() => ({
  createCollectionOrder: vi.fn(),
  getOrder: vi.fn(),
}))
const toCanvas = vi.hoisted(() => vi.fn())

vi.mock('@/api/admin/payment', () => ({
  adminPaymentAPI: { createCollectionOrder, getOrder },
}))
vi.mock('qrcode', () => ({ default: { toCanvas } }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key }),
}))

const SelectStub = {
  props: ['modelValue', 'options', 'disabled'],
  emits: ['update:modelValue'],
  template: `<select :value="modelValue" :disabled="disabled" @change="$emit('update:modelValue', $event.target.value)">
    <option v-for="option in options" :key="option.value" :value="option.value">{{ option.label }}</option>
  </select>`,
}

const dialogStubs = {
  BaseDialog: { props: ['show'], template: '<section v-if="show"><slot /></section>' },
  Select: SelectStub,
  Icon: true,
}

function checkoutResult(overrides: Record<string, unknown> = {}) {
  return {
    order_id: 42,
    status: 'PENDING',
    amount: 0,
    pay_amount: 432,
    currency: 'CNY',
    payment_type: 'alipay',
    out_trade_no: 'collection-42',
    qr_code: 'https://qr.alipay.com/collection-42',
    expires_at: '2099-01-02T03:04:05.000Z',
    ...overrides,
  }
}

function collectionOrder(status: 'PENDING' | 'COMPLETED') {
  return {
    id: 42,
    user_id: 1,
    amount: 0,
    pay_amount: 432,
    currency: 'CNY',
    fee_rate: 0,
    payment_type: 'alipay',
    out_trade_no: 'collection-42',
    status,
    order_type: 'collection',
    created_at: '2099-01-01T03:04:05.000Z',
    expires_at: '2099-01-02T03:04:05.000Z',
    refund_amount: 0,
  }
}

function stepUp(run: (action: () => Promise<unknown>) => Promise<unknown> = (action) => action()) {
  return {
    visible: { value: false },
    blockedReason: { value: '' },
    prompt: vi.fn(),
    onVerified: vi.fn(),
    onCancel: vi.fn(),
    run: vi.fn(run),
  }
}

function mountDialog(controller = stepUp()) {
  return mount(AdminCollectionDialog, {
    props: { show: true, stepUp: controller },
    global: { stubs: dialogStubs },
  })
}

async function createFor(wrapper: ReturnType<typeof mount>, amount: string) {
  await wrapper.get('[data-test="collection-amount"]').setValue(amount)
  await wrapper.get('form').trigger('submit')
  await flushPromises()
}

describe('AdminCollectionDialog', () => {
  it.each(['COMPLETED', 'CANCELLED'])('does not offer payment for a replayed %s checkout', async (status) => {
    createCollectionOrder.mockResolvedValue({ data: checkoutResult({ status }) })
    const wrapper = mountDialog()
    await createFor(wrapper, '432')
    expect(wrapper.find('[data-test="collection-qr-canvas"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="download-collection-qr"]').exists()).toBe(false)
    wrapper.unmount()
  })

  beforeEach(() => {
    createCollectionOrder.mockReset().mockResolvedValue({ data: checkoutResult() })
    getOrder.mockReset()
    toCanvas.mockReset().mockResolvedValue(undefined)
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('submits an exact CNY fen amount and shows the server cash amount and expiry', async () => {
    const wrapper = mountDialog()
    await createFor(wrapper, '432')

    expect(createCollectionOrder).toHaveBeenCalledWith({ amount_fen: 43_200, payment_type: 'alipay' }, expect.stringMatching(/^admin-collection-/))
    expect(wrapper.text()).toContain('432.00')
    expect(wrapper.text()).toContain('2099')
    expect(toCanvas).toHaveBeenCalledWith(expect.any(HTMLCanvasElement), 'https://qr.alipay.com/collection-42', expect.any(Object))
    expect(wrapper.text()).toContain('payment.admin.collection.noBalanceCredit')
    wrapper.unmount()
  })

  it('retains the same idempotency key through an uncertain request retried by step-up', async () => {
    const controller = stepUp(async (action) => {
      try {
        return await action()
      } catch {
        return action()
      }
    })
    createCollectionOrder
      .mockRejectedValueOnce(new Error('connection reset'))
      .mockResolvedValueOnce({ data: checkoutResult() })
    const wrapper = mountDialog(controller)

    await createFor(wrapper, '432.01')

    expect(createCollectionOrder).toHaveBeenCalledTimes(2)
    expect(createCollectionOrder).toHaveBeenNthCalledWith(1, { amount_fen: 43_201, payment_type: 'alipay' }, expect.any(String))
    expect(createCollectionOrder.mock.calls[1][1]).toBe(createCollectionOrder.mock.calls[0][1])
    wrapper.unmount()
  })

  it('does not encode a hosted payment URL when the provider omitted its native QR payload', async () => {
    createCollectionOrder.mockResolvedValue({ data: checkoutResult({ qr_code: '', pay_url: 'https://pay.example.test/collection-42' }) })
    const wrapper = mountDialog()

    await createFor(wrapper, '432')

    expect(toCanvas).not.toHaveBeenCalled()
    expect(wrapper.get('[data-test="collection-native-qr-error"]').text()).toContain('payment.admin.collection.nativeQRCodeUnavailable')
    expect(wrapper.find('[data-test="collection-qr-canvas"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="collection-checkout-link"]').attributes('href')).toBe('https://pay.example.test/collection-42')
    wrapper.unmount()
  })

  it('checks order state only after an explicit refresh and distinguishes pending from confirmed payment', async () => {
    getOrder
      .mockResolvedValueOnce({ data: { order: collectionOrder('PENDING') } })
      .mockResolvedValueOnce({ data: { order: collectionOrder('COMPLETED') } })
    const wrapper = mountDialog()

    await createFor(wrapper, '432')
    expect(getOrder).not.toHaveBeenCalled()

    await wrapper.get('[data-test="refresh-collection-order"]').trigger('click')
    await flushPromises()
    expect(getOrder).toHaveBeenCalledWith(42)
    expect(wrapper.get('[data-test="collection-payment-state"]').text()).toContain('payment.admin.collection.paymentPending')

    await wrapper.get('[data-test="refresh-collection-order"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="collection-payment-state"]').text()).toContain('payment.admin.collection.paymentConfirmed')
    expect(wrapper.find('[data-test="collection-qr-canvas"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="download-collection-qr"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('shows render failures and disables QR download', async () => {
    toCanvas.mockRejectedValueOnce(new Error('canvas unavailable'))
    const wrapper = mountDialog()

    await createFor(wrapper, '432')

    expect(wrapper.get('[data-test="collection-qr-error"]').text()).toContain('payment.admin.collection.qrRenderFailed')
    expect(wrapper.get('[data-test="download-collection-qr"]').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it('hides collection payment actions after local expiry', async () => {
    createCollectionOrder.mockResolvedValue({ data: checkoutResult({ expires_at: '2000-01-02T03:04:05.000Z' }) })
    const wrapper = mountDialog()

    await createFor(wrapper, '432')

    expect(wrapper.get('[data-test="collection-payment-state"]').text()).toContain('payment.admin.collection.expiredLocally')
    expect(wrapper.find('[data-test="collection-qr-canvas"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="download-collection-qr"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('ignores a stale QR renderer failure after the dialog closes', async () => {
    let rejectRender: ((reason?: unknown) => void) | undefined
    toCanvas.mockReturnValueOnce(new Promise<void>((_resolve, reject) => { rejectRender = reject }))
    const wrapper = mountDialog()

    await createFor(wrapper, '432')
    await wrapper.setProps({ show: false })
    rejectRender!(new Error('canvas gone'))
    await flushPromises()
    await wrapper.setProps({ show: true })
    await flushPromises()

    expect(wrapper.find('[data-test="collection-qr-error"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
