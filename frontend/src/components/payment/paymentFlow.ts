import type {
  CreateOrderRequest,
  CreateOrderResult,
  MethodLimit,
  OrderType,
  PaymentDiscountSnapshot,
  WechatJSAPIPayload,
  WechatOAuthInfo,
} from '@/types/payment'

export const PAYMENT_RECOVERY_STORAGE_KEY = 'payment.recovery.current'
export const PAYMENT_CANCELLATION_STORAGE_KEY = 'payment.cancellation.pending.v1'
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
  | 'checkout_frame'
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
  /** Server-issued Alipay checkout URL used by the shared QR surface. */
  checkoutFrameUrl?: string
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
  /** Reset-card choices retained for OAuth/session recovery only. */
  resetCardQuantity?: number
  resetCardUseOnPurchase?: boolean
  /** Display-only server quote retained while a provider flow is in progress. */
  paymentDiscount?: PaymentDiscountSnapshot
  /** The user closed this checkout and the cancel request still needs silent retry. */
  cancellationRequested?: boolean
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
  validityDays?: number
  paymentType: string
  tierRevision?: string
  quantity?: number
  useOnPurchase?: boolean
  couponCode?: string
  couponRevision?: string
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
  paymentDiscount?: PaymentDiscountSnapshot
  resetCardQuantity?: number
  resetCardUseOnPurchase?: boolean
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
  resetCardQuantity?: number
  resetCardUseOnPurchase?: boolean
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
const ALIPAY_CHECKOUT_FRAME_HOSTS = new Set([
  'openapi.alipay.com',
  'openapi-sandbox.dl.alipaydev.com',
])
const ALIPAY_NATIVE_QR_HOSTS = new Set([
  'qr.alipay.com',
  'qr.alipaydev.com',
])
const ALIPAY_HOSTED_CHECKOUT_HOSTS = new Set([
  ...ALIPAY_CHECKOUT_FRAME_HOSTS,
  'pay.totools.cn',
])
const MAX_ALIPAY_CHECKOUT_FRAME_URL_LENGTH = 16384
const MAX_ALIPAY_HOSTED_CHECKOUT_URL_LENGTH = 16384

function readSingleSearchParam(params: URLSearchParams, name: string): string | null {
  const values = params.getAll(name)
  return values.length === 1 ? values[0] : null
}

function hasUnsafeUrlCharacters(value: string): boolean {
  return Array.from(value).some((character) => {
    const code = character.charCodeAt(0)
    return code <= 0x1f || code === 0x7f || character.trim() === ''
  })
}

/**
 * The backend is authoritative for the signed checkout URL. This browser-side
 * check only prevents an altered recovery value from becoming an iframe src.
 */
export function validateAlipayCheckoutFrameUrl(value: unknown): string {
  if (typeof value !== 'string') return ''
  const raw = value
  if (
    !raw
    || raw.length > MAX_ALIPAY_CHECKOUT_FRAME_URL_LENGTH
    || raw !== raw.trim()
    || hasUnsafeUrlCharacters(raw)
  ) return ''

  let url: URL
  try {
    url = new URL(raw)
  } catch {
    return ''
  }

  const rawAuthority = raw.match(/^https:\/\/([^/?#]+)/i)?.[1]?.toLowerCase()
  if (
    url.protocol !== 'https:'
    || !ALIPAY_CHECKOUT_FRAME_HOSTS.has(url.hostname)
    || !rawAuthority
    || rawAuthority !== url.hostname
    || url.username
    || url.password
    || url.port
    || url.hash
    || url.pathname !== '/gateway.do'
  ) {
    return ''
  }

  const method = readSingleSearchParam(url.searchParams, 'method')
  const bizContent = readSingleSearchParam(url.searchParams, 'biz_content')
  const signType = readSingleSearchParam(url.searchParams, 'sign_type')
  const sign = readSingleSearchParam(url.searchParams, 'sign')
  if (method !== 'alipay.trade.page.pay' || signType !== 'RSA2' || !sign || sign.trim() !== sign || !bizContent) {
    return ''
  }

  try {
    const biz = JSON.parse(bizContent) as Record<string, unknown>
    if (
      !biz
      || Array.isArray(biz)
      || String(biz.qr_pay_mode) !== '4'
      || (String(biz.qrcode_width) !== '220' && String(biz.qrcode_width) !== '224')
    ) {
      return ''
    }
  } catch {
    return ''
  }

  // Keep the original signed bytes intact; URL serialization can change query encoding.
  return raw
}

function validateAlipayHTTPSURL(
  value: unknown,
  hosts: ReadonlySet<string>,
  maxLength: number,
  pathValidator?: (url: URL) => boolean,
): string {
  if (typeof value !== 'string') return ''
  const raw = value
  if (
    !raw
    || raw.length > maxLength
    || raw !== raw.trim()
    || hasUnsafeUrlCharacters(raw)
  ) return ''

  let url: URL
  try {
    url = new URL(raw)
  } catch {
    return ''
  }

  const rawAuthority = raw.match(/^https:\/\/([^/?#]+)/i)?.[1]?.toLowerCase()
  if (
    url.protocol !== 'https:'
    || !url.hostname
    || !hosts.has(url.hostname.toLowerCase())
    || !rawAuthority
    || rawAuthority !== url.hostname.toLowerCase()
    || url.username
    || url.password
    || url.port
    || url.hash
    || !url.pathname
    || url.pathname === '/'
  ) {
    return ''
  }

  if (pathValidator && !pathValidator(url)) return ''

  return raw
}

function isValidAlipayHostedCheckoutPath(url: URL): boolean {
  const host = url.hostname.toLowerCase()
  if (host === 'pay.totools.cn') {
    return url.pathname.startsWith('/checkout/') && url.pathname.length > '/checkout/'.length
  }
  return url.pathname === '/gateway.do'
}

/** Validate the official HTTPS payload returned by Alipay precreate. */
export function validateAlipayQRCode(value: unknown): string {
  return validateAlipayHTTPSURL(value, ALIPAY_NATIVE_QR_HOSTS, MAX_ALIPAY_HOSTED_CHECKOUT_URL_LENGTH)
}

/**
 * Validate a server-issued hosted checkout URL before using it as QR content.
 * The URL is a fallback presentation only: payment completion still comes
 * from the order status poll. Keeping this separate from the native QR field
 * lets Alipay page-pay work in the same embedded QR surface without placing
 * the provider document in an iframe.
 */
export function validateAlipayHostedCheckoutUrl(value: unknown): string {
  return validateAlipayHTTPSURL(
    value,
    ALIPAY_HOSTED_CHECKOUT_HOSTS,
    MAX_ALIPAY_HOSTED_CHECKOUT_URL_LENGTH,
    isValidAlipayHostedCheckoutPath,
  )
}

/**
 * Return the value that the Alipay QR renderer should encode for one order.
 * Native qr_code data (https://qr.alipay.com/...) is strictly required.
 * Prohibits encoding page.pay hosted checkout URLs or iframe frame URLs as a QR code string.
 */
export function resolveAlipayQRCode(
  result: Pick<CreateOrderResult, 'qr_code' | 'pay_url' | 'checkout_frame_url'>,
  _options: { allowHostedCheckout?: boolean } = {},
): string {
  return validateAlipayQRCode(result.qr_code)
}

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
  if (input.orderType === 'reset_card') {
    const quantity = Number(input.resetCardQuantity)
    if (Number.isSafeInteger(quantity) && quantity >= 1 && quantity <= 99) {
      payload.reset_card_quantity = quantity
    }
    if (input.resetCardUseOnPurchase === true) {
      payload.reset_card_use_on_purchase = true
    }
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
  const nativeQRCode = String(result.qr_code || '').trim()
  const effectiveQRCode = visibleMethod === 'alipay'
    ? resolveAlipayQRCode(result)
    : nativeQRCode
  const payUrl = visibleMethod === 'alipay'
    ? validateAlipayHostedCheckoutUrl(result.pay_url) || validateAlipayCheckoutFrameUrl(result.pay_url)
    : result.pay_url || ''
  const baseState = createPaymentRecoverySnapshot({
    orderId: result.order_id,
    amount: result.amount,
    qrCode: effectiveQRCode,
    expiresAt: result.expires_at || '',
    paymentType: visibleMethod,
    payUrl,
    checkoutFrameUrl: validateAlipayCheckoutFrameUrl(result.checkout_frame_url),
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
    ...(context.orderType === 'reset_card' && Number.isSafeInteger(context.resetCardQuantity) && context.resetCardQuantity! >= 1 && context.resetCardQuantity! <= 99
      ? { resetCardQuantity: context.resetCardQuantity }
      : {}),
    ...(context.orderType === 'reset_card' && context.resetCardUseOnPurchase === true
      ? { resetCardUseOnPurchase: true }
      : {}),
    paymentDiscount: result.payment_discount ?? context.paymentDiscount,
  }, context.now)

  const normalizedStatus = String(result.status || '').trim().toUpperCase()
  if (normalizedStatus === 'PENDING') {
    const deadline = Date.parse(baseState.expiresAt)
    // A pending order has no safe browser-side launch without a trustworthy
    // deadline. A malformed response must return to the normal error path,
    // while a past deadline stays in the status shell for server verification.
    if (!baseState.expiresAt || !Number.isFinite(deadline)) {
      return { kind: 'unhandled', paymentState: baseState, recovery: baseState }
    }
    if (deadline <= (context.now ?? Date.now())) {
      return { kind: 'status_waiting', paymentState: baseState, recovery: baseState }
    }
  }

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

  // 1. 码串绘制: A native scannable QR payload is authoritative (WeChat Native or Alipay precreate).
  if (effectiveQRCode) {
    const paymentState = baseState.qrCode === effectiveQRCode
      ? baseState
      : { ...baseState, qrCode: effectiveQRCode }
    return { kind: 'qr_waiting', paymentState, recovery: paymentState }
  }

  // 2. iframe 收银台: Desktop Alipay page-pay with qr_pay_mode=4 is embedded via iframe.
  // Prohibit converting page.pay URLs into Canvas QR codes.
  if (visibleMethod === 'alipay' && !context.isMobile && baseState.checkoutFrameUrl) {
    return { kind: 'checkout_frame', paymentState: baseState, recovery: baseState }
  }

  // 3. 页面跳转: Hosted redirect or popup waiting card.
  if (baseState.payUrl) {
    return { kind: 'redirect_waiting', paymentState: baseState, recovery: baseState }
  }

  // An idempotent replay can legitimately return an existing local order after
  // it has completed, expired, or been fenced from another provider launch.
  // Keep that authenticated order in the status shell so the client asks the
  // server for the authoritative outcome instead of treating it as a launch
  // error or creating another provider transaction.
  if (normalizedStatus && normalizedStatus !== 'PENDING' && Number.isSafeInteger(result.order_id) && result.order_id > 0) {
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

function normalizePaymentDiscount(value: unknown): PaymentDiscountSnapshot | undefined {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined
  const candidate = value as Partial<PaymentDiscountSnapshot>
  const codeID = candidate.code_id
  if (
    !Number.isSafeInteger(codeID) || typeof codeID !== 'number' || codeID <= 0
    || typeof candidate.code !== 'string'
    || typeof candidate.original_amount !== 'string'
    || typeof candidate.discount_amount !== 'string'
    || typeof candidate.pay_amount !== 'string'
    || typeof candidate.currency !== 'string'
  ) {
    return undefined
  }
  return {
    code_id: codeID,
    code: candidate.code,
    original_amount: candidate.original_amount,
    discount_amount: candidate.discount_amount,
    pay_amount: candidate.pay_amount,
    currency: candidate.currency,
  }
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
    || (parsed.checkoutFrameUrl != null && typeof parsed.checkoutFrameUrl !== 'string')
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
    || (parsed.resetCardQuantity != null && (!Number.isSafeInteger(parsed.resetCardQuantity) || parsed.resetCardQuantity < 1 || parsed.resetCardQuantity > 99))
    || (parsed.resetCardUseOnPurchase != null && typeof parsed.resetCardUseOnPurchase !== 'boolean')
    || (parsed.cancellationRequested != null && typeof parsed.cancellationRequested !== 'boolean')
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
    checkoutFrameUrl: validateAlipayCheckoutFrameUrl(parsed.checkoutFrameUrl),
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
    ...(parsed.resetCardQuantity ? { resetCardQuantity: parsed.resetCardQuantity } : {}),
    ...(parsed.resetCardUseOnPurchase === true ? { resetCardUseOnPurchase: true } : {}),
    paymentDiscount: normalizePaymentDiscount(parsed.paymentDiscount),
    ...(parsed.cancellationRequested === true ? { cancellationRequested: true } : {}),
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

type CancellationStorage = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>

function readQueuedPaymentCancellations(storage: CancellationStorage, key = PAYMENT_CANCELLATION_STORAGE_KEY): number[] {
  const raw = storage.getItem(key)
  if (!raw) return []
  try {
    const parsed = JSON.parse(raw) as unknown
    if (!Array.isArray(parsed)) return []
    return [...new Set(parsed.filter((value): value is number => Number.isSafeInteger(value) && value > 0))]
  } catch {
    return []
  }
}

/** Keep a cancellation request durable when a dialog is closed before HTTP settles. */
export function queuePaymentCancellation(storage: CancellationStorage, orderId: number, key = PAYMENT_CANCELLATION_STORAGE_KEY): void {
  if (!Number.isSafeInteger(orderId) || orderId <= 0) return
  const queued = readQueuedPaymentCancellations(storage, key)
  if (!queued.includes(orderId)) queued.push(orderId)
  storage.setItem(key, JSON.stringify(queued.slice(-MAX_RECOVERY_ENTRIES)))
}

export function clearQueuedPaymentCancellation(storage: CancellationStorage, orderId: number, key = PAYMENT_CANCELLATION_STORAGE_KEY): void {
  const queued = readQueuedPaymentCancellations(storage, key).filter(id => id !== orderId)
  if (queued.length === 0) storage.removeItem(key)
  else storage.setItem(key, JSON.stringify(queued))
}

export function readQueuedPaymentCancellationIds(storage: CancellationStorage, key = PAYMENT_CANCELLATION_STORAGE_KEY): number[] {
  return readQueuedPaymentCancellations(storage, key)
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
    version: 2,
    userId: input.userId,
    subscriptionId: input.subscriptionId,
    groupId: input.groupId,
    planId: input.planId,
    amount: fingerprintMoney(input.amount),
    monthlyPrice: fingerprintMoney(input.monthlyPrice),
    expiresAt: input.validityDays ? undefined : String(input.expiresAt || ''),
    ...(input.validityDays ? { validityDays: input.validityDays } : {}),
    paymentType: String(input.paymentType || '').trim(),
    tierRevision: String(input.tierRevision || '').trim(),
    quantity: Number.isSafeInteger(input.quantity) && input.quantity! >= 1 && input.quantity! <= 99 ? input.quantity : 1,
    useOnPurchase: input.useOnPurchase === true,
    couponCode: String(input.couponCode || '').trim(),
    couponRevision: String(input.couponRevision || '').trim(),
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

/** Discard only an unbound response-loss retry when checkout choices change. */
export function discardResetCardCheckoutAttemptForSelectionChange(
  storage: AttemptStorage,
  key = RESET_CARD_CHECKOUT_ATTEMPT_STORAGE_KEY,
): void {
  const current = readResetCardCheckoutAttempt(storage.getItem(key))
  if (!current?.orderId) storage.removeItem(key)
}
