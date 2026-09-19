import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import OrderPurchaseSnapshot from '../OrderPurchaseSnapshot.vue'
import type { PaymentOrder } from '@/types/payment'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('OrderPurchaseSnapshot quota semantics', () => {
  it('distinguishes unlimited zero quota from a positive limit and absent historical evidence', () => {
    const order = { order_type: 'subscription', product_snapshot: { name: 'Original plan', daily_limit_usd: 0, weekly_limit_usd: 110 } } as PaymentOrder
    const wrapper = mount(OrderPurchaseSnapshot, { props: { order } })
    expect(wrapper.text()).toContain('payment.planCard.unlimited')
    expect(wrapper.text()).toContain('$110')
    expect(wrapper.text()).not.toContain('$0')
    expect(wrapper.text()).not.toContain('payment.orderOps.monthlyQuota')
  })

  it('shows the frozen monthly reset-card commitment while preserving one-time snapshots', () => {
    const monthlyOrder = {
      order_type: 'subscription',
      product_snapshot: {
        name: 'Quarterly',
        entitlements: {
          reset_card_count: 2,
          reset_card_delivery_mode: 'monthly',
          reset_card_issue_count: 3,
          reset_card_expiry_days: 14,
          reset_card_expiry_unit: 'day',
        },
      },
    } as PaymentOrder
    expect(mount(OrderPurchaseSnapshot, { props: { order: monthlyOrder } }).text())
      .toContain('payment.entitlements.monthlyResetCards')

    const immediateOrder = {
      order_type: 'subscription',
      product_snapshot: {
        name: 'Monthly',
        entitlements: { reset_card_count: 2, reset_card_expiry_days: 14 },
      },
    } as PaymentOrder
    expect(mount(OrderPurchaseSnapshot, { props: { order: immediateOrder } }).text())
      .toContain('2 · payment.orderOps.days')
  })

  it('shows the server-issued coupon settlement independently from plan entitlements', () => {
    const order = {
      order_type: 'subscription',
      product_snapshot: {
        name: 'Original plan',
        payment_discount: {
          code_id: 9,
          code: 'SAVE2026',
          original_amount: '100.00',
          discount_amount: '20.00',
          pay_amount: '80.00',
          currency: 'CNY',
        },
      },
    } as PaymentOrder
    const text = mount(OrderPurchaseSnapshot, { props: { order } }).text()

    expect(text).toContain('SAVE2026')
    expect(text).toContain('100.00')
    expect(text).toContain('20.00')
    expect(text).toContain('80.00')
  })

  it('shows immutable reset-card quantity, unit price, and purchase-time use intent', () => {
    const baseOrder = {
      order_type: 'reset_card',
      currency: 'CNY',
      product_snapshot: {
        name: 'Quota reset card',
        currency: 'CNY',
        quantity: 3,
        price: 37.02,
        unit_price: 12.34,
        use_on_purchase: true,
      },
    } as PaymentOrder
    const text = mount(OrderPurchaseSnapshot, { props: { order: baseOrder } }).text()

    expect(text).toContain('payment.orderOps.resetCardQuantity')
    expect(text).toContain('3')
    expect(text).toContain('payment.orderOps.resetCardUnitPrice')
    expect(text).toContain('CNY 12.34')
    expect(text).toContain('payment.orderOps.resetCardTotalPrice')
    expect(text).toContain('CNY 37.02')
    expect(text).not.toContain('payment.orderOps.listPrice')
    expect(text).toContain('payment.orderOps.resetCardUseOnPurchase')
    expect(text).toContain('common.yes')

    const notUsedOnPurchase = {
      ...baseOrder,
      product_snapshot: { ...baseOrder.product_snapshot, use_on_purchase: false },
    } as PaymentOrder
    expect(mount(OrderPurchaseSnapshot, { props: { order: notUsedOnPurchase } }).text()).toContain('common.no')
  })
})
