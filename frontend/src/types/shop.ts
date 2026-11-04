import type { OrderStatus } from './payment'
export type ShopMode = 'session_topup' | 'account_delivery'
export type ShopStatus = 'WAITING_PAYMENT' | 'WAITING_INPUT' | 'QUEUED' | 'PROCESSING' | 'NEEDS_INPUT' | 'COMPLETED' | 'CLOSED'
export type ShopNotificationStatus = 'NOT_SENT' | 'PENDING' | 'SENDING' | 'SENT' | 'FAILED'
export interface ShopProduct {
  id: number
  name: string
  category: string
  summary: string
  description: string
  price: number
  currency: 'CNY'
  duration_value: number
  duration_unit: 'month' | 'day' | 'year'
  fulfillment_mode: ShopMode
  input_format: 'session_json' | 'text'
  input_url: string
  input_instructions: string
  guide_key: '' | 'chatgpt-plus-account'
  enabled: boolean
  sort_order: number
  revision: number
  created_at: string
  updated_at: string
}
export type ShopProductInput = Omit<ShopProduct, 'id' | 'currency' | 'created_at' | 'updated_at' | 'revision'> & { revision?: number }
export interface ShopInputCheck {
  account: string
  plan_type: string
  expires_at?: string
  warnings: string[]
}
export interface ShopOrderDetail {
  order_id: number
  out_trade_no: string
  product: ShopProduct
  user_id: number
  user_email?: string
  pay_amount: number
  currency: 'CNY'
  payment_status: OrderStatus
  fulfillment_status: ShopStatus
  revision: number
  input_revision: number
  input_account: string
  input_check?: ShopInputCheck
  input_submitted_at?: string
  created_at: string
  paid_at?: string
  completed_at?: string
  status_note: string
  delivery_content?: string
  email_status: ShopNotificationStatus
  admin_notification_status: ShopNotificationStatus
  can_submit: boolean
  can_start: boolean
  can_complete: boolean
  can_reveal_input: boolean
}
export interface ShopAction {
  action: 'start' | 'request_input' | 'complete'
  revision: number
  input_revision: number
  note?: string
  delivery_content?: string
}
export const shopStatusLabels: Record<ShopStatus, string> = {
  WAITING_PAYMENT: '待付款', WAITING_INPUT: '待提交资料', QUEUED: '待人工处理',
  PROCESSING: '处理中', NEEDS_INPUT: '待补充资料', COMPLETED: '已完成', CLOSED: '已关闭'
}
export const shopNotificationLabels: Record<ShopNotificationStatus, string> = {
  NOT_SENT: '未发送', PENDING: '等待发送', SENDING: '发送中', SENT: '已发送', FAILED: '发送失败'
}
export const shopModeLabels: Record<ShopMode, string> = { session_topup: '提交资料 · 人工代充', account_delivery: '人工账号交付' }
export function shopDuration(product: ShopProduct): string {
  return `${product.duration_value} ${ { month: '个月', day: '天', year: '年' }[product.duration_unit] }`
}
