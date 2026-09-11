import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, put, post } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn() }))

vi.mock('@/api/client', () => ({ apiClient: { get, put, post } }))

import { adminPaymentAPI } from '@/api/admin/payment'

describe('admin payment invoice api', () => {
  beforeEach(() => {
    get.mockReset()
    put.mockReset()
    post.mockReset()
    get.mockResolvedValue({ data: {} })
    put.mockResolvedValue({ data: {} })
    post.mockResolvedValue({ data: {} })
  })

  it('filters the order queue and updates an order-scoped invoice request', async () => {
    await adminPaymentAPI.getOrders({ invoice_status: 'PENDING' })
    const payload = { status: 'PROCESSING' as const }
    await adminPaymentAPI.updateInvoiceRequest(51, payload)

    expect(get).toHaveBeenCalledWith('/admin/payment/orders', { params: { invoice_status: 'PENDING' } })
    expect(put).toHaveBeenCalledWith('/admin/payment/orders/51/invoice', payload)
  })

  it('uploads an issued PDF as multipart data', async () => {
    const pdf = new File(['%PDF-1.7\ninvoice'], 'invoice-51.pdf', { type: 'application/pdf' })

    await adminPaymentAPI.updateInvoiceRequest(51, {
      status: 'ISSUED',
      provider: 'manual',
      invoice_item_name: 'Information technology services',
      invoice_number: '24612000000000000001',
      invoice_pdf: pdf,
    })

    const [url, body, config] = put.mock.calls[0]
    expect(url).toBe('/admin/payment/orders/51/invoice')
    expect(body).toBeInstanceOf(FormData)
    expect((body as FormData).get('status')).toBe('ISSUED')
    expect((body as FormData).get('invoice_pdf')).toBe(pdf)
    expect(config).toEqual({ headers: { 'Content-Type': 'multipart/form-data' } })
  })

  it('retries a failed invoice result email', async () => {
    await adminPaymentAPI.retryInvoiceEmail(51)

    expect(post).toHaveBeenCalledWith('/admin/payment/orders/51/invoice/email/retry')
  })
})
