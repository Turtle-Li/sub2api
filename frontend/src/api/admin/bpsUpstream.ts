import { apiClient } from '../client'

export interface BPSUpstreamConfig {
  enabled: boolean
  account_ids: number[]
  /** 声明实时联网搜索的请求也走 BPS（省略搜索工具），默认回原路径 */
  live_search: boolean
}

export interface BPSUpstreamPolicy {
  models: string[]
  breaker_threshold: number
  breaker_open_seconds: number
  immediate_breaker_status: number[]
}

export interface BPSAccountStats {
  account_id: number
  successes: number
  fallbacks: number
  errors_after_output: number
  skipped?: Record<string, number>
  last_success_at?: string
  last_failure_at?: string
  last_failure_reason?: string
  last_status_code?: number
  breaker_open_until?: string
  breaker_failures?: number
}

export interface BPSUpstreamAccount {
  id: number
  name: string
  platform: string
  type: string
  status: string
  schedulable: boolean
  eligible: boolean
  missing: boolean
  stats?: BPSAccountStats
}

export interface BPSUpstreamOverview {
  config: BPSUpstreamConfig
  policy: BPSUpstreamPolicy
  accounts: BPSUpstreamAccount[]
  unlisted_stats: BPSAccountStats[]
  monitor_started_at: string
  now: string
}

export async function getOverview(): Promise<BPSUpstreamOverview> {
  const { data } = await apiClient.get<BPSUpstreamOverview>('/admin/bps-upstream')
  return data
}

export async function updateConfig(config: BPSUpstreamConfig): Promise<BPSUpstreamConfig> {
  const { data } = await apiClient.put<BPSUpstreamConfig>('/admin/bps-upstream/config', config)
  return data
}

export async function resetBreaker(accountID: number): Promise<void> {
  await apiClient.post(`/admin/bps-upstream/accounts/${accountID}/reset-breaker`)
}
