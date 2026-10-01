import { apiClient } from '../client'

// 降智测试：用已钉入 turn-state 票据的账号直接发起一次提问，查看回答质量。
const BASE = '/admin/codex-degradation-probes'

export const NO_PINNED_STATE_CODE = 'CODEX_DEGRADATION_PROBE_NO_PINNED_STATE'

export type DegradationProbeStatus = 'queued' | 'running' | 'succeeded' | 'failed'

export interface DegradationProbeResult {
  id: number
  batch_id: string
  account_id: number
  account_name: string
  path: 'turn_state'
  model: string
  effort: string
  applied_effort: string
  prompt: string
  status: DegradationProbeStatus
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

export interface DegradationProbeRequest {
  account_ids: number[]
  model: string
  effort: string
  prompt: string
}

export async function listProbes(): Promise<DegradationProbeResult[]> {
  const { data } = await apiClient.get<DegradationProbeResult[]>(BASE)
  return data ?? []
}

export async function createProbes(request: DegradationProbeRequest): Promise<DegradationProbeResult[]> {
  const { data } = await apiClient.post<DegradationProbeResult[]>(BASE, request)
  return data ?? []
}

export async function getProbe(id: number): Promise<DegradationProbeResult> {
  const { data } = await apiClient.get<DegradationProbeResult>(`${BASE}/${id}`)
  return data
}

export async function deleteProbe(id: number): Promise<void> {
  await apiClient.delete(`${BASE}/${id}`)
}

export async function deleteAllProbes(): Promise<number> {
  const { data } = await apiClient.delete<{ deleted: number }>(BASE)
  return data?.deleted ?? 0
}
