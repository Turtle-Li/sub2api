import { describe, expect, it } from 'vitest'

import { canRefund } from '../orderUtils'
import type { PaymentInvoiceRecord, PaymentOrder } from '@/types/payment'

const refundableOrder: Pick<PaymentOrder, 'status' | 'refund_amount' | 'invoice'> = {
  status: 'COMPLETED',
  refund_amount: 0,
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
})
