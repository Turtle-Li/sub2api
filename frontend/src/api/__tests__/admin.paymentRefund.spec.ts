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

  it('loads an omitted or explicit refund review and submits its quote revision and structured reason', async () => {
    const request = {
      quote_revision: 'review-51',
      reason_code: 'customer_request' as const,
      reason_detail: 'Customer cancellation',
    }

    await adminPaymentAPI.getRefundReview(51)
    await adminPaymentAPI.getRefundReview(51, 0.03)
    await adminPaymentAPI.refundOrder(51, request)

    expect(get).toHaveBeenCalledWith('/admin/payment/orders/51/refund-review')
    expect(get).toHaveBeenCalledWith('/admin/payment/orders/51/refund-review', { params: { refund_amount: 0.03 } })
    expect(post).toHaveBeenCalledWith('/admin/payment/orders/51/refund', request)
  })

  it('uses dedicated recovery endpoints without accepting a client refund amount', async () => {
    const confirmation = {
      method_code: 'wechat_transfer' as const,
      external_reference: 'wx-transfer-20260916-1',
      refunded_at: '2026-09-16T04:00:00.000Z',
      evidence_detail: 'Verified recipient and exact transfer amount',
    }

    await adminPaymentAPI.retryRefund(4)
    await adminPaymentAPI.confirmExternalRefund(4, confirmation)

    expect(post).toHaveBeenNthCalledWith(1, '/admin/payment/orders/4/refund/retry')
    expect(post).toHaveBeenNthCalledWith(2, '/admin/payment/orders/4/refund/confirm-external', confirmation)
    expect(confirmation).not.toHaveProperty('amount')
    expect(confirmation).not.toHaveProperty('amount_fen')
  })
})
