import type { LocationQuery, LocationQueryRaw } from 'vue-router'
import type { SubscriptionPlan } from '@/types/payment'
import { normalizeVisibleMethod } from '@/components/payment/paymentFlow'

export interface ParsedWechatResumeRoute {
  orderAmount: number
  orderType: 'balance' | 'subscription' | 'reset_card'
  paymentType: string
  planId?: number
  subscriptionId?: number
  resetCardTierRevision?: string
  resetCardQuantity?: number
  resetCardUseOnPurchase?: boolean
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

function readResetCardQuantity(query: LocationQuery): number | undefined {
  const quantity = Number.parseInt(readQueryString(query, 'reset_card_quantity'), 10)
  return Number.isSafeInteger(quantity) && quantity >= 1 && quantity <= 99 ? quantity : undefined
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
  const subscriptionId = Number.parseInt(readQueryString(query, 'subscription_id'), 10)
  const hasSubscriptionId = Number.isFinite(subscriptionId) && subscriptionId > 0
  const resetCardTierRevision = readQueryString(query, 'reset_card_tier_revision').trim()
  const resetCardQuantity = readResetCardQuantity(query)
  const resetCardUseOnPurchase = readQueryString(query, 'reset_card_use_on_purchase') === '1'
  const requestedType = readQueryString(query, 'order_type')
  const orderType = requestedType === 'reset_card'
    ? 'reset_card'
    : requestedType === 'subscription' || hasPlanId ? 'subscription' : 'balance'
  const resetCardContext = orderType === 'reset_card'
    ? {
        ...(resetCardQuantity ? { resetCardQuantity } : {}),
        ...(resetCardUseOnPurchase ? { resetCardUseOnPurchase: true } : {}),
      }
    : {}
  if (wechatResumeToken) {
    return {
      wechatResumeToken,
      paymentType,
      orderType,
      orderAmount: 0,
      planId: hasPlanId ? planId : undefined,
      subscriptionId: hasSubscriptionId ? subscriptionId : undefined,
      resetCardTierRevision: resetCardTierRevision || undefined,
      ...resetCardContext,
    }
  }

  const openid = readQueryString(query, 'openid')
  if (!openid) {
    return null
  }

  const rawAmount = Number.parseFloat(readQueryString(query, 'amount'))
  const orderAmount = Number.isFinite(rawAmount) && rawAmount > 0
    ? rawAmount
    : (orderType === 'subscription'
      ? (plans.find(plan => plan.id === planId)?.price ?? 0)
      : fallbackBalanceAmount)

  return {
    openid,
    paymentType,
    orderType,
    orderAmount,
    planId: hasPlanId ? planId : undefined,
    subscriptionId: hasSubscriptionId ? subscriptionId : undefined,
    resetCardTierRevision: resetCardTierRevision || undefined,
    ...resetCardContext,
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
  delete nextQuery.subscription_id
  delete nextQuery.reset_card_tier_revision
  delete nextQuery.reset_card_quantity
  delete nextQuery.reset_card_use_on_purchase
  delete nextQuery.payment_idempotency_key
  return nextQuery
}
