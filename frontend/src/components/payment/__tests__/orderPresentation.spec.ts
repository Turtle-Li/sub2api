import { describe, expect, it } from 'vitest'
import { compactOrderNumber, fulfillmentFact, paymentFact, purchaseName } from '../orderPresentation'
import type { PaymentOrder } from '@/types/payment'

const order = (values: Partial<PaymentOrder> = {}): PaymentOrder => ({
  id: 1, user_id: 2, amount: 20, pay_amount: 20, fee_rate: 0,
  payment_type: 'alipay', out_trade_no: 'sub2_20260910000000001', status: 'PENDING',
  order_type: 'subscription', refund_amount: 0,
  created_at: '2026-09-10T00:00:00Z', expires_at: '2026-09-10T01:00:00Z', ...values,
})

describe('order presentation facts', () => {
  it('distinguishes a paid fulfillment failure from an unpaid order', () => {
    const paidFailure = order({ status: 'FAILED', paid_at: '2026-09-10T00:01:00Z' })
    expect(paymentFact(paidFailure)).toBe('PAID')
    expect(fulfillmentFact(paidFailure)).toBe('FAILED')
    expect(paymentFact(order({ status: 'FAILED' }))).toBe('UNPAID')
    expect(fulfillmentFact(order({ status: 'FAILED' }))).toBe('NOT_STARTED')
  })
  it('preserves fulfilled history and keeps refund review out of issuance status', () => {
    const refunded = order({ status: 'REFUNDED', paid_at: '2026-09-10T00:01:00Z', completed_at: '2026-09-10T00:02:00Z', needs_manual_review: true })
    expect(paymentFact(refunded)).toBe('PAID')
    expect(fulfillmentFact(refunded)).toBe('FULFILLED')
    expect(fulfillmentFact({ ...refunded, completed_at: undefined })).toBe('PENDING')
  })
  it('honors server projections and does not invent a legacy plan', () => {
    expect(fulfillmentFact({ status: 'PAID', paid_at: '2026-09-10T00:01:00Z', fulfillment_status: 'MANUAL_REVIEW' })).toBe('PENDING')
    expect(purchaseName(order({ plan_id: 123 }))).toBeUndefined()
    expect(purchaseName(order({ product_snapshot: { name: 'Original plan' } }))).toBe('Original plan')
    expect(purchaseName(order({ product_snapshot: { label: 'Balance bundle' } }))).toBe('Balance bundle')
  })
  it('shortens only display references while retaining a recognizable suffix', () => {
    const original = 'sub2_20260910000000001'
    expect(compactOrderNumber(original)).toBe('sub2_…000001')
    expect(compactOrderNumber('short')).toBe('short')
    expect(original).toBe('sub2_20260910000000001')
  })
})
