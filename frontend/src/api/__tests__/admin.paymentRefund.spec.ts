import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))

vi.mock('@/api/client', () => ({ apiClient: { get, post } }))

import { adminPaymentAPI } from '@/api/admin/payment'

describe('admin payment refund API', () => {
  beforeEach(() => {
    get.mockReset()
    post.mockReset()
    get.mockResolvedValue({ data: {} })
    post.mockResolvedValue({ data: {} })
  })

  it('loads a refund review and submits only its quote revision and reason', async () => {
    const request = { quote_revision: 'review-51', reason: 'Customer cancellation' }

    await adminPaymentAPI.getRefundReview(51)
    await adminPaymentAPI.refundOrder(51, request)

    expect(get).toHaveBeenCalledWith('/admin/payment/orders/51/refund-review')
    expect(post).toHaveBeenCalledWith('/admin/payment/orders/51/refund', request)
  })
})
