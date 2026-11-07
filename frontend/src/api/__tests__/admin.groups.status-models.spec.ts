import { describe, expect, it, vi } from 'vitest'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get } }))
import { getModelAllowlistCandidates } from '@/api/admin/groups'

describe('group status model discovery', () => {
  it('requests available text models for the exact group without a platform override', async () => {
    get.mockResolvedValueOnce({ data: { models: ['deepseek-chat'] } })
    await expect(getModelAllowlistCandidates(31, undefined, true)).resolves.toEqual(['deepseek-chat'])
    expect(get).toHaveBeenLastCalledWith('/admin/groups/31/model-allowlist-candidates', { params: { available_only: true } })
    get.mockResolvedValueOnce({ data: { models: ['gpt-text'] } })
    await getModelAllowlistCandidates(6, 'openai')
    expect(get).toHaveBeenLastCalledWith('/admin/groups/6/model-allowlist-candidates', { params: { platform: 'openai' } })
  })
})
