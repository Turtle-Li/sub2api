import { describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { mount } from '@vue/test-utils'

import AdminInvoiceDialog from '../AdminInvoiceDialog.vue'
import type { PaymentInvoiceRecord, PaymentOrder } from '@/types/payment'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/utils/format', () => ({ formatBytes: (value: number) => `${value} bytes` }))

const BaseDialogStub = defineComponent({
  props: { show: Boolean, title: String, width: String },
  emits: ['close'],
  template: '<section v-if="show"><slot /><slot name="footer" /></section>',
})

function order(status: PaymentInvoiceRecord['status']): PaymentOrder {
  const currentInvoice: PaymentInvoiceRecord = {
    id: 2, order_id: 1, user_id: 7, title_type: 'enterprise', title: 'Example Co.',
    tax_identifier: '91310000MA12345678', recipient_email: 'finance@example.com', amount: 108,
    currency: 'CNY', status, revision: 1, provider: 'manual', requested_at: '2026-08-28T00:00:00Z',
    created_at: '2026-08-28T00:00:00Z', updated_at: '2026-08-28T00:00:00Z',
    email_delivery_status: 'NOT_SENT', email_delivery_attempts: 0,
  }
  if (status === 'ISSUED') {
    currentInvoice.invoice_item_name = 'Information technology services'
    currentInvoice.invoice_number = '24612000000000000001'
    currentInvoice.document_filename = 'invoice-1.pdf'
    currentInvoice.document_size_bytes = 128
    currentInvoice.email_delivery_status = 'SENT'
  }
  return {
    id: 1, user_id: 7, amount: 108, pay_amount: 108, fee_rate: 0, payment_type: 'alipay',
    out_trade_no: 'sub2_1', status: 'COMPLETED', order_type: 'balance', created_at: '2026-08-28T00:00:00Z',
    expires_at: '2026-08-28T01:00:00Z', refund_amount: 0, invoice: currentInvoice,
  }
}

function mountDialog(currentOrder: PaymentOrder) {
  return mount(AdminInvoiceDialog, {
    props: { show: true, order: currentOrder },
    global: { stubs: { BaseDialog: BaseDialogStub, Icon: true, InvoiceStatusBadge: true, InvoiceEmailDeliveryBadge: true } },
  })
}

describe('AdminInvoiceDialog', () => {
  it('requires and emits the issued-invoice delivery contract', async () => {
    const wrapper = mountDialog(order('PROCESSING'))
    await wrapper.find('#admin-invoice-status').setValue('ISSUED')
    await wrapper.find('#admin-invoice-item').setValue('Information technology services')
    await wrapper.find('#admin-invoice-number').setValue('24612000000000000001')
    const pdf = new File(['%PDF-1.7\ninvoice'], 'invoice-1.pdf', { type: 'application/pdf' })
    const input = wrapper.find('#admin-invoice-pdf')
    Object.defineProperty(input.element, 'files', { value: [pdf] })
    await input.trigger('change')
    await wrapper.find('form').trigger('submit')

    const emitted = wrapper.emitted('submit')?.[0]?.[0] as Record<string, unknown>
    expect(emitted).toMatchObject({
      status: 'ISSUED', provider: 'manual', invoice_item_name: 'Information technology services',
      invoice_number: '24612000000000000001', invoice_pdf: pdf,
    })
  })

  it('requires a pending request to enter processing before it can be issued', () => {
    const wrapper = mountDialog(order('PENDING'))
    const values = wrapper.findAll('#admin-invoice-status option').map((option) => option.attributes('value'))
    expect(values).toEqual(['PROCESSING', 'REJECTED'])
  })

  it('keeps an issued invoice read-only and surfaces the refund correction warning', () => {
    const wrapper = mountDialog(order('ISSUED'))
    expect(wrapper.find('form').exists()).toBe(false)
    expect(wrapper.text()).toContain('payment.invoice.admin.refundCorrectionWarning')
  })

  it('allows an administrator to retry a failed result email', async () => {
    const failedOrder = order('ISSUED')
    failedOrder.invoice!.email_delivery_status = 'FAILED'
    const wrapper = mountDialog(failedOrder)

    await wrapper.find('button.btn-secondary.btn-sm').trigger('click')

    expect(wrapper.emitted('retry-email')).toHaveLength(1)
  })
})
