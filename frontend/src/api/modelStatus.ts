import { apiClient } from './client'
import type { PlazaGroupAvailability } from './modelPlaza'

export interface ModelStatusGroup {
  id: number
  name: string
  platform: string
  availability: PlazaGroupAvailability | null
}

export interface ModelStatusResponse {
  generated_at: string
  groups: ModelStatusGroup[]
}

export async function getModelStatus(signal?: AbortSignal): Promise<ModelStatusResponse> {
  const { data } = await apiClient.get<ModelStatusResponse>('/model-status', { signal })
  return data
}
