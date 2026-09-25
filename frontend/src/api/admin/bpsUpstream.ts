import { apiClient } from '../client'

export interface BPSUpstreamConfig {
  enabled: boolean
  account_ids: number[]
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

export type BPSOutcome = 'success' | 'fallback' | 'skipped' | 'error_after_output'

export interface BPSEvent {
  time: string
  account_id: number
  model: string
  outcome: BPSOutcome
  reason?: string
  detail?: string
  status_code?: number
  requested_effort?: string
  applied_effort?: string
  duration_ms?: number
}

export interface BPSUpstreamOverview {
  config: BPSUpstreamConfig
  policy: BPSUpstreamPolicy
  accounts: BPSUpstreamAccount[]
  unlisted_stats: BPSAccountStats[]
  events: BPSEvent[]
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
