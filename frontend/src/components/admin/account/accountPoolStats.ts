import type { AccountPoolStats } from '@/api/admin/accountPools'

export type AccountPoolStatusKey =
  | 'normal'
  | 'rate_limited'
  | 'temp_unschedulable'
  | 'unschedulable'
  | 'error'
  | 'inactive'

export interface AccountPoolStatusSegment {
  key: AccountPoolStatusKey
  /** Account list `status` filter value that yields exactly this bucket. */
  filter: string
  count: number
  labelKey: string
  barClass: string
  textClass: string
}

// Buckets are disjoint and mirror the backend list status filters, so
// clicking a bucket in the pool modal lists exactly `count` accounts.
const SEGMENTS: Array<Omit<AccountPoolStatusSegment, 'count'>> = [
  { key: 'normal', filter: 'active', labelKey: 'admin.accounts.pools.stats.normal', barClass: 'bg-emerald-500', textClass: 'text-emerald-600 dark:text-emerald-400' },
  { key: 'rate_limited', filter: 'rate_limited', labelKey: 'admin.accounts.pools.stats.rateLimited', barClass: 'bg-amber-400', textClass: 'text-amber-600 dark:text-amber-400' },
  { key: 'temp_unschedulable', filter: 'temp_unschedulable', labelKey: 'admin.accounts.pools.stats.tempUnschedulable', barClass: 'bg-orange-400', textClass: 'text-orange-600 dark:text-orange-400' },
  { key: 'unschedulable', filter: 'unschedulable', labelKey: 'admin.accounts.pools.stats.unschedulable', barClass: 'bg-gray-400', textClass: 'text-gray-500 dark:text-gray-400' },
  { key: 'error', filter: 'error', labelKey: 'admin.accounts.pools.stats.error', barClass: 'bg-red-500', textClass: 'text-red-600 dark:text-red-400' },
  { key: 'inactive', filter: 'inactive', labelKey: 'admin.accounts.pools.stats.inactive', barClass: 'bg-slate-300 dark:bg-slate-600', textClass: 'text-slate-500 dark:text-slate-400' }
]

/** Non-empty status buckets in display order ("normal" is always included). */
export function poolStatusSegments(stats: AccountPoolStats): AccountPoolStatusSegment[] {
  return SEGMENTS
    .map(segment => ({ ...segment, count: stats[segment.key] ?? 0 }))
    .filter(segment => segment.key === 'normal' || segment.count > 0)
}

/** All buckets, including empty ones (used for the modal stat cards). */
export function poolStatusBuckets(stats: AccountPoolStats): AccountPoolStatusSegment[] {
  return SEGMENTS.map(segment => ({ ...segment, count: stats[segment.key] ?? 0 }))
}
