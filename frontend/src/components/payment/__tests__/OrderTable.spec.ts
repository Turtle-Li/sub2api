import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import OrderTable from '../OrderTable.vue'
import type { PaymentOrder } from '@/types/payment'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

const DataTableStub = {
  props: ['data'],
  template: `
    <div>
      <div v-for="row in data" :key="row.id" :data-test="'order-' + row.id">
        <slot name="cell-fulfillment_status" :row="row" />
      </div>
    </div>
  `,
}

function order(values: Partial<PaymentOrder> = {}): PaymentOrder {
  return {
    id: 1,
    user_id: 2,
    amount: 10,
    pay_amount: 10,
    fee_rate: 0,
    payment_type: 'wxpay',
    out_trade_no: 'sub2_order_table_1',
    status: 'COMPLETED',
    order_type: 'subscription',
    created_at: '2026-09-16T00:00:00Z',
    expires_at: '2026-09-16T01:00:00Z',
    refund_amount: 0,
    fulfillment_status: 'FULFILLED',
    ...values,
  }
}

describe('OrderTable benefit presentation', () => {
  it('shows a paid coupon hold as manual review without implying a refund', () => {
    const wrapper = mount(OrderTable, {
      props: {
        orders: [order({ status: 'FAILED', payment_status: 'PAID', fulfillment_status: 'FAILED', needs_manual_review: true, refund_entitlement_status: 'NOT_APPLICABLE' })],
        loading: false,
      },
      global: { stubs: { DataTable: DataTableStub } },
    })
    expect(wrapper.text()).toContain('payment.orderOps.reviewRequired')
    expect(wrapper.text()).toContain('payment.result.paidManualReview')
    expect(wrapper.text()).not.toContain('payment.orderOps.refundHandling')
    expect(wrapper.text()).not.toContain('payment.orderOps.refundReviewRequired')
  })

  it('separates the historical issuance fact from refund handling only when needed', () => {
    const wrapper = mount(OrderTable, {
      props: {
        orders: [
          order({ id: 1 }),
          order({
            id: 2,
            status: 'REFUND_PENDING',
            refund_entitlement_status: 'RECLAIMING',
            needs_manual_review: true,
            refund_recovery: {
              state: 'WAITING_PROVIDER_BALANCE',
              amount_fen: 10,
              currency: 'CNY',
              can_retry: true,
              can_confirm_external: true,
            },
          }),
        ],
        loading: false,
      },
      global: { stubs: { DataTable: DataTableStub } },
    })

    const ordinary = wrapper.get('[data-test="order-1"]').text()
    expect(ordinary).toContain('payment.orderOps.fulfillment.fulfilled')
    expect(ordinary).not.toContain('payment.orderOps.issuanceRecord')
    expect(ordinary).not.toContain('payment.orderOps.refundHandling')

    const refund = wrapper.get('[data-test="order-2"]').text()
    expect(refund).toContain('payment.orderOps.issuanceRecord')
    expect(refund).toContain('payment.orderOps.fulfillment.fulfilled')
    expect(refund).toContain('payment.orderOps.refundHandling')
    expect(refund).toContain('payment.orderOps.refundEntitlement.reclaiming')
    expect(refund).toContain('payment.admin.refundMerchantBalanceInsufficientShort')
    expect(refund).toContain('payment.orderOps.refundReviewRequired')
  })
})
