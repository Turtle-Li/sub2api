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
        <slot name="cell-payment_status" :row="row" />
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
  it('keeps customer history detailed and gives admins one current outcome plus one blocker', () => {
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

    const admin = mount(OrderTable, {
      props: {
        orders: [order({
          id: 3,
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
        })],
        loading: false,
        showUser: true,
      },
      global: { stubs: { DataTable: DataTableStub } },
    })

    const adminText = admin.get('[data-test="order-3"]').text()
    expect(adminText).toContain('payment.status.refund_pending')
    expect(adminText).toContain('payment.orderOps.refundEntitlement.reclaiming')
    expect(adminText).toContain('payment.admin.refundMerchantBalanceInsufficientCompact')
    expect(adminText).not.toContain('payment.orderOps.issuanceRecord')
    expect(adminText).not.toContain('payment.orderOps.refundHandling')
    expect(adminText).not.toContain('payment.orderOps.refundReviewRequired')
  })
})
