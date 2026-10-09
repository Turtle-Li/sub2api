import { resolveAlipayQRCode } from '@/components/payment/paymentFlow'
import type { CreateOrderResult } from '@/types/payment'
import type { AdminCollectionPaymentType } from '@/api/admin/payment'

export const COLLECTION_MIN_AMOUNT_FEN = 1
export const COLLECTION_MAX_AMOUNT_FEN = 100_000_000

/** Parse user-entered CNY without floating-point conversion. */
export function parseCollectionAmountFen(value: string): number | null {
  const source = value.trim()
  const match = /^(\d+)(?:\.(\d{1,2}))?$/.exec(source)
  if (!match) return null

  const yuan = Number(match[1])
  const fractional = (match[2] || '').padEnd(2, '0')
  const fen = yuan * 100 + Number(fractional || '0')
  if (!Number.isSafeInteger(yuan) || !Number.isSafeInteger(fen)) return null
  if (fen < COLLECTION_MIN_AMOUNT_FEN || fen > COLLECTION_MAX_AMOUNT_FEN) return null
  return fen
}

export function collectionIntentFingerprint(amountFen: number, paymentType: AdminCollectionPaymentType): string {
  return `${amountFen}:${paymentType}`
}

/**
 * Payment URLs are never QR substitutes. Alipay additionally requires its
 * official native QR payload; WeChat Native payloads are provider-issued
 * opaque strings and are accepted only from qr_code.
 */
export function resolveCollectionNativeQRCode(
  result: Pick<CreateOrderResult, 'qr_code' | 'pay_url' | 'checkout_frame_url'>,
  paymentType: AdminCollectionPaymentType,
): string {
  if (paymentType === 'alipay') return resolveAlipayQRCode(result)
  return String(result.qr_code || '').trim()
}

/**
 * A provider checkout page may be offered as an explicit browser link, but it
 * is never a substitute for the provider's native QR payload.
 */
export function resolveCollectionCheckoutURL(
  result: Pick<CreateOrderResult, 'pay_url'>,
): string {
  if (typeof result.pay_url !== 'string') return ''
  const raw = result.pay_url
  if (
    !raw
    || raw.length > 16_384
    || raw !== raw.trim()
    || Array.from(raw).some(character => {
      const code = character.charCodeAt(0)
      return code <= 0x1f || code === 0x7f || character.trim() === ''
    })
  ) return ''

  let url: URL
  try {
    url = new URL(raw)
  } catch {
    return ''
  }

  if (
    url.protocol !== 'https:'
    || !url.hostname
    || url.username
    || url.password
    || url.hash
  ) return ''

  return raw
}
