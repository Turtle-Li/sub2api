import type { FulfillmentStatus, PaymentFactStatus, PaymentOrder } from '@/types/payment'

// Older API responses may lack the new read model. Only persisted timestamps
// establish money/fulfillment facts; FAILED alone cannot mean payment failure.
export function paymentFact(order: PaymentOrder): PaymentFactStatus {
  return order.payment_status ?? (order.paid_at ? 'PAID' : 'UNPAID')
}

export function fulfillmentFact(order: PaymentOrder): FulfillmentStatus {
  if (order.fulfillment_status) return order.fulfillment_status
  if (order.completed_at) return 'FULFILLED'
  if (!order.paid_at) return 'NOT_STARTED'
  if (order.needs_manual_review) return 'MANUAL_REVIEW'
  return order.status === 'FAILED' ? 'FAILED' : 'PENDING'
}

export function purchaseName(order: PaymentOrder): string | undefined {
  const snapshot = order.product_snapshot
  return snapshot?.name || snapshot?.label || snapshot?.product_name || undefined
}

export function compactOrderNumber(value: string): string {
  return value.length > 14 ? `${value.slice(0, 5)}…${value.slice(-6)}` : value
}
