import { describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { mount } from '@vue/test-utils'

import AdminRefundDialog from '../AdminRefundDialog.vue'
import type { RefundReview } from '@/api/admin/payment'
import type { PaymentOrder } from '@/types/payment'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string, params?: { count?: number }) => params?.count == null ? key : `${key}:${params.count}` }) }))

const BaseDialogStub = defineComponent({
  props: { show: Boolean, title: String, width: String },
  emits: ['close'],
  template: '<section v-if="show"><slot /><footer><slot name="footer" /></footer></section>',
})

function order(): PaymentOrder {
  return {
    id: 51,
    user_id: 7,
    amount: 15,
    pay_amount: 12.34,
    currency: 'CNY',
    fee_rate: 0,
    payment_type: 'alipay',
    out_trade_no: 'refund-51',
    status: 'COMPLETED',
    order_type: 'balance',
    created_at: '2026-09-14T00:00:00Z',
    expires_at: '2026-09-14T01:00:00Z',
    refund_amount: 0,
  }
}

function balanceReview(overrides: Partial<RefundReview> = {}): RefundReview {
  return {
    order_id: 51,
    order_type: 'balance',
    currency: 'CNY',
    can_refund: true,
    requires_manual_review: false,
    quote_revision: 'quote-51',
    generated_at: '2026-09-14T00:00:00Z',
    default_refund_amount: 12.34,
    max_refund_amount: 12.34,
    entitlement_amount: 15,
    balance: {
      original_paid_credit: 12,
      original_gift_credit: 3,
      remaining_paid_credit: 12,
      available_balance: 20,
      available_paid_credit: 12,
      available_gift_credit: 8,
      paid_credit_to_reclaim: 12,
      gift_credit_to_reclaim: 3,
    },
    ...overrides,
  }
}

function mountDialog(review: RefundReview) {
  return mount(AdminRefundDialog, {
    props: { show: true, order: order(), review },
    global: { stubs: { BaseDialog: BaseDialogStub } },
  })
}

describe('AdminRefundDialog', () => {
  it('renders server-calculated balance effects without editable monetary or recovery controls', async () => {
    const wrapper = mountDialog(balanceReview())

    expect(wrapper.text()).toContain('payment.admin.paidPrincipalRefundable')
    expect(wrapper.text()).toContain('payment.admin.giftedCreditRecovery')
    expect(wrapper.text()).toContain('payment.admin.currentAvailableCredit')
    expect(wrapper.text()).toContain('payment.admin.refundCash')
    expect(wrapper.text()).toContain('$12.00')
    expect(wrapper.text()).toContain('¥12.34')
    expect(wrapper.find('input[type="number"]').exists()).toBe(false)
    expect(wrapper.find('#deduct-balance').exists()).toBe(false)
    expect(wrapper.find('#force-refund').exists()).toBe(false)

    await wrapper.find('#refund-reason').setValue('Customer cancellation')
    await wrapper.find('form').trigger('submit')
    expect(wrapper.emitted('confirm')?.[0]).toEqual([{ reason: 'Customer cancellation' }])
  })

  it('renders subscription time and the new expiry from the authoritative quote', () => {
    const wrapper = mountDialog(balanceReview({
      order_type: 'subscription',
      balance: undefined,
      subscription: {
        subscription_id: 8,
        term_start_at: '2026-08-14T00:00:00Z',
        term_end_at: '2026-11-14T00:00:00Z',
        current_expires_at: '2026-11-14T00:00:00Z',
        new_expires_at: '2026-10-14T00:00:00Z',
        purchased_seconds: 7_776_000,
        used_seconds: 2_592_000,
        remaining_seconds: 5_184_000,
      },
    }))

    expect(wrapper.text()).toContain('payment.admin.usedTime')
    expect(wrapper.text()).toContain('payment.admin.remainingTime')
    expect(wrapper.text()).toContain('payment.admin.proratedRefund')
    expect(wrapper.text()).toContain('payment.admin.newExpiry')
    expect(wrapper.text()).toContain('payment.admin.refundTimeDays:30')
    expect(wrapper.text()).toContain('payment.admin.refundTimeDays:60')
  })

  it('shows the manual-review reason and disables confirmation', async () => {
    const wrapper = mountDialog(balanceReview({
      can_refund: false,
      requires_manual_review: true,
      quote_revision: undefined,
      reason_code: 'NON_REVERSIBLE_ENTITLEMENT',
      reason: 'Manual entitlement rollback is required.',
    }))

    expect(wrapper.text()).toContain('payment.admin.refundManualReviewRequired')
    expect(wrapper.text()).toContain('Manual entitlement rollback is required.')
    expect(wrapper.find('button[type="submit"]').attributes('disabled')).toBeDefined()

    await wrapper.find('#refund-reason').setValue('Customer cancellation')
    await wrapper.find('form').trigger('submit')
    expect(wrapper.emitted('confirm')).toBeUndefined()
  })
})
