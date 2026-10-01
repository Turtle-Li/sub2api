import { apiClient } from '../client'

// 降智修复：经后端鉴权代理访问本机 turn-state 管理器（白名单路径，响应包成 {code,message,data}）。
const BASE = '/admin/codex-turn-state/api'

export type CodexDegradationVerdict = 'no_record' | 'unknown' | 'handled' | 'degraded'

export interface CodexDegradedRow {
  account_id: number
  account_name: string
  requested_model: string
  sent_model: string
  response_model: string
  count: number
  first_seen: string
  last_seen: string
  ttft_avg_ms: number | null
}

export interface CodexTurnStateModel {
  model: string
  target_len: number
  state_len: number
  valid: boolean
  expired: boolean
  expires_at: string | null
  remaining_minutes: number
  remaining_seconds: number
  cookie_present: boolean
  cookie_valid: boolean
  cookie_remaining_seconds: number
  cookie_expires_at: string | null
  pinned_updated_at: string | null
  length_degraded: boolean
  backoff_seconds: number
  next_probe_seconds: number
  degradation: CodexDegradationVerdict
  recent_degradation: CodexDegradedRow | null
}

export interface CodexTurnStateAccount {
  id: number
  name: string
  refresh_advance_minutes: number
  source: 'config' | 'panel'
  error: string | null
  models: CodexTurnStateModel[]
}

export interface CodexTurnStateSettings {
  refresh_advance_minutes: number
  probing_enabled: boolean
  error: string | null
}

export interface CodexRenewalTiming {
  samples: number
  average_seconds: number | null
  last_seconds: number | null
  since: number | null
  window: number
}

export type CodexProbeJobStatus = 'queued' | 'running' | 'waiting' | 'done' | 'cancelled' | 'error'

export interface CodexProbeJob {
  id: string
  account_id: number
  model: string | null
  force: boolean
  status: CodexProbeJobStatus
  queued_at: number
  started_at: number | null
  finished_at: number | null
  updated: number
  error: string | null
}

export interface CodexTurnStateSnapshot {
  generated_at: number
  probing_enabled: boolean
  settings: CodexTurnStateSettings | null
  renewal_timing: CodexRenewalTiming | null
  read_only: boolean
  degraded_enabled: boolean
  accounts: CodexTurnStateAccount[]
  degraded: CodexDegradedRow[]
  degraded_error: string
  degraded_window: string
  proxy_count: number
  static_proxy_count: number
  dynamic_provider_count: number
  poll_interval_seconds: number
  jobs: CodexProbeJob[]
}

export interface CodexProbeCounts {
  attempts: number
  http_200: number
  state_292: number
  target_hits: number
  persisted: number
  errors: number
  header_ms_total: number
  status_counts: Record<string, number>
  http_200_rate: number
  state_292_rate: number
  target_hit_rate: number
  persisted_rate: number
  persisted_per_target_hit_rate: number
  error_rate: number
  header_ms_average: number
}

export interface CodexProbeStats {
  date_range: { start_day: string | null; end_day: string | null }
  totals: CodexProbeCounts
  by_source: Record<string, CodexProbeCounts>
}

export interface CodexProbeHistoryEntry {
  at: number
  state_len: number
  http_status: number
  header_ms: number
  ok: boolean
  outcome: 'saved' | 'target_hit' | 'miss' | 'legacy'
  source: string
}

/** 键为 `<accountId>:<model>` */
export type CodexProbeHistory = Record<string, CodexProbeHistoryEntry[]>

export type CodexProxySourceType = 'static' | 'rotating' | 'extract'

export interface CodexProxySource {
  id: string
  name: string
  type: CodexProxySourceType | string
  enabled: boolean
  origin: 'base' | 'managed'
  read_only: boolean
  endpoint_count?: number
  count?: number
  status: string
}

export interface CodexProxySourceInput {
  name: string
  type: CodexProxySourceType
  content: string
  username: string
  password: string
}

export interface CodexMonitoredModelInput {
  account_id: number
  name: string
  model: string
  target_state_len: number
}

export const ACTIVE_JOB_STATUSES: readonly CodexProbeJobStatus[] = ['queued', 'running', 'waiting']

export async function getState(): Promise<CodexTurnStateSnapshot> {
  const { data } = await apiClient.get<CodexTurnStateSnapshot>(`${BASE}/state`)
  return data
}

export async function updateSettings(
  patch: { refresh_advance_minutes: number } | { probing_enabled: boolean },
): Promise<CodexTurnStateSettings> {
  const { data } = await apiClient.post<CodexTurnStateSettings>(`${BASE}/settings`, patch)
  return data
}

export async function getDegraded(force = false): Promise<{ degraded: CodexDegradedRow[]; error: string }> {
  const { data } = await apiClient.get<{ degraded: CodexDegradedRow[]; error: string }>(`${BASE}/degraded`, {
    params: force ? { force: 1 } : undefined,
  })
  return data
}

export async function getStats(range: { start_day?: string; end_day?: string } = {}): Promise<CodexProbeStats> {
  const params: Record<string, string> = {}
  if (range.start_day) params.start_day = range.start_day
  if (range.end_day) params.end_day = range.end_day
  const { data } = await apiClient.get<CodexProbeStats>(`${BASE}/stats`, { params })
  return data
}

export async function getHistory(): Promise<CodexProbeHistory> {
  const { data } = await apiClient.get<CodexProbeHistory>(`${BASE}/history`)
  return data ?? {}
}

export async function getJob(id: string): Promise<CodexProbeJob> {
  const { data } = await apiClient.get<CodexProbeJob>(`${BASE}/jobs/${encodeURIComponent(id)}`)
  return data
}

export async function listAccountModels(accountId: number): Promise<string[]> {
  const { data } = await apiClient.get<{ account_id: number; models: string[] }>(`${BASE}/accounts/${accountId}/models`)
  return data?.models ?? []
}

/** 手动探测：入队后返回任务 ID（同一目标已有活动任务时返回该任务）。 */
export async function startProbe(request: { account_id: number; model: string; force: boolean }): Promise<string> {
  const { data } = await apiClient.post<{ job_id: string }>(`${BASE}/probe`, request)
  return data.job_id
}

export async function addMonitoredModel(input: CodexMonitoredModelInput): Promise<void> {
  await apiClient.post(`${BASE}/accounts`, input)
}

/** 移出监控；已有票据保留至到期。 */
export async function removeMonitoredModel(accountId: number, model: string): Promise<void> {
  await apiClient.delete(`${BASE}/accounts/${accountId}/${encodeURIComponent(model)}`)
}

export async function listProxySources(): Promise<CodexProxySource[]> {
  const { data } = await apiClient.get<{ sources: CodexProxySource[] } | CodexProxySource[]>(`${BASE}/proxy-sources`)
  return Array.isArray(data) ? data : data?.sources ?? []
}

export async function createProxySource(input: CodexProxySourceInput): Promise<void> {
  await apiClient.post(`${BASE}/proxy-sources`, input)
}

export async function setProxySourceEnabled(id: string, enabled: boolean): Promise<void> {
  await apiClient.post(`${BASE}/proxy-sources/${encodeURIComponent(id)}/enabled`, { enabled })
}

export async function deleteProxySource(id: string): Promise<void> {
  await apiClient.delete(`${BASE}/proxy-sources/${encodeURIComponent(id)}`)
}
