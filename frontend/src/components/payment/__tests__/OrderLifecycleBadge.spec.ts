import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import OrderLifecycleBadge from '../OrderLifecycleBadge.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

describe('OrderLifecycleBadge', () => {
  it('shows reclaimed refund benefits separately from historical fulfillment', () => {
    const wrapper = mount(OrderLifecycleBadge, {
      props: { kind: 'refundEntitlement', value: 'RECLAIMED' },
    })

    expect(wrapper.text()).toBe('payment.orderOps.refundEntitlement.reclaimed')
    expect(wrapper.classes()).toContain('badge-success')
  })

  it('marks an unverified historical benefit state for review', () => {
    const wrapper = mount(OrderLifecycleBadge, {
      props: { kind: 'refundEntitlement', value: 'HISTORICAL_UNVERIFIED' },
    })

    expect(wrapper.text()).toBe('payment.orderOps.refundEntitlement.historical_unverified')
    expect(wrapper.classes()).toContain('badge-danger')
  })
})
