import { apiClient } from '../client'
import type { BatchImageModel, BatchImageModelCapabilities } from '../batchImage'

export type AdminImageModel = Omit<BatchImageModel, 'capabilities'> & {
  capabilities?: BatchImageModelCapabilities | null
}

export interface ImageModelCatalogue {
  models: AdminImageModel[]
  candidates: AdminImageModel[]
  unavailable_reason: string | null
}

export async function getImageModels(groupId: number): Promise<ImageModelCatalogue> {
  const { data } = await apiClient.get<ImageModelCatalogue>(`/admin/groups/${groupId}/image-models`)
  return data
}

const walletGroupRoute = '/admin/image-studio/wallet-group'

export interface ImageStudioWalletConfig {
  group_id: number
  account_ids: number[]
}

export async function getImageStudioWalletGroup(signal?: AbortSignal): Promise<ImageStudioWalletConfig> {
  const { data } = await apiClient.get<ImageStudioWalletConfig>(walletGroupRoute, { signal })
  return data
}

export async function setImageStudioWalletGroup(config: ImageStudioWalletConfig, expected: ImageStudioWalletConfig): Promise<ImageStudioWalletConfig> {
  const { data } = await apiClient.put<ImageStudioWalletConfig>(walletGroupRoute, { config, expected })
  return data
}

export async function setWorkBuddyImagePolicy(accountId: number, accepted: boolean): Promise<boolean> {
  const { data } = await apiClient.patch<{ native_retry_accepted: boolean }>(
    `/admin/accounts/${accountId}/workbuddy-image-policy`, { native_retry_accepted: accepted },
  )
  return data.native_retry_accepted
}
