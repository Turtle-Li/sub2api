import type { LocationQuery, LocationQueryRaw } from 'vue-router'
import type { SubscriptionPlan } from '@/types/payment'
import { normalizeVisibleMethod } from '@/components/payment/paymentFlow'

export interface ParsedWechatResumeRoute {
  orderAmount: number
  orderType: 'balance' | 'subscription' | 'subscription_reset'
  paymentType: string
  planId?: number
  resetSubscriptionId?: number
  resetCardQuantity?: number
  openid?: string
  wechatResumeToken?: string
}

function readQueryString(query: LocationQuery, key: string): string {
  const value = query[key]
  if (Array.isArray(value)) {
    return typeof value[0] === 'string' ? value[0] : ''
  }
  return typeof value === 'string' ? value : ''
}

export function hasWechatResumeQuery(query: LocationQuery): boolean {
  if (readQueryString(query, 'wechat_resume') === '1') {
    return true
  }
  return readQueryString(query, 'wechat_resume_token') !== ''
    || readQueryString(query, 'openid') !== ''
}

export function parseWechatResumeRoute(
  query: LocationQuery,
  plans: SubscriptionPlan[],
  fallbackBalanceAmount: number,
): ParsedWechatResumeRoute | null {
  if (!hasWechatResumeQuery(query)) {
    return null
  }

  const wechatResumeToken = readQueryString(query, 'wechat_resume_token')
  const paymentType = normalizeVisibleMethod(readQueryString(query, 'payment_type')) || 'wxpay'
  const planId = Number.parseInt(readQueryString(query, 'plan_id'), 10)
  const hasPlanId = Number.isFinite(planId) && planId > 0
  const resetSubscriptionId = Number.parseInt(readQueryString(query, 'reset_subscription_id'), 10)
  const resetCardQuantity = Number.parseInt(readQueryString(query, 'reset_card_quantity'), 10)
  const hasResetSubscriptionId = Number.isFinite(resetSubscriptionId) && resetSubscriptionId > 0
  const hasResetCardQuantity = Number.isFinite(resetCardQuantity) && resetCardQuantity > 0
  const requestedOrderType = readQueryString(query, 'order_type')
  const orderType = requestedOrderType === 'subscription_reset' || hasResetSubscriptionId
    ? 'subscription_reset'
    : requestedOrderType === 'subscription' || hasPlanId
      ? 'subscription'
      : 'balance'

  if (wechatResumeToken) {
    return {
      wechatResumeToken,
      paymentType,
      orderType,
      orderAmount: 0,
      planId: hasPlanId ? planId : undefined,
      resetSubscriptionId: hasResetSubscriptionId ? resetSubscriptionId : undefined,
      resetCardQuantity: hasResetCardQuantity ? Math.min(100, resetCardQuantity) : undefined,
    }
  }

  const openid = readQueryString(query, 'openid')
  if (!openid) {
    return null
  }

  const rawAmount = Number.parseFloat(readQueryString(query, 'amount'))
  const orderAmount = Number.isFinite(rawAmount) && rawAmount > 0
    ? rawAmount
    : orderType === 'subscription'
      ? (plans.find(plan => plan.id === planId)?.price ?? 0)
      : orderType === 'subscription_reset' ? 0 : fallbackBalanceAmount

  return {
    openid,
    paymentType,
    orderType,
    orderAmount,
    planId: hasPlanId ? planId : undefined,
    resetSubscriptionId: hasResetSubscriptionId ? resetSubscriptionId : undefined,
    resetCardQuantity: hasResetCardQuantity ? Math.min(100, resetCardQuantity) : undefined,
  }
}

export function stripWechatResumeQuery(query: LocationQuery): LocationQueryRaw {
  const nextQuery: LocationQueryRaw = { ...query }
  delete nextQuery.wechat_resume
  delete nextQuery.wechat_resume_token
  delete nextQuery.openid
  delete nextQuery.state
  delete nextQuery.scope
  delete nextQuery.payment_type
  delete nextQuery.amount
  delete nextQuery.order_type
  delete nextQuery.plan_id
  delete nextQuery.reset_subscription_id
  delete nextQuery.reset_card_quantity
  return nextQuery
}
