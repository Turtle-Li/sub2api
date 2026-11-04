import type { ShopInputCheck } from '@/types/shop'

interface ShopSessionPreview {
  accountName: string
  check: ShopInputCheck | null
  error: string
}

const text = (value: unknown): string => typeof value === 'string' ? value.trim() : ''
const object = (value: unknown): Record<string, unknown> | null => value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null
const numericDate = (value: unknown): value is number => typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 && value <= 253402300799

function decodeBase64URL(value: string): string {
  if (!/^[A-Za-z0-9_-]+$/.test(value)) throw new Error('Invalid base64url')
  return atob(value.replace(/-/g, '+').replace(/_/g, '/'))
}

function decodeObject(value: string): Record<string, unknown> | null {
  try {
    const bytes = Uint8Array.from(decodeBase64URL(value), char => char.charCodeAt(0))
    return object(JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)))
  } catch { return null }
}

// Untrusted, local display hints only. The server remains authoritative and
// neither this result nor the original content is persisted by this function.
export function previewShopSession(content: string, now = Date.now()): ShopSessionPreview | null {
  if (!content.trim()) return null
  const invalid = (error: string): ShopSessionPreview => ({ accountName: '', check: null, error })
  if (content.length > 262144 || new TextEncoder().encode(content).byteLength > 262144) return invalid('内容过长，请重新复制完整的 Session JSON。')
  let session: Record<string, unknown> | null
  try { session = object(JSON.parse(content)) }
  catch { return invalid('内容不是完整的 JSON，请复制 Session 页面的全部内容。') }
  if (!session) return invalid('请粘贴 Session 页面的完整 JSON 对象。')

  const user = object(session.user) ?? {}
  const account = object(session.account) ?? {}
  const email = text(user.email)
  const warnings: string[] = []
  if (email.length > 320 || !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) warnings.push('未识别到有效的账号邮箱，请确认已登录目标账号并复制全部内容。')

  const token = text(session.accessToken)
  const parts = token.split('.')
  const header = decodeObject(parts[0] ?? '')
  const claims = decodeObject(parts[1] ?? '')
  let signatureValid = false
  try { signatureValid = !!decodeBase64URL(parts[2] ?? '').length }
  catch { /* The warning below contains no credential content. */ }
  if (!token) warnings.push('未找到 accessToken，请复制 Session 页面的全部内容。')
  else if (parts.length !== 3 || !header || !text(header.alg) || text(header.alg).toLowerCase() === 'none' || !claims || !signatureValid) warnings.push('令牌格式不完整或不正确，请重新复制 Session。')

  const auth = object(claims?.['https://api.openai.com/auth']) ?? {}
  const profile = object(claims?.['https://api.openai.com/profile']) ?? {}
  const differs = (first: unknown, second: unknown, ignoreCase = false) => {
    const a = text(first), b = text(second)
    return !!a && !!b && (ignoreCase ? a.toLowerCase() !== b.toLowerCase() : a !== b)
  }
  if (differs(profile.email, user.email, true) || differs(auth.chatgpt_account_id, account.id) || differs(auth.chatgpt_user_id, user.id)) warnings.push('Session 与令牌中的账号信息不一致，请重新登录目标账号后复制。')

  const sessionPlan = text(account.planType).toLowerCase()
  const tokenPlan = text(auth.chatgpt_plan_type).toLowerCase()
  const plan = sessionPlan || tokenPlan
  const planType = ['free', 'plus', 'pro', 'team', 'business', 'enterprise', 'edu', 'go'].includes(plan) ? plan : 'unknown'
  if (planType === 'unknown') warnings.push('未识别到订阅类型，需客服核对。')
  if (sessionPlan && tokenPlan && sessionPlan !== tokenPlan) warnings.push('Session 与令牌中的订阅信息不同，需客服核对。')

  const expiries: number[] = []
  if (claims) {
    if (numericDate(claims.exp) && claims.exp > 0) expiries.push(claims.exp * 1000)
    else warnings.push('令牌有效期格式不正确，请重新复制 Session。')
    if (claims.nbf !== undefined) {
      if (!numericDate(claims.nbf)) warnings.push('令牌生效时间格式不正确，请重新复制 Session。')
      else if (claims.nbf * 1000 > now + 60000) warnings.push('Session 尚未生效，请重新登录后复制。')
    }
  }
  if (session.expires !== undefined && session.expires !== '') {
    const expires = typeof session.expires === 'string' && /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(session.expires) ? Date.parse(session.expires) : NaN
    if (Number.isFinite(expires)) expiries.push(expires)
    else warnings.push('Session 有效期格式不正确，请重新复制。')
  }
  const expiry = expiries.length ? Math.min(...expiries) : undefined
  if (expiry !== undefined && expiry <= now) warnings.push('Session 已过期，请重新登录后复制。')
  return {
    accountName: text(user.name).slice(0, 160),
    check: { account: email.length <= 320 ? email : '', plan_type: planType, ...(expiry !== undefined ? { expires_at: new Date(expiry).toISOString() } : {}), warnings },
    error: '',
  }
}
