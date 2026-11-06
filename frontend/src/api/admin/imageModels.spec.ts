import { beforeEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '../client'
import { getImageModels, setWorkBuddyImagePolicy } from './imageModels'

vi.mock('../client', () => ({ apiClient: { get: vi.fn(), patch: vi.fn() } }))
beforeEach(() => vi.resetAllMocks())

describe('admin image API', () => {
  it('reads the group catalogue through the panel envelope', async () => {
    const catalogue = { models: [], candidates: [], unavailable_reason: 'batch_image_disabled' }
    vi.mocked(apiClient.get).mockResolvedValue({ data: catalogue })
    expect(await getImageModels(7)).toEqual(catalogue)
    expect(apiClient.get).toHaveBeenCalledWith('/admin/groups/7/image-models')
  })
  it('patches only native retry acceptance without credentials or other extra keys', async () => {
    vi.mocked(apiClient.patch).mockResolvedValue({ data: { native_retry_accepted: false } })
    expect(await setWorkBuddyImagePolicy(20, false)).toBe(false)
    expect(apiClient.patch).toHaveBeenCalledWith('/admin/accounts/20/workbuddy-image-policy', { native_retry_accepted: false })
  })
})
