import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, put } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))

vi.mock('@/api/client', () => ({ apiClient: { get, put } }))

import { adminPaymentAPI } from '@/api/admin/payment'

describe('admin reset-card tier policy API', () => {
  beforeEach(() => {
    get.mockReset()
    put.mockReset()
    get.mockResolvedValue({ data: [] })
    put.mockResolvedValue({ data: {} })
  })

  it('lists group policies and writes the stable type and positive tier to the group endpoint', async () => {
    const payload = { family_key: 'gpt_standard', tier_rank: 2 }

    await adminPaymentAPI.getResetCardTierPolicies()
    await adminPaymentAPI.updateResetCardTierPolicy(41, payload)

    expect(get).toHaveBeenCalledWith('/admin/payment/reset-card-tiers')
    expect(put).toHaveBeenCalledWith('/admin/payment/reset-card-tiers/41', payload)
  })
})
