import { describe, expect, it, vi } from 'vitest'

const { put } = vi.hoisted(() => ({ put: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { put } }))
import { setPaidFallback } from '@/api/admin/accounts'

describe('account paid fallback API', () => {
  it.each([true, false])('uses the dedicated merge endpoint for %s', async enabled => {
    const result = { account_id: 7, enabled }
    put.mockReset().mockResolvedValue({ data: result })
    await expect(setPaidFallback(7, enabled)).resolves.toEqual(result)
    expect(put).toHaveBeenCalledOnce()
    expect(put).toHaveBeenCalledWith('/admin/accounts/7/paid-fallback', { enabled })
  })
})
