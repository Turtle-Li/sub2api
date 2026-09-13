import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AdminOrdersView from '../orders/AdminOrdersView.vue'

const {
  getOrders,
  getOrder,
  getRefundReview,
  refundOrder,
  queryRefund,
  showSuccess,
  showWarning,
  showError,
  stepUpRun,
  isStepUpCancelled,
} = vi.hoisted(() => ({
  getOrders: vi.fn(),
  getOrder: vi.fn(),
  getRefundReview: vi.fn(),
  refundOrder: vi.fn(),
  queryRefund: vi.fn(),
  showSuccess: vi.fn(),
  showWarning: vi.fn(),
  showError: vi.fn(),
  stepUpRun: vi.fn(),
  isStepUpCancelled: vi.fn(),
}))

vi.mock('@/api/admin/payment', () => ({
  adminPaymentAPI: { getOrders, getOrder, getRefundReview, refundOrder, queryRefund },
  default: { getOrders, getOrder, getRefundReview, refundOrder, queryRefund }
}))
vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }), RouterLink: { template: '<a><slot /></a>' } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess, showWarning, showError }) }))
vi.mock('@/composables/useStepUp', () => ({
  useStepUp: () => ({ run: stepUpRun }),
  isStepUpCancelled,
}))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))

const refundPayload = { reason: 'Requested refund' }
const manualWarning = 'refund succeeded; remaining balance recovery requires manual review'

function order(id: number, status: string) {
  return {
    id,
    status,
    out_trade_no: `order-${id}`,
    amount: 5,
    pay_amount: 5,
    currency: 'USD',
    payment_type: 'alipay',
    fee_rate: 0,
    created_at: '2026-09-09T00:00:00Z',
    expires_at: '2026-09-09T01:00:00Z',
  }
}

function review(id: number, quoteRevision = `quote-${id}`) {
  return {
    order_id: id,
    order_type: 'balance',
    currency: 'CNY',
    can_refund: true,
    requires_manual_review: false,
    quote_revision: quoteRevision,
    generated_at: '2026-09-14T00:00:00Z',
    default_refund_amount: 5,
    max_refund_amount: 5,
    entitlement_amount: 5,
    balance: {
      original_paid_credit: 5,
      original_gift_credit: 0,
      remaining_paid_credit: 5,
      available_balance: 5,
      available_paid_credit: 5,
      available_gift_credit: 0,
      paid_credit_to_reclaim: 5,
      gift_credit_to_reclaim: 0,
    },
  }
}

async function mountOrders(statusOrRows: string | ReturnType<typeof order>[]) {
  const rows = typeof statusOrRows === 'string' ? [order(42, statusOrRows)] : statusOrRows
  getOrders.mockResolvedValue({ data: { items: rows, total: rows.length } })
  getOrder.mockImplementation((id: number) => Promise.resolve({ data: { order: rows.find((row) => row.id === id), audit_logs: [] } }))
  const wrapper = mount(AdminOrdersView, {
    global: {
      stubs: {
        AppLayout: { template: '<main><slot /></main>' },
        OrderTable: {
          props: ['orders'],
          template: '<div><div v-for="row in orders" :key="row.id" :data-order-id="row.id"><slot name="actions" :row="row" /></div></div>'
        },
        AdminRefundDialog: {
          name: 'AdminRefundDialog',
          props: ['show', 'order', 'review', 'loading', 'error', 'warning', 'submitting'],
          emits: ['confirm', 'cancel'],
          template: '<div v-if="show" data-test="refund-dialog" :data-order-id="order?.id" :data-review="review?.quote_revision || \'\'">{{ warning }}{{ error }}</div>'
        },
        BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' },
        Select: true,
        Pagination: true,
        Icon: true,
        OrderStatusBadge: true,
        AdminPaymentOwnerTest: {
          emits: ['created'],
          template: '<button data-test="owner-payment-created" @click="$emit(\'created\')" />',
        },
        TotpStepUpDialog: true,
      }
    }
  })
  await flushPromises()
  return wrapper
}

function buttonForOrder(wrapper: ReturnType<typeof mount>, orderID: number, label: string) {
  const row = wrapper.find(`[data-order-id="${orderID}"]`)
  return row.findAll('button').find((button) => button.text() === label)!
}

async function openRefundDialog(wrapper: ReturnType<typeof mount>, orderID = 42) {
  await buttonForOrder(wrapper, orderID, 'payment.admin.refund').trigger('click')
  await flushPromises()
}

describe('admin refund outcome feedback', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    stepUpRun.mockImplementation((action: () => Promise<unknown>) => action())
    isStepUpCancelled.mockReturnValue(false)
    getRefundReview.mockImplementation((id: number) => Promise.resolve({ data: review(id) }))
  })

  it.each(['submit', 'query'])('keeps a successful refund manual-review warning visible after %s', async (action) => {
    refundOrder.mockResolvedValue({ data: { success: true, warning: manualWarning } })
    queryRefund.mockResolvedValue({ data: { success: true, warning: manualWarning } })
    const wrapper = await mountOrders(action === 'submit' ? 'COMPLETED' : 'REFUND_PENDING')
    const buttonText = action === 'submit' ? 'payment.admin.refund' : 'payment.admin.queryRefundStatus'
    await buttonForOrder(wrapper, 42, buttonText).trigger('click')
    if (action === 'submit') {
      await flushPromises()
      wrapper.findComponent({ name: 'AdminRefundDialog' }).vm.$emit('confirm', refundPayload)
    }
    await flushPromises()

    expect(stepUpRun).toHaveBeenCalledTimes(1)
    expect(showWarning).toHaveBeenCalledWith(manualWarning)
    expect(showSuccess).not.toHaveBeenCalled()
    expect(showError).not.toHaveBeenCalled()
    expect(getOrders).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-test="refund-dialog"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('reports acceptance as pending and closes the submitted refund dialog', async () => {
    refundOrder.mockResolvedValue({ data: { success: false, warning: 'unified payment refund is pending confirmation' } })
    const wrapper = await mountOrders('COMPLETED')
    await openRefundDialog(wrapper)
    wrapper.findComponent({ name: 'AdminRefundDialog' }).vm.$emit('confirm', refundPayload)
    await flushPromises()

    expect(refundOrder).toHaveBeenCalledWith(42, { quote_revision: 'quote-42', reason: 'Requested refund' })
    expect(showSuccess).toHaveBeenCalledWith('payment.admin.refundPending')
    expect(showWarning).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="refund-dialog"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('retries the original refund snapshot after MFA while blocking concurrent refund actions', async () => {
    const rows = [order(42, 'COMPLETED'), order(99, 'REFUND_PENDING')]
    const initialPayload = { quote_revision: 'quote-42', reason: 'Requested refund' }
    let resumeMFA: (() => void) | undefined
    stepUpRun.mockImplementation(async (action: () => Promise<unknown>) => {
      try {
        return await action()
      } catch (error) {
        if ((error as { code?: string }).code !== 'STEP_UP_REQUIRED') throw error
        await new Promise<void>((resolve) => { resumeMFA = resolve })
        return action()
      }
    })
    refundOrder
      .mockRejectedValueOnce({ status: 403, code: 'STEP_UP_REQUIRED' })
      .mockResolvedValueOnce({ data: { success: true, warning: manualWarning } })
    const wrapper = await mountOrders(rows)
    await openRefundDialog(wrapper)
    const suppliedPayload = { ...refundPayload }
    wrapper.findComponent({ name: 'AdminRefundDialog' }).vm.$emit('confirm', suppliedPayload)
    await flushPromises()

    expect(refundOrder).toHaveBeenCalledTimes(1)
    expect(buttonForOrder(wrapper, 99, 'payment.admin.queryRefundStatus').attributes('disabled')).toBeDefined()
    await buttonForOrder(wrapper, 99, 'payment.admin.queryRefundStatus').trigger('click')
    expect(queryRefund).not.toHaveBeenCalled()

    await buttonForOrder(wrapper, 99, 'common.view').trigger('click')
    await flushPromises()
    suppliedPayload.reason = 'different order selection must not alter retry'
    resumeMFA!()
    await flushPromises()

    expect(refundOrder).toHaveBeenCalledTimes(2)
    expect(refundOrder).toHaveBeenNthCalledWith(1, 42, initialPayload)
    expect(refundOrder).toHaveBeenNthCalledWith(2, 42, initialPayload)
    expect(refundOrder.mock.calls[0][1]).not.toBe(suppliedPayload)
    expect(showWarning).toHaveBeenCalledWith(manualWarning)
    expect(wrapper.find('[data-test="refund-dialog"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('silently leaves a refund dialog open when the MFA prompt is cancelled', async () => {
    const cancelled = new Error('step-up cancelled')
    stepUpRun.mockImplementation(async (action: () => Promise<unknown>) => {
      try {
        await action()
      } catch {
        throw cancelled
      }
      throw new Error('expected an MFA challenge')
    })
    isStepUpCancelled.mockImplementation((error: unknown) => error === cancelled)
    refundOrder.mockRejectedValue({ status: 403, code: 'STEP_UP_REQUIRED' })
    const wrapper = await mountOrders('COMPLETED')
    await openRefundDialog(wrapper)
    wrapper.findComponent({ name: 'AdminRefundDialog' }).vm.$emit('confirm', { ...refundPayload })
    await flushPromises()

    expect(refundOrder).toHaveBeenCalledTimes(1)
    expect(showSuccess).not.toHaveBeenCalled()
    expect(showWarning).not.toHaveBeenCalled()
    expect(showError).not.toHaveBeenCalled()
    expect(getOrders).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-test="refund-dialog"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('keeps the latest refund review when an earlier dialog request resolves late', async () => {
    let resolveFirst: ((value: { data: ReturnType<typeof review> }) => void) | undefined
    let resolveSecond: ((value: { data: ReturnType<typeof review> }) => void) | undefined
    const first = new Promise<{ data: ReturnType<typeof review> }>((resolve) => { resolveFirst = resolve })
    const second = new Promise<{ data: ReturnType<typeof review> }>((resolve) => { resolveSecond = resolve })
    getRefundReview.mockImplementation((id: number) => id === 42 ? first : second)

    const wrapper = await mountOrders([order(42, 'COMPLETED'), order(99, 'COMPLETED')])
    await buttonForOrder(wrapper, 42, 'payment.admin.refund').trigger('click')
    await flushPromises()
    await buttonForOrder(wrapper, 99, 'payment.admin.refund').trigger('click')
    await flushPromises()

    resolveFirst!({ data: review(42, 'quote-old') })
    await flushPromises()
    expect(wrapper.find('[data-test="refund-dialog"]').attributes('data-order-id')).toBe('99')
    expect(wrapper.find('[data-test="refund-dialog"]').attributes('data-review')).toBe('')

    resolveSecond!({ data: review(99, 'quote-current') })
    await flushPromises()
    expect(wrapper.find('[data-test="refund-dialog"]').attributes('data-review')).toBe('quote-current')
    wrapper.unmount()
  })

  it('keeps the dialog disabled when loading the refund review fails', async () => {
    getRefundReview.mockRejectedValue({ code: 'REFUND_REVIEW_UNAVAILABLE', message: 'review unavailable' })
    const wrapper = await mountOrders('COMPLETED')
    await openRefundDialog(wrapper)

    expect(wrapper.find('[data-test="refund-dialog"]').text()).toContain('review unavailable')
    wrapper.findComponent({ name: 'AdminRefundDialog' }).vm.$emit('confirm', refundPayload)
    await flushPromises()
    expect(refundOrder).not.toHaveBeenCalled()
    expect(showError).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('refreshes a stale quote and will not retry the previous revision', async () => {
    let resolveRefresh: ((value: { data: ReturnType<typeof review> }) => void) | undefined
    const refreshed = new Promise<{ data: ReturnType<typeof review> }>((resolve) => { resolveRefresh = resolve })
    getRefundReview
      .mockResolvedValueOnce({ data: review(42, 'quote-old') })
      .mockImplementationOnce(() => refreshed)
    refundOrder.mockRejectedValueOnce({ status: 409, code: 'REFUND_QUOTE_STALE' })

    const wrapper = await mountOrders('COMPLETED')
    await openRefundDialog(wrapper)
    wrapper.findComponent({ name: 'AdminRefundDialog' }).vm.$emit('confirm', refundPayload)
    await flushPromises()

    expect(refundOrder).toHaveBeenCalledWith(42, { quote_revision: 'quote-old', reason: 'Requested refund' })
    expect(getRefundReview).toHaveBeenCalledTimes(2)
    expect(showWarning).toHaveBeenCalledWith('payment.admin.refundQuoteStale')

    wrapper.findComponent({ name: 'AdminRefundDialog' }).vm.$emit('confirm', refundPayload)
    await flushPromises()
    expect(refundOrder).toHaveBeenCalledTimes(1)

    resolveRefresh!({ data: review(42, 'quote-fresh') })
    await flushPromises()
    expect(wrapper.find('[data-test="refund-dialog"]').attributes('data-review')).toBe('quote-fresh')
    wrapper.unmount()
  })

  it('reloads orders after the owner payment test creates an order', async () => {
    const wrapper = await mountOrders('PENDING')
    expect(getOrders).toHaveBeenCalledTimes(1)

    await wrapper.get('[data-test="owner-payment-created"]').trigger('click')
    await flushPromises()

    expect(getOrders).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })
})
