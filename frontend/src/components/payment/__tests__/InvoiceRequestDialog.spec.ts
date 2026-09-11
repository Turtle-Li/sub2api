import { describe, expect, it, vi } from 'vitest'
import { defineComponent, ref } from 'vue'
import { mount } from '@vue/test-utils'

import InvoiceRequestDialog from '../InvoiceRequestDialog.vue'
import type { PaymentInvoiceRecord, PaymentOrder } from '@/types/payment'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key, locale: ref('en-US') }),
}))

const BaseDialogStub = defineComponent({
  props: { show: Boolean, title: String, width: String },
  emits: ['close'],
  template: '<section v-if="show"><h1>{{ title }}</h1><slot /><slot name="footer" /></section>',
})

function invoice(overrides: Partial<PaymentInvoiceRecord> = {}): PaymentInvoiceRecord {
  return {
    id: 2, order_id: 1, user_id: 7, title_type: 'enterprise', title: 'Example Co.',
    tax_identifier: '91310000MA12345678', recipient_email: 'finance@example.com',
    amount: 108, currency: 'CNY', status: 'REJECTED', revision: 1, provider: 'manual',
    email_delivery_status: 'NOT_SENT', email_delivery_attempts: 0,
    requested_at: '2026-08-28T00:00:00Z', created_at: '2026-08-28T00:00:00Z',
    updated_at: '2026-08-28T00:00:00Z', rejection_reason: 'Title mismatch', ...overrides,
  }
}

function order(currentInvoice?: PaymentInvoiceRecord): PaymentOrder {
  return {
    id: 1, user_id: 7, amount: 108, pay_amount: 108, currency: 'CNY', fee_rate: 0,
    payment_type: 'alipay', out_trade_no: 'sub2_1', status: 'COMPLETED', order_type: 'balance',
    created_at: '2026-08-28T00:00:00Z', expires_at: '2026-08-28T01:00:00Z', refund_amount: 0,
    invoice: currentInvoice,
  }
}

function mountDialog(currentOrder: PaymentOrder) {
  return mount(InvoiceRequestDialog, {
    props: { show: true, order: currentOrder },
    global: { stubs: { BaseDialog: BaseDialogStub, Icon: true, InvoiceStatusBadge: true } },
  })
}

describe('InvoiceRequestDialog', () => {
  it('prefills a rejected request and clears the business tax ID when resubmitted as personal', async () => {
    const wrapper = mountDialog(order(invoice()))

    expect((wrapper.find('#invoice-title').element as HTMLInputElement).value).toBe('Example Co.')
    await wrapper.find('#invoice-title-type').setValue('personal')
    await wrapper.find('#invoice-title').setValue('Personal Buyer')
    await wrapper.find('#invoice-email').setValue('buyer@example.com')
    await wrapper.find('form').trigger('submit')

    expect(wrapper.emitted('submit')?.[0]?.[0]).toMatchObject({
      title_type: 'personal', title: 'Personal Buyer', recipient_email: 'buyer@example.com',
      tax_identifier: undefined,
    })
  })

  it('renders an issued invoice as read-only with PDF email delivery status', () => {
    const wrapper = mountDialog(order(invoice({
      status: 'ISSUED', invoice_number: '24612000000000000001',
      document_filename: 'invoice-1.pdf', email_delivery_status: 'SENT', rejection_reason: undefined,
    })))

    expect(wrapper.find('form').exists()).toBe(false)
    expect(wrapper.find('a').exists()).toBe(false)
    expect(wrapper.text()).toContain('payment.invoice.pdfEmailTitle')
    expect(wrapper.text()).toContain('invoice-1.pdf')
  })

  it('keeps a rejected request read-only after the order enters refund processing', () => {
    const refundedOrder = { ...order(invoice()), status: 'REFUND_REQUESTED' as const }
    const wrapper = mountDialog(refundedOrder)

    expect(wrapper.find('form').exists()).toBe(false)
    expect(wrapper.text()).toContain('Title mismatch')
  })
})
