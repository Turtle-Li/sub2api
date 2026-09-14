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

  it('loads a refund review and submits its quote revision and structured reason', async () => {
    const request = {
      quote_revision: 'review-51',
      reason_code: 'customer_request' as const,
      reason_detail: 'Customer cancellation',
    }

    await adminPaymentAPI.getRefundReview(51)
    await adminPaymentAPI.refundOrder(51, request)

    expect(get).toHaveBeenCalledWith('/admin/payment/orders/51/refund-review')
    expect(post).toHaveBeenCalledWith('/admin/payment/orders/51/refund', request)
  })
})
