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

export type BPSProbePath = 'bps' | 'native'
export type BPSProbeStatus = 'queued' | 'running' | 'succeeded' | 'failed'

export interface BPSProbeResult {
  id: number
  batch_id: string
  account_id: number
  account_name: string
  path: BPSProbePath
  model: string
  effort: string
  applied_effort: string
  prompt: string
  status: BPSProbeStatus
  /** 仅详情接口返回完整回答；列表只带预览与长度 */
  content?: string
  content_preview: string
  content_length: number
  error_message: string
  input_tokens: number
  output_tokens: number
  reasoning_tokens: number
  duration_ms: number
  created_at: string
  started_at?: string
  finished_at?: string
}

export interface BPSProbeRequest {
  account_ids: number[]
  paths: BPSProbePath[]
  model: string
  effort: string
  prompt: string
}

export async function listProbes(): Promise<BPSProbeResult[]> {
  const { data } = await apiClient.get<BPSProbeResult[]>('/admin/bps-upstream/probes')
  return data
}

export async function createProbes(request: BPSProbeRequest): Promise<BPSProbeResult[]> {
  const { data } = await apiClient.post<BPSProbeResult[]>('/admin/bps-upstream/probes', request)
  return data
}

export async function getProbe(id: number): Promise<BPSProbeResult> {
  const { data } = await apiClient.get<BPSProbeResult>(`/admin/bps-upstream/probes/${id}`)
  return data
}

export async function deleteProbe(id: number): Promise<void> {
  await apiClient.delete(`/admin/bps-upstream/probes/${id}`)
}

export async function deleteAllProbes(): Promise<number> {
  const { data } = await apiClient.delete<{ deleted: number }>('/admin/bps-upstream/probes')
  return data.deleted
}
