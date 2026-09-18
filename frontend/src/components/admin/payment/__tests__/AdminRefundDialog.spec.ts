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

function mountDialog(review: RefundReview, props: Record<string, unknown> = {}) {
  return mount(AdminRefundDialog, {
    props: { show: true, order: order(), review, ...props },
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

    await wrapper.find('#refund-reason-detail').setValue('Customer cancellation')
    await wrapper.find('form').trigger('submit')
    expect(wrapper.emitted('confirm')?.[0]).toEqual([{
      reason_code: 'customer_request',
      reason_detail: 'Customer cancellation',
    }])
  })

  it('requires details when the administrator selects Other', async () => {
    const wrapper = mountDialog(balanceReview())

    await wrapper.find('#refund-reason-code').setValue('other')
    expect(wrapper.find('button[type="submit"]').attributes('disabled')).toBeDefined()
    await wrapper.find('form').trigger('submit')
    expect(wrapper.emitted('confirm')).toBeUndefined()

    await wrapper.find('#refund-reason-detail').setValue('  account\nverification issue  ')
    await wrapper.find('form').trigger('submit')
    expect(wrapper.emitted('confirm')?.[0]).toEqual([{
      reason_code: 'other',
      reason_detail: 'account\nverification issue',
    }])
  })

  it('renders selected subscription effects from the authoritative quote', () => {
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
        seconds_to_reclaim: 3_456_000,
      },
    }), { reviewedRefundAmount: 12.34 })

    expect(wrapper.text()).toContain('payment.admin.usedTime')
    expect(wrapper.text()).toContain('payment.admin.remainingTime')
    expect(wrapper.text()).toContain('payment.admin.refundEntitlementTime')
    expect(wrapper.text()).toContain('payment.admin.newExpiry')
    expect(wrapper.text()).toContain('payment.admin.refundTimeDays:30')
    expect(wrapper.text()).toContain('payment.admin.refundTimeDays:60')
    expect(wrapper.text()).toContain('payment.admin.refundTimeDays:40')
    expect((wrapper.get('#refund-amount').element as HTMLInputElement).value).toBe('12.34')
  })

  it('shows the manual-review reason and disables confirmation', async () => {
    const wrapper = mountDialog(balanceReview({
      can_refund: false,
      requires_manual_review: true,
      quote_revision: undefined,
      balance: undefined,
      subscription: undefined,
      default_refund_amount: 0,
      max_refund_amount: 0,
      entitlement_amount: 0,
      reason_code: 'NON_REVERSIBLE_ENTITLEMENT',
      reason: 'Manual entitlement rollback is required.',
    }))

    expect(wrapper.text()).toContain('payment.admin.refundManualReviewRequired')
    expect(wrapper.text()).toContain('Manual entitlement rollback is required.')
		expect(wrapper.text().match(/Manual entitlement rollback is required\./g)).toHaveLength(1)
		expect(wrapper.text()).not.toContain('payment.admin.refundUnavailable')
    expect(wrapper.text()).not.toContain('¥0.00')
    expect(wrapper.find('button[type="submit"]').exists()).toBe(false)
    expect(wrapper.emitted('confirm')).toBeUndefined()
  })

  it('collects audited subscription provenance without exposing a refund amount input', async () => {
    const wrapper = mountDialog(balanceReview({
      order_type: 'subscription',
      can_refund: false,
      requires_manual_review: true,
      quote_revision: undefined,
      balance: undefined,
      subscription: undefined,
      default_refund_amount: 0,
      max_refund_amount: 0,
      entitlement_amount: 0,
      reason_code: 'LEGACY_SUBSCRIPTION_UNATTRIBUTED',
      subscription_backfill: {
        audit_revision: 'audit-51',
        suggested_subscription_id: 8,
        suggested_term_start_at: '2026-09-12T15:13:43Z',
        suggested_term_end_at: '2026-10-12T15:13:43Z',
        evidence_source: 'payment_audit_and_subscription',
        subscription_group_id: 4,
        purchased_days: 30,
        candidates: [{
          subscription_id: 8,
          starts_at: '2026-09-12T15:13:43Z',
          expires_at: '2026-10-12T15:13:43Z',
          status: 'active',
        }],
      },
    }))

    expect((wrapper.get('[data-testid="backfill-subscription-id"]').element as HTMLSelectElement).value).toBe('8')
    expect(wrapper.text()).toContain('payment.admin.subscriptionGrantBackfillSnapshot')
    expect(wrapper.text()).toContain('payment.admin.subscriptionGrantBackfillAmountHint')
    expect(wrapper.find('input[name="refund_amount"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="subscription-grant-backfill"]').attributes('disabled')).toBeDefined()

    await wrapper.get('[data-testid="backfill-evidence-detail"]').setValue('Verified order audit and subscription dates')
    await wrapper.get('[data-testid="subscription-grant-backfill"]').trigger('click')

    expect(wrapper.emitted('backfill')?.[0]?.[0]).toEqual(expect.objectContaining({
      audit_revision: 'audit-51',
      subscription_id: 8,
      evidence_source: 'payment_audit_and_subscription',
      evidence_detail: 'Verified order audit and subscription dates',
    }))
    expect(wrapper.emitted('backfill')?.[0]?.[0]).toEqual(expect.objectContaining({
      term_start_at: '2026-09-12T15:13:43Z',
      term_end_at: '2026-10-12T15:13:43Z',
    }))
  })

  it('validates a subscription amount locally and emits only a selected amount with a matching preview', async () => {
    const review = balanceReview({
      order_type: 'subscription',
      balance: undefined,
      min_refund_amount: 0.03,
      default_refund_amount: 0.08,
      max_refund_amount: 0.08,
      subscription: {
        subscription_id: 8,
        term_start_at: '2026-08-14T00:00:00Z',
        term_end_at: '2026-11-14T00:00:00Z',
        current_expires_at: '2026-11-14T00:00:00Z',
        new_expires_at: '2026-10-14T00:00:00Z',
        purchased_seconds: 7_776_000,
        used_seconds: 2_592_000,
        remaining_seconds: 5_184_000,
        seconds_to_reclaim: 3_456_000,
      },
    })
    const wrapper = mountDialog(review)

    await wrapper.get('#refund-amount').setValue('0.031')
    expect(wrapper.text()).toContain('payment.admin.refundAmountInvalid')
    expect(wrapper.find('button[type="submit"]').attributes('disabled')).toBeDefined()

    await wrapper.get('#refund-amount').setValue('0.02')
    expect(wrapper.text()).toContain('payment.admin.refundAmountTooSmall')

    await wrapper.get('#refund-amount').setValue('0.09')
    expect(wrapper.text()).toContain('payment.admin.refundAmountExceeded')

    await wrapper.get('#refund-amount').setValue('0.03')
    expect(wrapper.emitted('preview')?.at(-1)).toEqual([0.03])
    expect(wrapper.find('button[type="submit"]').attributes('disabled')).toBeDefined()

    await wrapper.setProps({ reviewedRefundAmount: 0.03 })
    await wrapper.find('form').trigger('submit')
    expect(wrapper.emitted('confirm')?.at(-1)).toEqual([{
      reason_code: 'customer_request',
      refund_amount: 0.03,
    }])
  })

  it('keeps the active amount field available beside an explicit preview error', () => {
    const wrapper = mountDialog(balanceReview({
      order_type: 'subscription',
      balance: undefined,
      min_refund_amount: 0.03,
      default_refund_amount: 0.03,
      max_refund_amount: 0.08,
      subscription: {
        subscription_id: 8,
        term_start_at: '2026-08-14T00:00:00Z',
        term_end_at: '2026-11-14T00:00:00Z',
        current_expires_at: '2026-11-14T00:00:00Z',
        new_expires_at: '2026-10-14T00:00:00Z',
        purchased_seconds: 7_776_000,
        used_seconds: 2_592_000,
        remaining_seconds: 5_184_000,
        seconds_to_reclaim: 3_456_000,
      },
    }), { reviewedRefundAmount: 0.03, error: 'The current amount cannot be represented.' })

    expect(wrapper.text()).toContain('The current amount cannot be represented.')
    expect(wrapper.find('#refund-amount').exists()).toBe(true)
  })
})
