import { createIdempotencyKey } from '@/utils/idempotency'

export const PAYMENT_RETURN_READY_MESSAGE = 'sub2api:payment-return-ready'
export const PAYMENT_POPUP_CONTEXT_KEY = 'payment.popup.context.v1'
export type PaymentPopupContext = { readonly orderId: number; readonly resumeToken?: string }

export function paymentPopupName(): string {
  return createIdempotencyKey('paymentPopup')
}

export function isPaymentPopup(): boolean {
  // Provider COOP can sever opener and clear window.name; popup-local storage
  // survives the round trip. Even a malformed marker must fail closed.
  return (/^paymentPopup(?:-[\w-]+)?$/.test(window.name) && Boolean(window.opener))
    || window.sessionStorage.getItem(PAYMENT_POPUP_CONTEXT_KEY) !== null
}

export function bindPaymentPopupContext(popup: Pick<Window, 'closed' | 'close' | 'sessionStorage'> | null, context: PaymentPopupContext): void {
  if (!popup || popup.closed) return
  if (!Number.isSafeInteger(context.orderId) || context.orderId <= 0) {
    popup.close()
    throw new RangeError('Invalid payment popup order')
  }
  // Call only on a fresh same-origin about:blank window, before any navigation.
  try {
    popup.sessionStorage.setItem(PAYMENT_POPUP_CONTEXT_KEY, JSON.stringify({ orderId: context.orderId, resumeToken: context.resumeToken || '' }))
  } catch (error) {
    popup.close()
    throw error
  }
}

/** Never use the globally shared latest checkout for a popup result. */
export function readPaymentPopupContext(): PaymentPopupContext | null {
  const raw = window.sessionStorage.getItem(PAYMENT_POPUP_CONTEXT_KEY)
  if (!raw) return null
  let data: unknown
  try { data = JSON.parse(raw) }
  catch (error) { if (error instanceof SyntaxError) return null; throw error }
  if (typeof data !== 'object' || data === null
    || !('orderId' in data) || typeof data.orderId !== 'number'
    || !Number.isSafeInteger(data.orderId) || data.orderId <= 0
    || !('resumeToken' in data) || typeof data.resumeToken !== 'string') return null
  return { orderId: data.orderId, resumeToken: data.resumeToken }
}

/** A presentation hint only. The checkout must still poll its own order. */
export function isPaymentReturnReadyMessage(data: unknown): boolean {
  return typeof data === 'object' && data !== null
    && 'type' in data && data.type === PAYMENT_RETURN_READY_MESSAGE
}
