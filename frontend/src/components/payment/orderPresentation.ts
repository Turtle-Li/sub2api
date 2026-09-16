import type { FulfillmentStatus, PaymentFactStatus, PaymentOrder } from '@/types/payment'

/** Minimum public fields needed to describe payment and fulfillment honestly. */
export interface PaymentPresentationOrder {
  status?: string | null
  paid?: boolean | null
  paid_at?: string | null
  completed_at?: string | null
  payment_status?: PaymentFactStatus | string | null
  fulfillment_status?: FulfillmentStatus | string | null
  needs_manual_review?: boolean | null
}

function normalizedStatus(status: string | null | undefined): string {
  return String(status || '').trim().toUpperCase()
}

function normalizedPaymentFact(value: string | null | undefined): PaymentFactStatus | null {
  const normalized = normalizedStatus(value)
  if (normalized === 'PAID') return 'PAID'
  if (normalized === 'UNPAID') return 'UNPAID'
  return null
}

function normalizedFulfillmentFact(value: string | null | undefined): FulfillmentStatus | null {
  const normalized = normalizedStatus(value)
  if (normalized === 'NOT_STARTED' || normalized === 'PENDING' || normalized === 'FULFILLED' || normalized === 'FAILED') {
    return normalized as FulfillmentStatus
  }
  return null
}

// Older API responses may lack the new read model. Only persisted timestamps
// establish money/fulfillment facts; FAILED alone cannot mean payment failure.
export function paymentFact(order: PaymentPresentationOrder | Partial<PaymentOrder>): PaymentFactStatus {
  const explicit = normalizedPaymentFact(order.payment_status)
  if (explicit) return explicit
  if (('paid' in order && order.paid === true) || !!order.paid_at) return 'PAID'

  // Compatibility with old authenticated DTOs that only exposed status. A
  // FAILED status is deliberately excluded: it remains unpaid unless a
  // durable paid fact is present.
  const status = normalizedStatus(order.status)
  return status === 'PAID' || status === 'RECHARGING' || status === 'COMPLETED'
    ? 'PAID'
    : 'UNPAID'
}

export function fulfillmentFact(order: PaymentPresentationOrder | Partial<PaymentOrder>): FulfillmentStatus {
  const explicit = normalizedFulfillmentFact(order.fulfillment_status)
  if (explicit) return explicit
  if (order.completed_at) return 'FULFILLED'

  const status = normalizedStatus(order.status)
  if (status === 'COMPLETED') return 'FULFILLED'
  // A terminal order without a durable payment fact never entered delivery.
  // Only a paid terminal order can honestly be called a fulfillment failure.
  if (paymentFact(order) !== 'PAID') return 'NOT_STARTED'
  if (status === 'FAILED' || status === 'CANCELLED' || status === 'EXPIRED') return 'FAILED'
  return 'PENDING'
}

export function purchaseName(order: PaymentOrder): string | undefined {
  const snapshot = order.product_snapshot
  return snapshot?.name || snapshot?.label || snapshot?.product_name || undefined
}

export function compactOrderNumber(value: string): string {
  return value.length > 14 ? `${value.slice(0, 5)}…${value.slice(-6)}` : value
}
