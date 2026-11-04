import { apiClient } from './client'
import type { CreateOrderResult } from '@/types/payment'
import type { ShopAction, ShopInputCheck, ShopOrderDetail, ShopProduct, ShopProductInput } from '@/types/shop'

export const shopAPI = {
  products: () => apiClient.get<ShopProduct[]>('/shop/products'),
  product: (id: number) => apiClient.get<ShopProduct>(`/shop/products/${id}`),
  createOrder: (input: { product_id: number; product_revision: number; payment_type: 'alipay' | 'wxpay' }, key: string) =>
    apiClient.post<CreateOrderResult>('/shop/orders', input, { headers: { 'Idempotency-Key': key } }),
  order: (id: number) => apiClient.get<ShopOrderDetail>(`/shop/orders/${id}`),
  checkInput: (id: number, input: { content: string; revision: number }) => apiClient.post<ShopInputCheck>(`/shop/orders/${id}/input/check`, input),
  submitInput: (id: number, input: { content: string; revision: number }) => apiClient.post<ShopOrderDetail>(`/shop/orders/${id}/input`, input),
  adminProducts: () => apiClient.get<ShopProduct[]>('/admin/shop/products'),
  saveProduct: (id: number | null, input: ShopProductInput) => id === null
    ? apiClient.post<ShopProduct>('/admin/shop/products', input)
    : apiClient.put<ShopProduct>(`/admin/shop/products/${id}`, input),
  adminOrders: (params: { page: number; page_size: number; status: string }) => apiClient.get<{ items: ShopOrderDetail[]; total: number; page: number; page_size: number }>('/admin/shop/orders', { params }),
  adminOrder: (id: number) => apiClient.get<ShopOrderDetail>(`/admin/shop/orders/${id}`),
  revealInput: (id: number, revision: number) => apiClient.post<{ content: string; input_revision: number }>(`/admin/shop/orders/${id}/input/reveal`, { revision }),
  action: (id: number, input: ShopAction) => apiClient.post<ShopOrderDetail>(`/admin/shop/orders/${id}/action`, input),
  retryNotification: (id: number, channel: 'email' | 'feishu') => apiClient.post<ShopOrderDetail>(`/admin/shop/orders/${id}/notifications/retry`, { channel })
}
