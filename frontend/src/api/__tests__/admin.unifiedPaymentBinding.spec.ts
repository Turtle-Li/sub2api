import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post, remove } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  remove: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiClient: { get, post, delete: remove },
}))

import {
  bindUnifiedPayment,
  getUnifiedPaymentBinding,
  useManualUnifiedPayment,
} from '@/api/admin/unifiedPaymentBinding'

const endpoint = '/admin/settings/unified-payment-binding'
const status = {
  base_url: 'https://pay.totools.cn',
  app_id: 'app.sub2.sandbox',
  environment: 'sandbox',
  return_url: 'https://www.turtleligpt.com/payment/result',
  webhook_url: 'https://api.turtleligpt.com/api/v1/payment/webhook/unified',
  revision: 7,
  configured: true,
  bootstrap_ready: true,
  runtime_enabled: false,
  pending_restart: true,
}

describe('unified payment binding admin API', () => {
  beforeEach(() => {
    get.mockReset()
    post.mockReset()
    remove.mockReset()
  })

  it('reads the binding status without inventing callback destinations', async () => {
    get.mockResolvedValue({ data: status })

    await expect(getUnifiedPaymentBinding()).resolves.toEqual(status)
    expect(get).toHaveBeenCalledWith(endpoint)
  })

  it('sends the save body and idempotency key', async () => {
    post.mockResolvedValue({ data: status })

    await expect(bindUnifiedPayment('https://pay.totools.cn', 'binding-code', 7, 'binding-save-0001')).resolves.toEqual(status)
    expect(post).toHaveBeenCalledWith(endpoint, {
      base_url: 'https://pay.totools.cn',
      binding_code: 'binding-code',
      revision: 7,
    }, {
      headers: { 'Idempotency-Key': 'binding-save-0001' },
    })
  })

  it('sends the retry-stable idempotency key with the manual delete', async () => {
    remove.mockResolvedValue({ data: status })

    await expect(useManualUnifiedPayment(7, 'binding-manual-0002')).resolves.toEqual(status)
    expect(remove).toHaveBeenCalledWith(endpoint, {
      data: { revision: 7 },
      headers: { 'Idempotency-Key': 'binding-manual-0002' },
    })
  })
})
