import { apiClient } from '../client'

// 满血网关监控：free 账号侦察网关 cookie，降智账号轮询各网关，发现满血窗口并持续跟踪。
const BASE = '/admin/codex-gateway-hunter'

export type GatewayTier = 'full' | 'pruned' | 'degraded' | 'unknown' | 'error'

export interface GatewayHunterConfig {
  enabled: boolean
  model: string
  hunter_account_ids: number[]
  tracker_account_ids: number[]
  scout_group_id: number
  scout_proxy_id: number
  probe_proxy_id: number
  workers: number
  min_probe_interval_seconds: number
  scout_interval_seconds: number
  tracker_daily_budget: number
}

export interface GatewayHunterAccount {
  id: number
  name: string
  role: 'hunter' | 'tracker' | 'scout'
  primary_used_percent: number | null
  secondary_used_percent: number | null
  probes_today: number
  retired: boolean
  retired_reason: string
  last_error: string
}

export interface GatewayHunterGateway {
  gateway: string
  first_seen: number
  scouted_at: number
  scout_account_id: number
  probes: number
  hits: number
  last_tier: GatewayTier | ''
  last_probe_at: number
  hot_since: number | null
  hot_accounts: Record<string, GatewayTier>
}

export interface GatewayHunterWindow {
  gateway: string
  start: number
  end: number | null
  duration_seconds: number
  found_by: number
  confirmed_by: number[]
}

export interface GatewayHunterProbe {
  at: number
  phase: 'hunt' | 'track' | 'bare'
  account_id: number
  gateway: string
  http_status: number
  tier: GatewayTier
  answer: string
  ttfb_ms: number
  total_ms: number
  reasoning_tokens: number | null
}

export interface GatewayHunterCounters {
  scouts: number
  scout_errors: number
  hunts: number
  hunt_hits: number
  bare_probes: number
  bare_hits: number
  tracks: number
  track_hits: number
  windows: number
  covered_seconds: number
  running_seconds: number
}

export interface GatewayHunterState {
  generated_at: number
  running: boolean
  leader: boolean
  started_at: number | null
  last_error: string
  config: GatewayHunterConfig
  accounts: GatewayHunterAccount[]
  gateways: GatewayHunterGateway[]
  windows: GatewayHunterWindow[]
  probes: GatewayHunterProbe[]
  counters: GatewayHunterCounters
}

export async function getState(): Promise<GatewayHunterState> {
  const { data } = await apiClient.get<GatewayHunterState>(`${BASE}/state`)
  return data
}

export async function updateConfig(config: GatewayHunterConfig): Promise<GatewayHunterConfig> {
  const { data } = await apiClient.put<GatewayHunterConfig>(`${BASE}/config`, config)
  return data
}

export async function start(): Promise<GatewayHunterState> {
  const { data } = await apiClient.post<GatewayHunterState>(`${BASE}/start`, {})
  return data
}

export async function stop(): Promise<GatewayHunterState> {
  const { data } = await apiClient.post<GatewayHunterState>(`${BASE}/stop`, {})
  return data
}

export const codexGatewayHunterAPI = { getState, updateConfig, start, stop }

export default codexGatewayHunterAPI
