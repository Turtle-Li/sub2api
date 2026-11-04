export const PAYMENT_RETURN_READY_MESSAGE = 'sub2api:payment-return-ready'

/** A presentation hint only. The checkout must still poll its own order. */
export function isPaymentReturnReadyMessage(data: unknown): boolean {
  return typeof data === 'object' && data !== null
    && 'type' in data && data.type === PAYMENT_RETURN_READY_MESSAGE
}
