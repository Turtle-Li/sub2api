import { describe, expect, it } from 'vitest'

import { canRefund } from '../orderUtils'
import type { PaymentInvoiceRecord, PaymentOrder } from '@/types/payment'

const refundableOrder: Pick<PaymentOrder, 'status' | 'refund_amount' | 'invoice' | 'order_type'> = {
  status: 'COMPLETED',
  refund_amount: 0,
  order_type: 'balance',
}

describe('canRefund', () => {
  it('hides the action after an invoice is issued', () => {
    const issuedInvoice = { status: 'ISSUED' } as PaymentInvoiceRecord

    expect(canRefund({ ...refundableOrder, invoice: issuedInvoice })).toBe(false)
  })

  it('keeps the existing action for a rejected invoice', () => {
    const rejectedInvoice = { status: 'REJECTED' } as PaymentInvoiceRecord

    expect(canRefund({ ...refundableOrder, invoice: rejectedInvoice })).toBe(true)
  })

  it('never offers an automatic refund for a custom collection', () => {
    expect(canRefund({ ...refundableOrder, order_type: 'collection' })).toBe(false)
  })
})
