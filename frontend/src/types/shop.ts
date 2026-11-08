import type { OrderStatus } from './payment'

export interface ShopIntegrationOption { id: string; version: number; adapter: string; enabled: boolean }
export interface ShopPaymentAction { binding_key: string; adapter: string; integration_ref: string; integration_version: number; params: Record<string, unknown> }
export type ShopAutomationState = 'PENDING' | 'RUNNING' | 'SUCCEEDED' | 'REVIEW_REQUIRED' | 'CANCELLED' | 'COMPENSATING' | 'REVOKED'
export interface ShopAutomationStatus {
  status: ShopAutomationState
  result_kind: 'none' | 'text'
  action_id?: string
  attempts?: number
  claim_revision?: number
  next_attempt_at?: string
  error_kind?: string
  can_retry?: boolean
  can_revoke?: boolean
  revoke_required?: boolean
}
export const shopAutomationLabels: Record<ShopAutomationState, string> = {
  PENDING: '等待自动处理', RUNNING: '正在自动交付', SUCCEEDED: '自动交付完成',
  REVIEW_REQUIRED: '交付待复核', CANCELLED: '自动交付已停止', COMPENSATING: '正在撤销授权', REVOKED: '授权已撤销'
}
export type ShopMode = 'session_topup' | 'account_delivery'
export type ShopStatus = 'WAITING_PAYMENT' | 'WAITING_INPUT' | 'QUEUED' | 'PROCESSING' | 'NEEDS_INPUT' | 'COMPLETED' | 'CLOSED'
export type ShopNotificationStatus = 'NOT_SENT' | 'PENDING' | 'SENDING' | 'SENT' | 'FAILED'
export type ShopPassDurationDays = 1 | 7
export interface ShopSKU {
  id: string
  attributes: Record<string, string>
  price: number
  duration_value: number
  duration_unit: 'month' | 'day' | 'year'
  concurrency: number
  enabled: boolean
}
export interface ShopProduct {
  delivery_config?: { readonly individual_copy: boolean }
  skus?: ShopSKU[]
  default_sku_id?: string
  sku_id?: string
  concurrency?: number
  concurrency_options?: { readonly concurrency: number; readonly price: number; readonly weekly_price?: number }[]
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
  guide_key: '' | 'chatgpt-plus-account' | 'deepseek-daypass'
  enabled: boolean
  sort_order: number
  revision: number
  created_at: string
  updated_at: string
  payment_actions?: ShopPaymentAction[]
  automatic_fulfillment?: boolean
}
export type ShopProductInput = Omit<ShopProduct, 'id' | 'currency' | 'created_at' | 'updated_at' | 'revision' | 'sku_id' | 'concurrency' | 'concurrency_options' | 'automatic_fulfillment'> & { revision?: number }
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
  automation?: ShopAutomationStatus
  delivery_kind?: 'none' | 'text'
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
export function isShopDaypass(product: ShopProduct): boolean { return product.guide_key === 'deepseek-daypass' }
export function shopDeliveryLabel(product: ShopProduct): string { return product.automatic_fulfillment ? (isShopDaypass(product) ? '付款后自动发放 API Key' : '付款后自动交付') : shopModeLabels[product.fulfillment_mode] }
export function shopDuration(product: Pick<ShopProduct, 'duration_value' | 'duration_unit'>): string {
  return `${product.duration_value} ${ { month: '个月', day: '天', year: '年' }[product.duration_unit] }`
}
