import type { Account } from '@/types'

const normalizeUsageRefreshValue = (value: unknown): string => {
  if (value == null) return ''
  return String(value)
}

const normalizeSnapshotRefreshValue = (value: unknown): unknown => {
  if (Array.isArray(value)) {
    return value.map(normalizeSnapshotRefreshValue)
  }
  if (value && typeof value === 'object') {
    return Object.fromEntries(
      Object.entries(value as Record<string, unknown>)
        .filter(([, entry]) => entry !== undefined)
        .sort(([left], [right]) => left.localeCompare(right))
        .map(([key, entry]) => [key, normalizeSnapshotRefreshValue(entry)])
    )
  }
  return value
}

const serializeSnapshotRefreshValue = (value: unknown): string => {
  if (value == null) return ''
  return JSON.stringify(normalizeSnapshotRefreshValue(value)) ?? ''
}

const isNonBlankString = (value: unknown): value is string => (
  typeof value === 'string' && value.trim().length > 0
)

// OpenAI API Key 账号指向已配置额度同步的 codex-proxy-rs 时，后端从 cpr 同步 5h/7d 额度。
export const isCPRQuotaSyncAccount = (account: Pick<Account, 'platform' | 'type' | 'cpr_quota_sync'>): boolean => (
  account.platform === 'openai' &&
  account.type === 'apikey' &&
  account.cpr_quota_sync === true
)

// 展示 Codex 5h/7d 额度窗口的 OpenAI 账号：OAuth 账号，或已配置 cpr 额度同步的 API Key 账号。
export const isOpenAICodexUsageAccount = (account: Pick<Account, 'platform' | 'type' | 'cpr_quota_sync'>): boolean => (
  account.platform === 'openai' && (account.type === 'oauth' || isCPRQuotaSyncAccount(account))
)

export const buildOpenAIUsageRefreshKey = (account: Pick<Account, 'id' | 'platform' | 'type' | 'cpr_quota_sync' | 'updated_at' | 'last_used_at' | 'rate_limit_reset_at' | 'extra'>): string => {
  if (!isOpenAICodexUsageAccount(account)) {
    return ''
  }

  const extra = account.extra ?? {}
  return [
    account.id,
    account.updated_at,
    account.last_used_at,
    account.rate_limit_reset_at,
    extra.codex_usage_updated_at,
    extra.codex_5h_used_percent,
    extra.codex_5h_reset_at,
    extra.codex_5h_reset_after_seconds,
    extra.codex_5h_window_minutes,
    extra.codex_7d_used_percent,
    extra.codex_7d_reset_at,
    extra.codex_7d_reset_after_seconds,
    extra.codex_7d_window_minutes
  ].map(normalizeUsageRefreshValue).join('|')
}

export const buildGrokUsageRefreshKey = (account: Pick<Account, 'platform' | 'extra'>): string => {
  if (account.platform !== 'grok') {
    return ''
  }

  const extra = account.extra ?? {}
  const usageSnapshot = extra.grok_usage_snapshot
  const canonicalTier = (usageSnapshot as Record<string, unknown> | null | undefined)?.subscription_tier
  const legacyQuotaFallback = isNonBlankString(canonicalTier)
    ? undefined
    : extra.grok_quota_snapshot
  return [
    serializeSnapshotRefreshValue(extra.grok_billing_snapshot),
    serializeSnapshotRefreshValue(usageSnapshot),
    serializeSnapshotRefreshValue(legacyQuotaFallback)
  ].join('|')
}
