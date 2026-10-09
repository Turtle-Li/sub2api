import { beforeEach, describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({ post: vi.fn() }))

vi.mock('@/api/client', () => ({ apiClient: { post } }))

import { adminPaymentAPI } from '@/api/admin/payment'

describe('admin collection payment API', () => {
  beforeEach(() => {
    post.mockReset().mockResolvedValue({ data: {} })
  })

  it('posts the exact fen-and-method contract with its idempotency key', async () => {
    await adminPaymentAPI.createCollectionOrder({ amount_fen: 43_200, payment_type: 'wxpay' }, 'admin-collection-11111111-1111-4111-8111-111111111111')

    expect(post).toHaveBeenCalledWith('/admin/payment/collection/orders', {
      amount_fen: 43_200,
      payment_type: 'wxpay',
    }, {
      headers: { 'Idempotency-Key': 'admin-collection-11111111-1111-4111-8111-111111111111' },
    })
  })
})
