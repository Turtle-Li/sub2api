import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiClient: {
    get,
    post,
  },
}))

import { paymentAPI } from '@/api/payment'

describe('payment api', () => {
  beforeEach(() => {
    get.mockReset()
    post.mockReset()
    get.mockResolvedValue({ data: {} })
    post.mockResolvedValue({ data: {} })
  })

  it('keeps legacy public out_trade_no verification for upgrade compatibility', async () => {
    await paymentAPI.verifyOrderPublic('legacy-order-no')

    expect(post).toHaveBeenCalledWith('/payment/public/orders/verify', {
      out_trade_no: 'legacy-order-no',
    })
  })

  it('keeps signed public resume-token resolve endpoint', async () => {
    await paymentAPI.resolveOrderPublicByResumeToken('resume-token-123')

    expect(post).toHaveBeenCalledWith('/payment/public/orders/resolve', {
      resume_token: 'resume-token-123',
    })
  })

  it('uses authenticated order-scoped invoice endpoints', async () => {
    const payload = {
      title_type: 'enterprise' as const,
      title: 'Example Co.',
      tax_identifier: '91310000MA12345678',
      recipient_email: 'finance@example.com',
    }

    await paymentAPI.createInvoiceRequest(51, payload)
    await paymentAPI.getInvoiceRequest(51)

    expect(post).toHaveBeenCalledWith('/payment/orders/51/invoice', payload)
    expect(get).toHaveBeenCalledWith('/payment/orders/51/invoice')
  })
})
