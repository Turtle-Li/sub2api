import { describe, expect, it } from 'vitest'
import {
  collectionIntentFingerprint,
  parseCollectionAmountFen,
  resolveCollectionCheckoutURL,
  resolveCollectionNativeQRCode,
} from '../adminCollection'

describe('admin custom collection helpers', () => {
  it.each([
    ['432', 43_200],
    ['432.01', 43_201],
    ['0.01', 1],
    ['1000000.00', 100_000_000],
  ])('parses %s into exact fen', (input, expected) => {
    expect(parseCollectionAmountFen(input)).toBe(expected)
  })

  it.each(['', '0', '432.001', '.01', '-1', '1000000.01', '1e2'])('rejects an invalid CNY amount: %s', (input) => {
    expect(parseCollectionAmountFen(input)).toBeNull()
  })

  it('keeps one deterministic intent identity for retries', () => {
    expect(collectionIntentFingerprint(43_200, 'alipay')).toBe('43200:alipay')
  })

  it('never converts a hosted payment URL into a collection QR code', () => {
    expect(resolveCollectionNativeQRCode({ qr_code: '', pay_url: 'https://pay.example.test/checkout', checkout_frame_url: 'https://pay.example.test/frame' }, 'wxpay')).toBe('')
    expect(resolveCollectionNativeQRCode({ qr_code: '', pay_url: 'https://pay.example.test/checkout' }, 'alipay')).toBe('')
  })

  it('permits a safe HTTPS checkout page only as an explicit link', () => {
    expect(resolveCollectionCheckoutURL({ pay_url: 'https://pay.example.test/checkout/42' })).toBe('https://pay.example.test/checkout/42')
    expect(resolveCollectionCheckoutURL({ pay_url: 'javascript:alert(1)' })).toBe('')
    expect(resolveCollectionCheckoutURL({ pay_url: 'https://user:pass@pay.example.test/checkout/42' })).toBe('')
  })
})
