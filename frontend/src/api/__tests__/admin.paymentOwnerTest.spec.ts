import { beforeEach, describe, expect, it, vi } from 'vitest'

const post = vi.hoisted(() => vi.fn())

vi.mock('@/api/client', () => ({ apiClient: { post } }))

import {
  createOwnerTestOrder,
  isOwnerTestOrderRequest,
  type OwnerTestOrderRequest,
} from '@/api/admin/payment'

const request: OwnerTestOrderRequest = { amount_fen: 1, payment_type: 'alipay' }
const idempotencyKey = 'owner-payment-test-1234567890'

describe('admin owner payment test API', () => {
  beforeEach(() => vi.resetAllMocks())

  it('posts only the approved intent with the required idempotency key', async () => {
    const response = { order_id: 42, status: 'PENDING', pay_url: 'https://pay.example.test/review' }
    post.mockResolvedValue({ data: response })

    await expect(createOwnerTestOrder(request, idempotencyKey)).resolves.toBe(response)
    expect(post).toHaveBeenCalledWith(
      '/admin/payment/owner-test/orders',
      request,
      { headers: { 'Idempotency-Key': idempotencyKey } },
    )
  })

  it('blocks unsupported amount, payment method, extra fields, and malformed request keys before sending a request', async () => {
    expect(isOwnerTestOrderRequest({ amount_fen: 1, payment_type: 'alipay' })).toBe(true)
    expect(isOwnerTestOrderRequest({ amount_fen: 2, payment_type: 'wxpay' })).toBe(true)
    expect(isOwnerTestOrderRequest({ amount_fen: 3, payment_type: 'alipay' })).toBe(false)
    expect(isOwnerTestOrderRequest({ amount_fen: 1, payment_type: 'stripe' })).toBe(false)
    expect(isOwnerTestOrderRequest({ amount_fen: 1, payment_type: 'alipay', user_id: 99 })).toBe(false)

    await expect(createOwnerTestOrder({ amount_fen: 3, payment_type: 'alipay' } as OwnerTestOrderRequest, idempotencyKey)).rejects.toThrow('invalid owner payment test request')
    await expect(createOwnerTestOrder({ amount_fen: 1, payment_type: 'alipay', user_id: 99 } as OwnerTestOrderRequest, idempotencyKey)).rejects.toThrow('invalid owner payment test request')
    await expect(createOwnerTestOrder(request, 'short-key')).rejects.toThrow('invalid owner payment test request')
    expect(post).not.toHaveBeenCalled()
  })
})
