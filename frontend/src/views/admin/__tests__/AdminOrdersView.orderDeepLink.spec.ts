import { nextTick, reactive } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AdminOrdersView from '../orders/AdminOrdersView.vue'

const { getOrders, getOrder, showError, showSuccess, showWarning } = vi.hoisted(() => ({
  getOrders: vi.fn(),
  getOrder: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  showWarning: vi.fn(),
}))

const route = reactive<{ query: Record<string, unknown> }>({ query: {} })

vi.mock('@/api/admin/payment', () => ({
  adminPaymentAPI: { getOrders, getOrder },
  default: { getOrders, getOrder },
}))
vi.mock('vue-router', () => ({
  useRoute: () => route,
  RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' },
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess, showWarning, showError }) }))
vi.mock('@/composables/useStepUp', () => ({
  useStepUp: () => ({ run: vi.fn() }),
  isStepUpCancelled: () => false,
}))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key }),
}))

function order(id: number) {
  return {
    id,
    user_id: 42,
    status: 'COMPLETED',
    out_trade_no: `order-${id}`,
    amount: 10,
    pay_amount: 8,
    currency: 'CNY',
    payment_type: 'alipay',
    fee_rate: 0,
    order_type: 'balance',
    refund_amount: 0,
    created_at: '2026-09-19T00:00:00Z',
    expires_at: '2026-09-19T01:00:00Z',
  }
}

const stubs = {
  AppLayout: { template: '<main><slot /></main>' },
  OrderTable: { props: ['orders', 'loading'], template: '<div data-test="order-table" />' },
  BaseDialog: { props: ['show'], template: '<section v-if="show" data-test="order-detail-dialog"><slot /></section>' },
  Select: true,
  Pagination: true,
  Icon: true,
  AdminRefundDialog: true,
  AdminPaymentOwnerTest: true,
  OrderStatusBadge: true,
  InvoiceStatusBadge: true,
  OrderPurchaseSnapshot: true,
  OrderLifecycleBadge: true,
  TotpStepUpDialog: true,
}

function clearRouteQuery() {
  for (const key of Object.keys(route.query)) delete route.query[key]
}

function setRouteOrderID(value: unknown) {
  route.query.order_id = value
}

async function mountOrders() {
  const wrapper = mount(AdminOrdersView, { global: { stubs } })
  await flushPromises()
  return wrapper
}

describe('admin order deep links', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    clearRouteQuery()
    getOrders.mockResolvedValue({ data: { items: [], total: 0 } })
  })

  it('fetches and opens the linked order detail and audit outside the current list page', async () => {
    const linkedOrder = order(202601)
    setRouteOrderID('202601')
    getOrder.mockResolvedValue({
      data: {
        order: linkedOrder,
        auditLogs: [{ id: 7, action: 'payment_coupon_consumed', detail: 'coupon applied', operator: 'admin@example.com', created_at: '2026-09-19T00:01:00Z' }],
      },
    })

    const wrapper = await mountOrders()

    expect(getOrder).toHaveBeenCalledWith(202601)
    expect(wrapper.get('[data-test="order-detail-dialog"]').text()).toContain('#202601')
    expect(wrapper.text()).toContain('payment_coupon_consumed')
    wrapper.unmount()
  })

  it.each(['NOT_APPLICABLE', 'MANUAL_REVIEW'])('separates paid manual review from refund handling (%s)', async (refundStatus) => {
    setRouteOrderID('202601')
    getOrder.mockResolvedValue({ data: { order: {
      ...order(202601), status: 'FAILED', paid_at: '2026-09-19T00:01:00Z',
      payment_status: 'PAID', fulfillment_status: 'FAILED', needs_manual_review: true,
      refund_entitlement_status: refundStatus,
    }, audit_logs: [] } })
    const wrapper = await mountOrders()
    const detail = wrapper.get('[data-test="order-detail-dialog"]').text()
    if (refundStatus === 'NOT_APPLICABLE') {
      expect(detail).toContain('payment.result.paidManualReview')
      expect(detail).not.toContain('payment.orderOps.refundHandling')
      expect(detail).not.toContain('payment.orderOps.refundReviewRequired')
    } else {
      expect(detail).toContain('payment.orderOps.refundHandling')
      expect(detail).toContain('payment.orderOps.refundReviewRequired')
      expect(detail).not.toContain('payment.result.paidManualReview')
    }
    wrapper.unmount()
  })

  it('ignores an earlier linked-order response after the route changes', async () => {
    let resolveFirst: ((value: { data: { order: ReturnType<typeof order> } }) => void) | undefined
    let resolveSecond: ((value: { data: { order: ReturnType<typeof order> } }) => void) | undefined
    const first = new Promise<{ data: { order: ReturnType<typeof order> } }>((resolve) => { resolveFirst = resolve })
    const second = new Promise<{ data: { order: ReturnType<typeof order> } }>((resolve) => { resolveSecond = resolve })
    getOrder.mockImplementation((id: number) => id === 202601 ? first : second)
    setRouteOrderID('202601')

    const wrapper = await mountOrders()
    setRouteOrderID('202602')
    await nextTick()
    await flushPromises()

    expect(getOrder).toHaveBeenNthCalledWith(1, 202601)
    expect(getOrder).toHaveBeenNthCalledWith(2, 202602)

    resolveFirst!({ data: { order: order(202601) } })
    await flushPromises()
    expect(wrapper.find('[data-test="order-detail-dialog"]').exists()).toBe(false)

    resolveSecond!({ data: { order: order(202602) } })
    await flushPromises()
    expect(wrapper.get('[data-test="order-detail-dialog"]').text()).toContain('#202602')
    wrapper.unmount()
  })

  it('keeps the detail closed and reports an inaccessible linked order', async () => {
    setRouteOrderID('202601')
    getOrder.mockRejectedValue({ response: { status: 404 }, message: 'not found' })

    const wrapper = await mountOrders()

    expect(getOrder).toHaveBeenCalledWith(202601)
    expect(showError).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-test="order-detail-dialog"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it.each([
    ['0'],
    ['-1'],
    ['1.5'],
    ['1e3'],
    ['9007199254740992'],
    [['202601']],
  ])('does not fetch malformed order_id %p', async (orderID) => {
    setRouteOrderID(orderID)

    const wrapper = await mountOrders()

    expect(getOrder).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="order-detail-dialog"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
