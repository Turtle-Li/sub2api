/** Only an authoritative paid/completed shop order ends the checkout flow. */
export function isManualShopPaymentConfirmed(order: { order_type?: string; status?: string } | null | undefined): boolean {
  return order?.order_type === 'manual_shop' && (order.status === 'PAID' || order.status === 'COMPLETED')
}
