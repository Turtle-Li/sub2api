import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import AdminInvoiceRequestsView from '../AdminInvoiceRequestsView.vue'

const { getOrders, updateInvoiceRequest, retryInvoiceEmail, retryInvoiceFeishu, routeQuery, showError } = vi.hoisted(() => ({
  getOrders: vi.fn(),
  updateInvoiceRequest: vi.fn(),
  retryInvoiceEmail: vi.fn(),
  retryInvoiceFeishu: vi.fn(),
  routeQuery: {} as Record<string, string>,
  showError: vi.fn(),
}))

vi.mock('@/api/admin/payment', () => {
  const api = {
    getOrders,
    updateInvoiceRequest,
    retryInvoiceEmail,
    retryInvoiceFeishu,
  }
  return { adminPaymentAPI: api, default: api }
})
vi.mock('vue-router', () => ({ useRoute: () => ({ query: routeQuery }) }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key, locale: { value: 'en-US' } }),
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError, showSuccess: vi.fn() }) }))
vi.mock('@/components/payment/orderUtils', () => ({ formatOrderDateTime: (value: string) => value }))
vi.mock('@/utils/format', () => ({ formatBytes: (value: number) => `${value} bytes` }))

const invoiceOrder = {
  id: 51,
  user_id: 7,
  amount: 108,
  pay_amount: 108,
  currency: 'CNY',
  fee_rate: 0,
  payment_type: 'alipay',
  out_trade_no: 'sub2_51',
  status: 'COMPLETED',
  order_type: 'balance',
  created_at: '2026-08-28T00:00:00Z',
  expires_at: '2026-08-28T01:00:00Z',
  refund_amount: 0,
  invoice: {
    id: 2,
    order_id: 51,
    user_id: 7,
    title_type: 'enterprise',
    title: 'Example Co.',
    tax_identifier: '91310000MA12345678',
    recipient_email: 'finance@example.com',
    amount: 108,
    currency: 'CNY',
    status: 'PENDING',
    revision: 1,
    provider: 'manual',
    email_delivery_status: 'NOT_SENT',
    email_delivery_attempts: 0,
    requested_at: '2026-08-28T00:00:00Z',
    created_at: '2026-08-28T00:00:00Z',
    updated_at: '2026-08-28T00:00:00Z',
  },
}

function mountView() {
  return mount(AdminInvoiceRequestsView, {
    global: {
      stubs: {
        AppLayout: { template: '<main><slot /></main>' },
        DataTable: { props: ['data'], template: '<div><div data-test="applications">{{ data.length }}</div><div v-for="row in data" :key="row.id" :data-order="row.id"><slot name="cell-actions" :row="row" /></div></div>' },
        Pagination: true,
        Select: true,
        Icon: true,
        AdminInvoiceDialog: true,
      },
    },
  })
}

describe('AdminInvoiceRequestsView', () => {
  beforeEach(() => {
    getOrders.mockReset()
    updateInvoiceRequest.mockReset()
    retryInvoiceEmail.mockReset()
    retryInvoiceFeishu.mockReset()
    showError.mockReset()
    Object.keys(routeQuery).forEach((key) => delete routeQuery[key])
    getOrders.mockResolvedValue({ data: { items: [invoiceOrder], total: 1 } })
  })

  it('loads only orders with invoice requests by default', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(getOrders).toHaveBeenCalledWith(expect.objectContaining({ invoice_status: 'HAS_INVOICE' }))
    expect(wrapper.get('[data-test="applications"]').text()).toBe('1')
  })

  it.each(['update', 'email', 'feishu'])('keeps the current order when a delayed %s response belongs to a closed dialog', async (action) => {
    const otherOrder = { ...invoiceOrder, id: 52, out_trade_no: 'sub2_52', invoice: { ...invoiceOrder.invoice, id: 3, order_id: 52, title: 'Second Co.' } }
    getOrders.mockResolvedValue({ data: { items: [invoiceOrder, otherOrder], total: 2 } })
    let resolve!: (value: unknown) => void
    const pending = new Promise((done) => { resolve = done })
    const request = action === 'update' ? updateInvoiceRequest : action === 'email' ? retryInvoiceEmail : retryInvoiceFeishu
    request.mockReturnValue(pending)
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-order="51"] button').trigger('click')
    const dialog = wrapper.findComponent({ name: 'AdminInvoiceDialog' })
    dialog.vm.$emit(action === 'update' ? 'submit' : `retry-${action}`, { status: 'PROCESSING' })
    await flushPromises()
    expect(request).toHaveBeenCalledWith(51, ...(action === 'update' ? [{ status: 'PROCESSING' }] : []))
    dialog.vm.$emit('close')
    await wrapper.get('[data-order="52"] button').trigger('click')
    resolve({ data: { ...invoiceOrder.invoice, title: 'First updated' } })
    await flushPromises()
    expect(dialog.props('show')).toBe(true)
    expect(dialog.props('order')).toMatchObject({ id: 52, invoice: { title: 'Second Co.' } })
    wrapper.unmount()
  })

  it('honors a supported invoice status from the route query', async () => {
    routeQuery.invoice_status = 'PENDING'
    mountView()
    await flushPromises()

    expect(getOrders).toHaveBeenCalledWith(expect.objectContaining({ invoice_status: 'PENDING' }))
  })
})
