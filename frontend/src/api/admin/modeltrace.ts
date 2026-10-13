import { apiClient } from '../client'
import type { AccountPlatform, AccountType } from '@/types'

export interface ModelTraceRecord {
  detected_model: string
  probability: number
  latency_ms: number
  tested_at: string
  error?: string
  requests?: ModelTraceRequestRecord[]
}

export interface ModelTraceRequestRecord {
  id: string
  started_at: string
  endpoint?: string
  latency_ms: number
  first_token_ms?: number
  input_tokens: number
  output_tokens: number
  usage_reported: boolean
  error?: string
  valid_sample: boolean
}

export interface ModelTraceModelConfig {
  latest?: ModelTraceRecord
  history?: ModelTraceRecord[]
  task?: ModelTraceTask
}

export interface ModelTraceTask {
  id: string
  status: 'queued' | 'running' | 'completed' | 'failed'
  created_at: string
  updated_at: string
  error?: string
}

export interface ModelTraceMonitorItem {
  account_id: number
  account_name: string
  platform: AccountPlatform
  type: AccountType
  last_run_at?: string
  models: Record<string, ModelTraceModelConfig>
}

export interface UpdateModelTraceConfigRequest {
  models: string[]
}

export interface ModelTraceGlobalConfig {
  enabled: boolean
  probe_count: number
  interval_seconds: number
  reasoning_effort: string
  cron_expression: string
}

export const modelTraceAPI = {
  async listMonitors(): Promise<ModelTraceMonitorItem[]> {
    const { data } = await apiClient.get<ModelTraceMonitorItem[]>('/admin/modeltrace/monitors')
    return data
  },
  async getGlobalConfig(): Promise<ModelTraceGlobalConfig> {
    const { data } = await apiClient.get<ModelTraceGlobalConfig>('/admin/modeltrace/global-config')
    return data
  },
  async updateGlobalConfig(cfg: ModelTraceGlobalConfig): Promise<{ success: boolean; config: ModelTraceGlobalConfig }> {
    const { data } = await apiClient.put<{ success: boolean; config: ModelTraceGlobalConfig }>('/admin/modeltrace/global-config', cfg)
    return data
  },
  async updateAccountConfig(accountId: number, reqData: UpdateModelTraceConfigRequest): Promise<void> {
    await apiClient.put(`/admin/modeltrace/accounts/${accountId}`, reqData)
  },
  async removeAccountMonitor(accountId: number): Promise<void> {
    await apiClient.delete(`/admin/modeltrace/accounts/${accountId}`)
  },
  async probeModel(accountId: number, modelId: string): Promise<ModelTraceTask> {
    const { data } = await apiClient.post<ModelTraceTask>(`/admin/modeltrace/accounts/${accountId}/models/run`, null, { params: { model: modelId } })
    return data
  },
}

export default modelTraceAPI
