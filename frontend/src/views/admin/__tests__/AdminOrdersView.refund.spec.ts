import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AdminOrdersView from '../orders/AdminOrdersView.vue'

const {
  getOrders,
  getOrder,
  getRefundReview,
  backfillSubscriptionGrant,
  refundOrder,
  queryRefund,
  retryRefund,
  confirmExternalRefund,
  showSuccess,
  showWarning,
  showError,
  stepUpRun,
  isStepUpCancelled,
} = vi.hoisted(() => ({
  getOrders: vi.fn(),
  getOrder: vi.fn(),
  getRefundReview: vi.fn(),
  backfillSubscriptionGrant: vi.fn(),
  refundOrder: vi.fn(),
  queryRefund: vi.fn(),
  retryRefund: vi.fn(),
  confirmExternalRefund: vi.fn(),
  showSuccess: vi.fn(),
  showWarning: vi.fn(),
  showError: vi.fn(),
  stepUpRun: vi.fn(),
  isStepUpCancelled: vi.fn(),
}))

vi.mock('@/api/admin/payment', () => ({
  adminPaymentAPI: { getOrders, getOrder, getRefundReview, backfillSubscriptionGrant, refundOrder, queryRefund, retryRefund, confirmExternalRefund },
  default: { getOrders, getOrder, getRefundReview, backfillSubscriptionGrant, refundOrder, queryRefund, retryRefund, confirmExternalRefund }
}))
vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }), RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess, showWarning, showError }) }))
vi.mock('@/composables/useStepUp', () => ({
  useStepUp: () => ({ run: stepUpRun }),
  isStepUpCancelled,
}))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))

const refundPayload = { reason_code: 'customer_request', reason_detail: 'Requested refund' }
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
          props: ['show', 'order', 'review', 'loading', 'error', 'warning', 'submitting', 'backfilling'],
          emits: ['confirm', 'backfill', 'cancel'],
          template: '<div v-if="show" data-test="refund-dialog" :data-order-id="order?.id" :data-review="review?.quote_revision || \'\'">{{ warning }}{{ error }}</div>'
        },
        BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' },
        Select: {
          name: 'Select',
          emits: ['update:modelValue', 'change'],
          template: '<div data-test="select" />',
        },
        Pagination: true,
        Icon: true,
        OrderStatusBadge: true,
        InvoiceStatusBadge: {
          name: 'InvoiceStatusBadge',
          props: ['status'],
          template: '<span data-test="invoice-status">{{ status }}</span>',
        },
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

describe('admin order management', () => {
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

    expect(refundOrder).toHaveBeenCalledWith(42, {
      quote_revision: 'quote-42',
      reason_code: 'customer_request',
      reason_detail: 'Requested refund',
    })
    expect(showSuccess).toHaveBeenCalledWith('payment.admin.refundPending')
    expect(showWarning).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="refund-dialog"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('retries the original refund snapshot after MFA while blocking concurrent refund actions', async () => {
    const rows = [order(42, 'COMPLETED'), order(99, 'REFUND_PENDING')]
    const initialPayload = {
      quote_revision: 'quote-42',
      reason_code: 'customer_request',
      reason_detail: 'Requested refund',
    }
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
    suppliedPayload.reason_detail = 'different order selection must not alter retry'
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

    expect(refundOrder).toHaveBeenCalledWith(42, {
      quote_revision: 'quote-old',
      reason_code: 'customer_request',
      reason_detail: 'Requested refund',
    })
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

  it('runs historical subscription provenance backfill behind step-up and keeps the fresh quote open', async () => {
    const manualReview = {
      ...review(42),
      order_type: 'subscription',
      can_refund: false,
      requires_manual_review: true,
      quote_revision: undefined,
      balance: undefined,
      reason_code: 'LEGACY_SUBSCRIPTION_UNATTRIBUTED',
      subscription_backfill: {
        audit_revision: 'audit-42',
        suggested_subscription_id: 8,
        suggested_term_start_at: '2026-09-12T15:13:43Z',
        suggested_term_end_at: '2026-10-12T15:13:43Z',
        subscription_group_id: 4,
        purchased_days: 30,
        candidates: [{
          subscription_id: 8,
          starts_at: '2026-09-12T15:13:43Z',
          expires_at: '2026-10-12T15:13:43Z',
          status: 'active',
        }],
      },
    }
    const freshReview = {
      ...review(42, 'quote-after-backfill'),
      order_type: 'subscription',
      balance: undefined,
      subscription: {
        subscription_id: 8,
        term_start_at: '2026-09-12T15:13:43Z',
        term_end_at: '2026-10-12T15:13:43Z',
        current_expires_at: '2026-10-12T15:13:43Z',
        new_expires_at: '2026-09-15T00:00:00Z',
        purchased_seconds: 2_592_000,
        used_seconds: 200_000,
        remaining_seconds: 2_392_000,
      },
    }
    getRefundReview.mockResolvedValueOnce({ data: manualReview })
    backfillSubscriptionGrant.mockResolvedValueOnce({ data: freshReview })
    const wrapper = await mountOrders('COMPLETED')
    await openRefundDialog(wrapper)

    const request = {
      audit_revision: 'audit-42',
      subscription_id: 8,
      term_start_at: '2026-09-12T15:13:43.000Z',
      term_end_at: '2026-10-12T15:13:43.000Z',
      evidence_source: 'payment_audit_and_subscription',
      evidence_detail: 'Verified payment audit and current subscription',
    }
    wrapper.findComponent({ name: 'AdminRefundDialog' }).vm.$emit('backfill', request)
    await flushPromises()

    expect(stepUpRun).toHaveBeenCalledTimes(1)
    expect(backfillSubscriptionGrant).toHaveBeenCalledWith(42, request)
    expect(showSuccess).toHaveBeenCalledWith('payment.admin.subscriptionGrantBackfillSuccess')
    expect(wrapper.find('[data-test="refund-dialog"]').attributes('data-review')).toBe('quote-after-backfill')
    expect(wrapper.find('[data-test="refund-dialog"]').exists()).toBe(true)
    expect(getOrders).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('resumes the exact balance-paused refund behind step-up', async () => {
    const paused = {
      ...order(4, 'REFUND_PENDING'),
      refund_recovery: {
        state: 'WAITING_PROVIDER_BALANCE',
        reason_code: 'WECHAT_MERCHANT_BALANCE_INSUFFICIENT',
        provider_status: 'HTTP_403_NOT_ENOUGH',
        failure_code: 'refund_submit_provider_rejected',
        refund_request_id: 'cccccccc-cccc-4ccc-8ccc-cccccccccccc',
        amount_fen: 10,
        currency: 'CNY',
        can_retry: true,
        can_confirm_external: true,
      },
    }
    retryRefund.mockResolvedValue({ data: { success: false, warning: 'pending' } })
    const wrapper = await mountOrders([paused])

    await buttonForOrder(wrapper, 4, 'payment.admin.retryPausedRefund').trigger('click')
    await flushPromises()

    expect(stepUpRun).toHaveBeenCalledTimes(1)
    expect(retryRefund).toHaveBeenCalledWith(4)
    expect(showSuccess).toHaveBeenCalledWith('payment.admin.refundRetryQueued')
    expect(getOrders).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('confirms an external refund without allowing the administrator to change its amount', async () => {
    const paused = {
      ...order(4, 'REFUND_PENDING'),
      refund_recovery: {
        state: 'WAITING_PROVIDER_BALANCE',
        reason_code: 'WECHAT_MERCHANT_BALANCE_INSUFFICIENT',
        provider_status: 'HTTP_403_NOT_ENOUGH',
        failure_code: 'refund_submit_provider_rejected',
        refund_request_id: 'cccccccc-cccc-4ccc-8ccc-cccccccccccc',
        amount_fen: 10,
        currency: 'CNY',
        can_retry: true,
        can_confirm_external: true,
      },
    }
    confirmExternalRefund.mockResolvedValue({ data: { success: true } })
    const wrapper = await mountOrders([paused])

    await buttonForOrder(wrapper, 4, 'payment.admin.externalRefundAction').trigger('click')
    await wrapper.get('#external-refund-reference').setValue('wx-transfer-20260916-1')
    await wrapper.get('#external-refund-time').setValue('2026-09-15T12:30')
    await wrapper.get('#external-refund-evidence').setValue('Verified recipient and exact transfer amount')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(stepUpRun).toHaveBeenCalledTimes(1)
    expect(confirmExternalRefund).toHaveBeenCalledWith(4, expect.objectContaining({
      method_code: 'wechat_transfer',
      external_reference: 'wx-transfer-20260916-1',
      refunded_at: expect.any(String),
      evidence_detail: 'Verified recipient and exact transfer amount',
    }))
    expect(confirmExternalRefund.mock.calls[0][1]).not.toHaveProperty('amount')
    expect(confirmExternalRefund.mock.calls[0][1]).not.toHaveProperty('amount_fen')
    expect(showSuccess).toHaveBeenCalledWith('payment.admin.externalRefundSuccess')
    expect(getOrders).toHaveBeenCalledTimes(2)
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

  it('keeps invoice status and filtering read-only on the order page', async () => {
    const invoiceOrder = { ...order(42, 'COMPLETED'), invoice: { status: 'PENDING' } }
    const wrapper = await mountOrders([invoiceOrder])
    const invoiceFilter = wrapper.findAllComponents({ name: 'Select' }).find(
      (select) => select.attributes('aria-label') === 'payment.invoice.admin.filterLabel'
    )

    expect(invoiceFilter).toBeDefined()
    await invoiceFilter!.vm.$emit('update:modelValue', 'ISSUED')
    await invoiceFilter!.vm.$emit('change')
    await flushPromises()
    expect(getOrders).toHaveBeenLastCalledWith(expect.objectContaining({ invoice_status: 'ISSUED' }))

    expect(wrapper.find('a[href="/admin/orders/invoices"]').exists()).toBe(false)
    expect(wrapper.find('[data-order-id="42"]').text()).not.toContain('payment.invoice.admin.process')

    await buttonForOrder(wrapper, 42, 'common.view').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="invoice-status"]').text()).toBe('PENDING')
    expect(wrapper.text()).not.toContain('payment.invoice.admin.openWorkflow')
    wrapper.unmount()
  })
})
