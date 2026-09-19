import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import PaymentDiscountCodeInput from '../PaymentDiscountCodeInput.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('PaymentDiscountCodeInput', () => {
  it('shows the server-issued original and discount amounts under an applied code on narrow screens', () => {
    const wrapper = mount(PaymentDiscountCodeInput, {
      props: {
        modelValue: 'SAVE2026',
        applied: {
          code_id: 9,
          code: 'SAVE2026',
          version: 2,
          original_amount: '5.00',
          discount_amount: '1.00',
          pay_amount: '4.00',
          currency: 'CNY',
          revision: 'coupon-revision',
        },
      },
    })

    expect(wrapper.find('[data-test="payment-discount-mobile-breakdown"]').text()).toContain('5.00')
    expect(wrapper.find('[data-test="payment-discount-mobile-breakdown"]').text()).toContain('1.00')
    expect(wrapper.find('input').attributes('maxlength')).toBe('32')
  })

  it('connects an error status to the code input', () => {
    const wrapper = mount(PaymentDiscountCodeInput, {
      props: { modelValue: 'INVALID', status: 'Invalid coupon', error: true },
    })

    expect(wrapper.find('input').attributes('aria-describedby')).toBe('payment-discount-code-status')
    expect(wrapper.find('input').attributes('aria-invalid')).toBe('true')
    expect(wrapper.find('[aria-live="polite"]').text()).toBe('Invalid coupon')
  })
})
