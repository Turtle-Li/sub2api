import { afterEach, describe, expect, it, vi } from 'vitest'
import { bindPaymentPopupContext, isPaymentPopup, paymentPopupName, readPaymentPopupContext, PAYMENT_POPUP_CONTEXT_KEY } from '../embeddedPaymentReturn'

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  sessionStorage.clear()
  document.querySelectorAll('iframe').forEach(frame => frame.remove())
})
function blankWindow(): Window {
  const frame = document.createElement('iframe')
  document.body.appendChild(frame)
  const popup = frame.contentWindow
  if (!popup) throw new Error('Missing browsing context')
  return popup
}
describe('payment popup ownership', () => {
  it.each([undefined, {}])('creates independent popup names without secure-context crypto: %s', crypto => {
    vi.stubGlobal('crypto', crypto)
    const first = paymentPopupName()
    expect(first).toMatch(/^paymentPopup-[\w-]+$/)
    expect(paymentPopupName()).not.toBe(first)
  })
  it('stamps the exact launch context before navigation and gives independent launches unique names', () => {
    const popup = blankWindow()
    const input = { orderId: 42, resumeToken: 'resume-A' }
    bindPaymentPopupContext(popup, input)
    input.orderId = 77
    expect(popup.sessionStorage.getItem(PAYMENT_POPUP_CONTEXT_KEY)).toBe(JSON.stringify({ orderId: 42, resumeToken: 'resume-A' }))
    expect(paymentPopupName()).not.toBe(paymentPopupName())
  })
  it('closes the blank popup when its ownership marker cannot be saved', () => {
    const storageError = new DOMException('blocked', 'SecurityError')
    const popup = { closed: false, close: vi.fn(), get sessionStorage(): Storage { throw storageError } }
    expect(() => bindPaymentPopupContext(popup, { orderId: 42 })).toThrow('blocked')
    expect(popup.close).toHaveBeenCalledTimes(1)
  })
  it('keeps the popup presentation and exact context after opener and window name are lost', () => {
    sessionStorage.setItem(PAYMENT_POPUP_CONTEXT_KEY, JSON.stringify({ orderId: 42, resumeToken: '' }))
    expect(isPaymentPopup()).toBe(true)
    expect(readPaymentPopupContext()).toEqual({ orderId: 42, resumeToken: '' })
  })
  it.each(['invalid-json', '{}', '{"orderId":-1,"resumeToken":""}', '{"orderId":42}', '{"orderId":42,"resumeToken":77}'])('fails closed for malformed popup ownership: %s', raw => {
    sessionStorage.setItem(PAYMENT_POPUP_CONTEXT_KEY, raw)
    expect(isPaymentPopup()).toBe(true)
    expect(readPaymentPopupContext()).toBeNull()
  })
})
