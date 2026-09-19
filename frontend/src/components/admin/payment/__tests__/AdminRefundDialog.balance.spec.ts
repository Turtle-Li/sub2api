import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import type { BalanceRefundReview, RefundReview } from '@/api/admin/payment'
import type { PaymentOrder } from '@/types/payment'
import AdminRefundDialog from '../AdminRefundDialog.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)

const BaseDialogStub = defineComponent({
  props: { show: Boolean, title: String, width: String },
  emits: ['close'],
  template: '<section v-if="show"><slot /><footer><slot name="footer" /></footer></section>',
})

function order(overrides: Partial<PaymentOrder> = {}): PaymentOrder {
  return {
    id: 1,
    user_id: 10,
    amount: 100,
    pay_amount: 0.1,
    currency: 'CNY',
    fee_rate: 0,
    payment_type: 'alipay',
    out_trade_no: 'order-1',
    status: 'COMPLETED',
    order_type: 'balance',
    created_at: '2026-09-01T00:00:00Z',
    expires_at: '2026-09-02T00:00:00Z',
    refund_amount: 0,
    ...overrides,
  }
}

function balanceImpact(overrides: Partial<BalanceRefundReview> = {}): BalanceRefundReview {
  return {
    original_paid_credit: 0.1,
    original_gift_credit: 99.9,
    remaining_paid_credit: 0.1,
    available_balance: 100,
    available_paid_credit: 0.1,
    available_gift_credit: 99.9,
    paid_credit_to_reclaim: 0.1,
    gift_credit_to_reclaim: 99.9,
    ...overrides,
  }
}

function balanceReview(overrides: Partial<RefundReview> = {}): RefundReview {
  return {
    order_id: 1,
    order_type: 'balance',
    currency: 'CNY',
    can_refund: true,
    requires_manual_review: false,
    quote_revision: 'quote-recoverable',
    generated_at: '2026-09-01T00:00:00Z',
    default_refund_amount: 0.1,
    max_refund_amount: 0.1,
    entitlement_amount: 100,
    balance: balanceImpact(),
    ...overrides,
  }
}

function openRefund(review: RefundReview, orderOverrides: Partial<PaymentOrder> = {}) {
  return mount(AdminRefundDialog, {
    props: { show: true, order: order(orderOverrides), review },
    global: { stubs: { BaseDialog: BaseDialogStub } },
  })
}

describe('reviewed balance refund boundaries', () => {
  it('rejects a refund when the authoritative review finds the paid credit consumed', async () => {
    const wrapper = openRefund(balanceReview({
      can_refund: false,
      quote_revision: 'quote-paid-consumed',
      default_refund_amount: 0,
      max_refund_amount: 0,
      entitlement_amount: 0,
      reason_code: 'PAID_BALANCE_CONSUMED',
      reason: 'the paid balance has already been consumed; gifted balance is not refundable',
      balance: balanceImpact({
        available_balance: 99.9,
        available_paid_credit: 0,
        available_gift_credit: 99.9,
        paid_credit_to_reclaim: 0,
        gift_credit_to_reclaim: 0,
      }),
    }))

    expect(wrapper.text()).toContain('payment.admin.refundUnavailable')
    expect(wrapper.text()).toContain('the paid balance has already been consumed; gifted balance is not refundable')
    expect(wrapper.find('button[type="submit"]').exists()).toBe(false)
    expect(wrapper.find('input[name="refund_amount"]').exists()).toBe(false)
    expect(wrapper.find('#deduct-balance').exists()).toBe(false)

    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('confirm')).toBeUndefined()
  })

  it('restores refund availability only when a fresh review returns a recoverable quote', async () => {
    const wrapper = openRefund(balanceReview({
      can_refund: false,
      quote_revision: 'quote-paid-consumed',
      default_refund_amount: 0,
      max_refund_amount: 0,
      entitlement_amount: 0,
      reason_code: 'PAID_BALANCE_CONSUMED',
      balance: balanceImpact({
        available_balance: 99.9,
        available_paid_credit: 0,
        available_gift_credit: 99.9,
        paid_credit_to_reclaim: 0,
        gift_credit_to_reclaim: 0,
      }),
    }))

    expect(wrapper.find('button[type="submit"]').exists()).toBe(false)

    await wrapper.setProps({ review: balanceReview({ quote_revision: 'quote-restored' }) })

    expect(wrapper.text()).not.toContain('payment.admin.refundUnavailable')
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.find('#deduct-balance').exists()).toBe(false)

    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('confirm')?.[0]).toEqual([{ reason_code: 'customer_request' }])
  })

  it('uses the reviewed paid cash as the net refund and keeps gift credit out of the cash amount', async () => {
    const wrapper = openRefund(balanceReview())
    const impact = wrapper.get('[aria-label="payment.admin.balanceRefundImpact"]')

    expect(impact.text()).toContain('$0.10')
    expect(impact.text()).toContain('$99.90')
    expect(impact.text()).toContain('¥0.10')
    expect(impact.text()).not.toContain('¥100.00')
    expect(wrapper.find('input[name="refund_amount"]').exists()).toBe(false)

    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('confirm')?.[0]).toEqual([{ reason_code: 'customer_request' }])
  })

  it('does not permit a second refund once the server review marks the order settled', async () => {
    const wrapper = openRefund(balanceReview({
      can_refund: false,
      quote_revision: undefined,
      default_refund_amount: 0,
      max_refund_amount: 0,
      entitlement_amount: 0,
      balance: undefined,
      reason_code: 'REFUND_ALREADY_SETTLED',
      reason: 'each order allows only one successful refund',
    }), {
      status: 'PARTIALLY_REFUNDED',
      refund_amount: 0.1,
    })

    expect(wrapper.text()).toContain('payment.admin.refundUnavailable')
    expect(wrapper.text()).toContain('each order allows only one successful refund')
    expect(wrapper.find('button[type="submit"]').exists()).toBe(false)
    expect(wrapper.find('input[name="refund_amount"]').exists()).toBe(false)

    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('confirm')).toBeUndefined()
  })
})
