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
})
