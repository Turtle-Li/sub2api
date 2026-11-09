import type { PlazaGroupAvailability } from '@/api/modelPlaza'

export function cacheCapability(status: PlazaGroupAvailability | null | undefined, now: number): 'verified' | 'pending' | null {
  if (status?.shared_probe || status?.probe_cache_verification_version !== 1 || status.probe_cache_verification_window_hours !== 24 || !status.probe_enabled) return null
  const verified = Date.parse(status.probe_cache_verified_at ?? '')
  return Number.isFinite(verified) && verified <= now && now - verified <= 86400000 ? 'verified' : 'pending'
}

export function quotaNotice(status: PlazaGroupAvailability | null | undefined, now: number) {
  const observed = Date.parse(status?.pool_quota_observed_at ?? '')
  if (!status || !['limited', 'exhausted'].includes(status.pool_quota_state ?? '') || !Number.isFinite(observed) || observed > now + 60000 || now - observed > 90000) return null
  const reset = Date.parse(status.pool_quota_resets_at ?? '')
  const remaining = Number.isFinite(reset) && reset > observed ? Math.max(0, Math.ceil((reset - now) / 1000)) : null
  const clock = remaining === null ? null : [Math.floor(remaining / 3600), Math.floor(remaining / 60) % 60, remaining % 60].map(n => String(n).padStart(2, '0')).join(':')
  return { state: status.pool_quota_state!, remaining, clock }
}
