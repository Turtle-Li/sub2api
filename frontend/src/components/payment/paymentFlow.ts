import type {
  CreateOrderRequest,
  CreateOrderResult,
  MethodLimit,
  OrderType,
  WechatJSAPIPayload,
  WechatOAuthInfo,
} from '@/types/payment'

export const PAYMENT_RECOVERY_STORAGE_KEY = 'payment.recovery.current'
export const RESET_CARD_CHECKOUT_ATTEMPT_STORAGE_KEY = 'payment.reset-card.checkout-attempt.v1'

const VISIBLE_METHOD_ALIASES = {
  alipay: 'alipay',
  alipay_direct: 'alipay',
  wxpay: 'wxpay',
  wxpay_direct: 'wxpay',
  stripe: 'stripe',
  airwallex: 'airwallex',
} as const

export type VisiblePaymentMethod = 'alipay' | 'wxpay' | 'stripe' | 'airwallex'
export type StripeVisibleMethod = 'alipay' | 'wechat_pay'
export type PaymentLaunchKind =
  | 'qr_waiting'
  | 'status_waiting'
  | 'alipay_deep_link'
  | 'redirect_waiting'
  | 'stripe_popup'
  | 'stripe_route'
  | 'airwallex_route'
  | 'wechat_oauth'
  | 'wechat_jsapi'
  | 'unhandled'

/**
 * The browser only needs enough information to resume one checkout. This
 * object deliberately carries no provider return identifier: central payment
 * services may use a different channel order number than Sub2's out_trade_no.
 */
export interface PaymentRecoverySnapshot {
  orderId: number
  amount: number
  qrCode: string
  expiresAt: string
  paymentType: string
  payUrl: string
  outTradeNo: string
  clientSecret: string
  intentId: string
  currency: string
  countryCode: string
  paymentEnv: string
  payAmount: number
  orderType: OrderType | ''
  paymentMode: string
  resumeToken: string
  alipayMobilePrecreateDeepLink?: boolean
  createdAt: number
}

export interface PaymentRecoveryMatch {
  orderId?: number
  resumeToken?: string
  /** Only use this for a known local Sub2 out_trade_no, never a provider channel id. */
  outTradeNo?: string
}

/**
 * A reset-card checkout key is browser-persisted because the create-order
 * request may commit before its response reaches the browser. The fingerprint
 * includes every client-side quote input that defines the logical checkout.
 */
export interface ResetCardCheckoutAttemptInput {
  userId: number
  subscriptionId: number
  groupId: number
  planId: number
  amount: number
  monthlyPrice: number
  expiresAt: string
  paymentType: string
  tierRevision?: string
}

export interface ResetCardCheckoutAttempt {
  fingerprint: string
  idempotencyKey: string
  orderId?: number
}

export interface PaymentLaunchContext {
  visibleMethod: string
  orderType: OrderType
  isMobile: boolean
  isWechatBrowser?: boolean
  /** When true, Alipay payments always use QR code regardless of device type */
  forceQRCode?: boolean
  /** When true, the new mobile Alipay precreate flow takes priority over forceQRCode */
  mobilePrecreateDeepLink?: boolean
  now?: number
  stripePopupUrl?: string
  stripeRouteUrl?: string
  airwallexRouteUrl?: string
}

export interface PaymentLaunchDecision {
  kind: PaymentLaunchKind
  paymentState: PaymentRecoverySnapshot
  recovery: PaymentRecoverySnapshot
  stripeMethod?: StripeVisibleMethod
  oauth?: WechatOAuthInfo
  jsapi?: WechatJSAPIPayload
}

export interface BuildCreateOrderPayloadInput {
  amount: number
  paymentType: string
  orderType: OrderType
  planId?: number
  subscriptionId?: number
  resetCardTierRevision?: string
  origin?: string
  isMobile: boolean
  isWechatBrowser: boolean
  /** When true, Alipay payments always use QR code (passes is_mobile: false to backend) */
  forceQRCode?: boolean
  /** When true, keep the real mobile signal so the backend can select precreate */
  mobilePrecreateDeepLink?: boolean
}

type CreateOrderFlowResult = CreateOrderResult & {
  resume_token?: string
}

type StorageReader = Pick<Storage, 'getItem'>
type StorageWriter = Pick<Storage, 'removeItem' | 'setItem'> & Partial<StorageReader>
type AttemptStorage = Pick<Storage, 'getItem' | 'removeItem' | 'setItem'>

interface PaymentRecoveryEnvelope {
  version: 2
  activeOrderId?: number
  orders: Record<string, PaymentRecoverySnapshot>
}

const MAX_RECOVERY_ENTRIES = 4
const MAX_RECOVERY_AGE_MS = 2 * 60 * 60 * 1000

export function normalizeVisibleMethod(method: string): VisiblePaymentMethod | '' {
  const normalized = VISIBLE_METHOD_ALIASES[method.trim() as keyof typeof VISIBLE_METHOD_ALIASES]
  return normalized ?? ''
}

export function getVisibleMethods(methods: Record<string, MethodLimit>): Record<string, MethodLimit> {
  const visible: Record<string, MethodLimit> = {}

  Object.entries(methods).forEach(([type, limit]) => {
    const normalized = normalizeVisibleMethod(type) || type.trim()
    if (!normalized) return

    const isCanonical = type === normalized
    const existing = visible[normalized]
    if (!existing || isCanonical) {
      visible[normalized] = { ...limit }
    }
  })

  return visible
}

export function buildCreateOrderPayload(input: BuildCreateOrderPayloadInput): CreateOrderRequest {
  const visibleMethod = normalizeVisibleMethod(input.paymentType) || input.paymentType.trim()
  const normalizedOrigin = (input.origin || '').trim().replace(/\/+$/, '')
  // When forceQRCode is enabled for alipay, always tell the backend this is not a mobile
  // request so it generates a QR code instead of a mobile-redirect URL.
  const effectiveMobile = (input.forceQRCode && !input.mobilePrecreateDeepLink && visibleMethod === 'alipay')
    ? false
    : input.isMobile
  const payload: CreateOrderRequest = {
    amount: input.amount,
    payment_type: visibleMethod,
    order_type: input.orderType,
    is_mobile: effectiveMobile,
    payment_source: visibleMethod === 'wxpay' && input.isWechatBrowser
      ? 'wechat_in_app_resume'
      : 'hosted_redirect',
  }

  if (input.planId) {
    payload.plan_id = input.planId
  }
  if (input.subscriptionId) {
    payload.subscription_id = input.subscriptionId
  }
  const tierRevision = String(input.resetCardTierRevision || '').trim()
  if (tierRevision) {
    payload.reset_card_tier_revision = tierRevision
  }
  if (normalizedOrigin) {
    payload.return_url = `${normalizedOrigin}/payment/result`
  }

  return payload
}

export function decidePaymentLaunch(
  result: CreateOrderFlowResult,
  context: PaymentLaunchContext,
): PaymentLaunchDecision {
  const visibleMethod = normalizeVisibleMethod(context.visibleMethod) || context.visibleMethod
  const baseState = createPaymentRecoverySnapshot({
    orderId: result.order_id,
    amount: result.amount,
    qrCode: result.qr_code || '',
    expiresAt: result.expires_at || '',
    paymentType: visibleMethod,
    payUrl: result.pay_url || '',
    outTradeNo: result.out_trade_no || '',
    clientSecret: result.client_secret || '',
    intentId: result.intent_id || '',
    currency: result.currency || '',
    countryCode: result.country_code || '',
    paymentEnv: result.payment_env || '',
    payAmount: result.pay_amount,
    orderType: context.orderType,
    paymentMode: (result.payment_mode || '').trim(),
    resumeToken: result.resume_token || '',
    alipayMobilePrecreateDeepLink: result.alipay_mobile_precreate_deep_link === true,
  }, context.now)

  if (visibleMethod === 'airwallex' && baseState.clientSecret && baseState.intentId) {
    if (!context.airwallexRouteUrl) {
      return { kind: 'unhandled', paymentState: baseState, recovery: baseState }
    }
    const paymentState = { ...baseState, payUrl: context.airwallexRouteUrl || '' }
    return { kind: 'airwallex_route', paymentState, recovery: paymentState }
  }

  // Stripe is a separate visible method. A client_secret on an Alipay or
  // WeChat response must never silently route the customer through Stripe;
  // unified/direct gateways may include unrelated compatibility fields.
  if (visibleMethod === 'stripe' && baseState.clientSecret) {
    const kind: PaymentLaunchKind = context.isMobile ? 'stripe_route' : 'stripe_popup'
    const payUrl = kind === 'stripe_popup'
      ? context.stripePopupUrl || context.stripeRouteUrl || ''
      : context.stripeRouteUrl || context.stripePopupUrl || ''
    const paymentState = { ...baseState, payUrl }
    return { kind, paymentState, recovery: paymentState }
  }

  if (result.result_type === 'oauth_required' && result.oauth?.authorize_url) {
    return { kind: 'wechat_oauth', paymentState: baseState, recovery: baseState, oauth: result.oauth }
  }

  const jsapiPayload = result.jsapi ?? result.jsapi_payload
  if (result.result_type === 'jsapi_ready' && jsapiPayload) {
    return { kind: 'wechat_jsapi', paymentState: baseState, recovery: baseState, jsapi: jsapiPayload }
  }

  if (
    visibleMethod === 'alipay'
    && context.isMobile
    && baseState.alipayMobilePrecreateDeepLink
    && baseState.qrCode
  ) {
    return { kind: 'alipay_deep_link', paymentState: baseState, recovery: baseState }
  }

  const normalizedPaymentMode = baseState.paymentMode.trim().toLowerCase()
  const explicitRedirect = normalizedPaymentMode === 'redirect' || normalizedPaymentMode === 'popup'
  const explicitQr = normalizedPaymentMode === 'qrcode' || normalizedPaymentMode === 'native'

  // A real gateway QR payload is authoritative for the QR shell. A hosted
  // checkout URL is only a fallback when the provider did not supply one or
  // explicitly selected redirect mode; it is never encoded as a fake QR.
  if (baseState.qrCode && (explicitQr || !explicitRedirect)) {
    return { kind: 'qr_waiting', paymentState: baseState, recovery: baseState }
  }

  if (baseState.payUrl && (explicitRedirect || context.isMobile || !baseState.qrCode)) {
    return { kind: 'redirect_waiting', paymentState: baseState, recovery: baseState }
  }

  if (baseState.qrCode) {
    return { kind: 'qr_waiting', paymentState: baseState, recovery: baseState }
  }

  if (baseState.payUrl) {
    return { kind: 'redirect_waiting', paymentState: baseState, recovery: baseState }
  }

  // An idempotent replay can legitimately return an existing local order after
  // it has completed, expired, or been fenced from another provider launch.
  // Keep that authenticated order in the status shell so the client asks the
  // server for the authoritative outcome instead of treating it as a launch
  // error or creating another provider transaction.
  if (String(result.status || '').trim() && Number.isSafeInteger(result.order_id) && result.order_id > 0) {
    return { kind: 'status_waiting', paymentState: baseState, recovery: baseState }
  }

  return { kind: 'unhandled', paymentState: baseState, recovery: baseState }
}

export function createPaymentRecoverySnapshot(
  state: Omit<PaymentRecoverySnapshot, 'createdAt'>,
  now = Date.now(),
): PaymentRecoverySnapshot {
  return {
    ...state,
    createdAt: now,
  }
}

function isSnapshotLike(value: unknown): value is Partial<PaymentRecoverySnapshot> {
  return !!value && typeof value === 'object'
}

function normalizeOrderType(value: unknown): OrderType | '' {
  return value === 'subscription' || value === 'reset_card' || value === 'balance' ? value : 'balance'
}

function normalizeSnapshot(parsed: Partial<PaymentRecoverySnapshot>, now: number): PaymentRecoverySnapshot | null {
  if (
    typeof parsed.orderId !== 'number'
    || !Number.isFinite(parsed.orderId)
    || parsed.orderId <= 0
    || typeof parsed.amount !== 'number'
    || !Number.isFinite(parsed.amount)
    || typeof parsed.expiresAt !== 'string'
    || typeof parsed.paymentType !== 'string'
    || typeof parsed.payUrl !== 'string'
    || (parsed.qrCode != null && typeof parsed.qrCode !== 'string')
    || (parsed.outTradeNo != null && typeof parsed.outTradeNo !== 'string')
    || (parsed.clientSecret != null && typeof parsed.clientSecret !== 'string')
    || (parsed.intentId != null && typeof parsed.intentId !== 'string')
    || (parsed.currency != null && typeof parsed.currency !== 'string')
    || (parsed.countryCode != null && typeof parsed.countryCode !== 'string')
    || (parsed.paymentEnv != null && typeof parsed.paymentEnv !== 'string')
    || typeof parsed.payAmount !== 'number'
    || !Number.isFinite(parsed.payAmount)
    || (parsed.paymentMode != null && typeof parsed.paymentMode !== 'string')
    || (parsed.resumeToken != null && typeof parsed.resumeToken !== 'string')
    || typeof parsed.createdAt !== 'number'
    || !Number.isFinite(parsed.createdAt)
  ) {
    return null
  }

  // A browser-side checkout deadline is not a payment outcome. Retain a
  // recent snapshot long enough to ask the server whether it became PAID,
  // RECHARGING or COMPLETED after the page's countdown reached zero.
  if (now - parsed.createdAt > MAX_RECOVERY_AGE_MS) return null

  return {
    orderId: parsed.orderId,
    amount: parsed.amount,
    qrCode: parsed.qrCode || '',
    expiresAt: parsed.expiresAt,
    paymentType: parsed.paymentType,
    payUrl: parsed.payUrl,
    outTradeNo: parsed.outTradeNo || '',
    clientSecret: parsed.clientSecret || '',
    intentId: parsed.intentId || '',
    currency: parsed.currency || '',
    countryCode: parsed.countryCode || '',
    paymentEnv: parsed.paymentEnv || '',
    payAmount: parsed.payAmount,
    orderType: normalizeOrderType(parsed.orderType),
    paymentMode: parsed.paymentMode || '',
    resumeToken: parsed.resumeToken || '',
    alipayMobilePrecreateDeepLink: parsed.alipayMobilePrecreateDeepLink === true,
    createdAt: parsed.createdAt,
  }
}

function parseRecoveryStore(raw: string | null | undefined, now: number): PaymentRecoveryEnvelope {
  const empty: PaymentRecoveryEnvelope = { version: 2, orders: {} }
  if (!raw) return empty

  try {
    const parsed = JSON.parse(raw) as unknown
    if (isSnapshotLike(parsed) && 'orders' in parsed && isSnapshotLike((parsed as { orders?: unknown }).orders)) {
      const candidate = parsed as { activeOrderId?: unknown; orders: Record<string, unknown> }
      const orders: Record<string, PaymentRecoverySnapshot> = {}
      Object.entries(candidate.orders).forEach(([key, value]) => {
        const normalized = normalizeSnapshot(value as Partial<PaymentRecoverySnapshot>, now)
        if (normalized) orders[key] = normalized
      })
      const activeOrderId = typeof candidate.activeOrderId === 'number' ? candidate.activeOrderId : undefined
      return { version: 2, activeOrderId, orders }
    }

    const legacy = normalizeSnapshot(parsed as Partial<PaymentRecoverySnapshot>, now)
    if (legacy) {
      return { version: 2, activeOrderId: legacy.orderId, orders: { [String(legacy.orderId)]: legacy } }
    }
  } catch {
    // Invalid browser storage is treated as no recovery context.
  }
  return empty
}

function serializeRecoveryStore(envelope: PaymentRecoveryEnvelope, now: number): string {
  const entries = Object.entries(envelope.orders)
    .filter(([, snapshot]) => normalizeSnapshot(snapshot, now) !== null)
    .sort(([, left], [, right]) => right.createdAt - left.createdAt)
    .slice(0, MAX_RECOVERY_ENTRIES)
  const orders = Object.fromEntries(entries)
  const activeOrderId = envelope.activeOrderId && orders[String(envelope.activeOrderId)]
    ? envelope.activeOrderId
    : entries[0]?.[1].orderId
  return JSON.stringify({ version: 2, activeOrderId, orders })
}

export function writePaymentRecoverySnapshot(
  storage: StorageWriter,
  snapshot: PaymentRecoverySnapshot,
  key = PAYMENT_RECOVERY_STORAGE_KEY,
): void {
  if (!snapshot.orderId) return
  const now = Date.now()
  const existing = typeof storage.getItem === 'function' ? storage.getItem(key) : null
  const envelope = parseRecoveryStore(existing, now)
  envelope.orders[String(snapshot.orderId)] = snapshot
  envelope.activeOrderId = snapshot.orderId
  storage.setItem(key, serializeRecoveryStore(envelope, now))
}

export function clearPaymentRecoverySnapshot(
  storage: StorageWriter,
  key = PAYMENT_RECOVERY_STORAGE_KEY,
  match?: PaymentRecoveryMatch,
): void {
  if (!match || (match.orderId == null && !match.resumeToken && !match.outTradeNo)) {
    storage.removeItem(key)
    return
  }

  if (typeof storage.getItem !== 'function') return
  const raw = storage.getItem(key)
  if (!raw) return
  const now = Date.now()
  const envelope = parseRecoveryStore(raw, now)
  const keys = Object.keys(envelope.orders).filter((entryKey) => {
    const snapshot = envelope.orders[entryKey]
    if (match.orderId != null && snapshot.orderId !== match.orderId) return false
    if (match.resumeToken && snapshot.resumeToken !== match.resumeToken) return false
    if (match.outTradeNo && snapshot.outTradeNo !== match.outTradeNo) return false
    return true
  })
  if (keys.length === 0) return
  keys.forEach(entryKey => delete envelope.orders[entryKey])
  envelope.activeOrderId = envelope.activeOrderId && envelope.orders[String(envelope.activeOrderId)]
    ? envelope.activeOrderId
    : undefined
  if (Object.keys(envelope.orders).length === 0) {
    storage.removeItem(key)
  } else {
    storage.setItem(key, serializeRecoveryStore(envelope, now))
  }
}

export function readPaymentRecoverySnapshot(
  raw: string | null | undefined,
  options: { now?: number; resumeToken?: string; orderId?: number; outTradeNo?: string } = {},
): PaymentRecoverySnapshot | null {
  const now = options.now ?? Date.now()
  const envelope = parseRecoveryStore(raw, now)
  let candidates = Object.values(envelope.orders)
  if (options.resumeToken) candidates = candidates.filter(snapshot => snapshot.resumeToken === options.resumeToken)
  if (options.orderId != null && options.orderId > 0) candidates = candidates.filter(snapshot => snapshot.orderId === options.orderId)
  if (options.outTradeNo) candidates = candidates.filter(snapshot => snapshot.outTradeNo === options.outTradeNo)
  if (candidates.length === 0) return null

  if (!options.resumeToken && !options.orderId && !options.outTradeNo && envelope.activeOrderId) {
    const active = candidates.find(snapshot => snapshot.orderId === envelope.activeOrderId)
    if (active) return active
  }
  return candidates.sort((left, right) => right.createdAt - left.createdAt)[0] || null
}

function fingerprintMoney(value: number): string {
  if (!Number.isFinite(value)) return ''
  return (Math.round(value * 100) / 100).toFixed(2)
}

/**
 * Keep the serialized form deterministic so an interrupted request can reuse
 * its key only for the exact quote that created it.
 */
export function createResetCardCheckoutFingerprint(input: ResetCardCheckoutAttemptInput): string {
  return JSON.stringify({
    version: 1,
    userId: input.userId,
    subscriptionId: input.subscriptionId,
    groupId: input.groupId,
    planId: input.planId,
    amount: fingerprintMoney(input.amount),
    monthlyPrice: fingerprintMoney(input.monthlyPrice),
    expiresAt: String(input.expiresAt || ''),
    paymentType: String(input.paymentType || '').trim(),
    tierRevision: String(input.tierRevision || '').trim(),
  })
}

function readResetCardCheckoutAttempt(raw: string | null | undefined): ResetCardCheckoutAttempt | null {
  if (!raw) return null
  try {
    const parsed = JSON.parse(raw) as unknown
    if (!parsed || typeof parsed !== 'object') return null
    const candidate = parsed as Partial<ResetCardCheckoutAttempt>
    if (
      typeof candidate.fingerprint !== 'string'
      || !candidate.fingerprint
      || typeof candidate.idempotencyKey !== 'string'
      || !candidate.idempotencyKey
      || (candidate.orderId != null && (!Number.isSafeInteger(candidate.orderId) || candidate.orderId <= 0))
    ) {
      return null
    }
    return {
      fingerprint: candidate.fingerprint,
      idempotencyKey: candidate.idempotencyKey,
      ...(candidate.orderId ? { orderId: candidate.orderId } : {}),
    }
  } catch {
    return null
  }
}

function resetCardResumeIdempotencyHash(token: string): string {
  try {
    const parts = token.trim().split('.')
    if (parts.length !== 2 || !parts[0] || !parts[1]) return ''
    const encoded = parts[0].replace(/-/g, '+').replace(/_/g, '/')
    const padded = encoded.padEnd(Math.ceil(encoded.length / 4) * 4, '=')
    const binary = globalThis.atob(padded)
    const bytes = Uint8Array.from(binary, char => char.charCodeAt(0))
    const claims = JSON.parse(new TextDecoder().decode(bytes)) as Record<string, unknown>
    const hash = typeof claims.ikh === 'string' ? claims.ikh.trim().toLowerCase() : ''
    return claims.tk === 'wechat_payment_resume'
      && claims.ot === 'reset_card'
      && /^[a-f0-9]{64}$/.test(hash)
      ? hash
      : ''
  } catch {
    return ''
  }
}

async function sha256Hex(value: string): Promise<string> {
  const subtle = globalThis.crypto?.subtle
  if (!subtle) return ''
  try {
    const digest = await subtle.digest('SHA-256', new TextEncoder().encode(value.trim()))
    return Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, '0')).join('')
  } catch {
    return ''
  }
}

/**
 * OAuth deliberately keeps the raw reset-card idempotency key out of the URL.
 * Match the browser attempt to the hash inside the signed resume token so the
 * eventual order id can still be bound and cleared after a terminal result.
 * The backend remains the authority that verifies the token signature.
 */
export async function matchResetCardCheckoutAttemptForResume(
  storage: AttemptStorage,
  resumeToken: string,
  key = RESET_CARD_CHECKOUT_ATTEMPT_STORAGE_KEY,
): Promise<ResetCardCheckoutAttempt | null> {
  const expectedHash = resetCardResumeIdempotencyHash(resumeToken)
  const attempt = readResetCardCheckoutAttempt(storage.getItem(key))
  if (!expectedHash || !attempt) return null
  const actualHash = await sha256Hex(attempt.idempotencyKey)
  return actualHash === expectedHash ? attempt : null
}

export function getOrCreateResetCardCheckoutAttempt(
  storage: AttemptStorage,
  input: ResetCardCheckoutAttemptInput,
  createKey: () => string,
  key = RESET_CARD_CHECKOUT_ATTEMPT_STORAGE_KEY,
): ResetCardCheckoutAttempt {
  const fingerprint = createResetCardCheckoutFingerprint(input)
  const current = readResetCardCheckoutAttempt(storage.getItem(key))
  if (current?.fingerprint === fingerprint) return current

  const next: ResetCardCheckoutAttempt = {
    fingerprint,
    idempotencyKey: createKey(),
  }
  // This write intentionally happens before the caller starts the POST. A
  // response-loss retry must replay the same server-side idempotency lease.
  storage.setItem(key, JSON.stringify(next))
  return next
}

export function recordResetCardCheckoutOrder(
  storage: AttemptStorage,
  attempt: ResetCardCheckoutAttempt,
  orderId: number,
  key = RESET_CARD_CHECKOUT_ATTEMPT_STORAGE_KEY,
): void {
  if (!Number.isSafeInteger(orderId) || orderId <= 0) return
  const current = readResetCardCheckoutAttempt(storage.getItem(key))
  if (
    !current
    || current.fingerprint !== attempt.fingerprint
    || current.idempotencyKey !== attempt.idempotencyKey
  ) {
    return
  }
  storage.setItem(key, JSON.stringify({ ...current, orderId }))
}

export function clearResetCardCheckoutAttempt(
  storage: AttemptStorage,
  match: Pick<ResetCardCheckoutAttempt, 'orderId'>,
  key = RESET_CARD_CHECKOUT_ATTEMPT_STORAGE_KEY,
): void {
  if (!Number.isSafeInteger(match.orderId) || !match.orderId || match.orderId <= 0) return
  const current = readResetCardCheckoutAttempt(storage.getItem(key))
  if (current?.orderId !== match.orderId) return
  storage.removeItem(key)
}
