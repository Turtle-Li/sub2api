import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, shallowMount } from '@vue/test-utils'
import PaymentDiscountCouponsPanel from '../PaymentDiscountCouponsPanel.vue'
import PromoCodesView from '../PromoCodesView.vue'
import AdminPaymentCouponsView from '../orders/AdminPaymentCouponsView.vue'

const {
  getPaymentDiscountCoupons,
  createPaymentDiscountCoupon,
  updatePaymentDiscountCoupon,
  getPaymentDiscountCouponUsages,
  getPaymentDiscountCouponAudits,
  getPlans,
  promoList,
  copyToClipboard,
  showSuccess,
  showError,
} = vi.hoisted(() => ({
  getPaymentDiscountCoupons: vi.fn(),
  createPaymentDiscountCoupon: vi.fn(),
  updatePaymentDiscountCoupon: vi.fn(),
  getPaymentDiscountCouponUsages: vi.fn(),
  getPaymentDiscountCouponAudits: vi.fn(),
  getPlans: vi.fn(),
  promoList: vi.fn(),
  copyToClipboard: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('@/api/admin/payment', () => ({
  adminPaymentAPI: {
    getPaymentDiscountCoupons,
    createPaymentDiscountCoupon,
    updatePaymentDiscountCoupon,
    getPaymentDiscountCouponUsages,
    getPaymentDiscountCouponAudits,
    getPlans,
  },
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    promo: {
      list: promoList,
      create: vi.fn(),
      update: vi.fn(),
      delete: vi.fn(),
      getUsages: vi.fn(),
    },
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess, showError }),
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard }),
}))

vi.mock('@/composables/usePersistedPageSize', () => ({
  getPersistedPageSize: () => 20,
}))

vi.mock('@/utils/apiError', () => ({
  extractI18nErrorMessage: () => 'request failed',
}))

vi.mock('@/utils/format', () => ({
  formatCurrency: (amount: number, currency: string) => `${currency} ${amount.toFixed(2)}`,
  formatDateTime: (value: string) => value,
  formatDateTimeLocalInput: (timestamp: number) => {
    const value = new Date(timestamp * 1000)
    const pad = (part: number) => String(part).padStart(2, '0')
    return `${value.getFullYear()}-${pad(value.getMonth() + 1)}-${pad(value.getDate())}T${pad(value.getHours())}:${pad(value.getMinutes())}`
  },
  parseDateTimeLocalInput: (value: string) => {
    const parsed = new Date(value).getTime()
    return Number.isFinite(parsed) ? Math.floor(parsed / 1000) : null
  },
}))

vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key }),
}))

const coupon = {
  id: 17,
  code: 'PAYMENT20',
  discount_type: 'percent' as const,
  discount_value: '20.00',
  currency: 'CNY' as const,
  max_uses: 10,
  per_user_max_uses: 1,
  target_user_id: null,
  starts_at: '2030-01-01T00:00:00Z',
  expires_at: '2030-12-31T23:59:00Z',
  enabled: true,
  notes: 'new customer discount',
  version: 7,
  reserved_uses: 1,
  consumed_uses: 2,
  created_at: '2030-01-01T00:00:00Z',
}

const subscriptionPlans = [
  { id: 301, name: 'Plus', for_sale: true, validity_days: 1, validity_unit: 'month' },
  { id: 302, name: 'Archived Pro', for_sale: false, validity_days: 3, validity_unit: 'month' },
]

const panelStubs = {
  TablePageLayout: { template: '<section><slot name="filters" /><slot name="table" /><slot name="pagination" /></section>' },
  DataTable: {
    props: ['data'],
    template: '<div data-test="coupon-table"><div v-for="row in data" :key="row.id"><slot name="cell-code" :row="row" :value="row.code" /><slot name="cell-actions" :row="row" /></div></div>',
  },
  Pagination: true,
  BaseDialog: {
    props: ['show'],
    template: '<div v-if="show" data-test="dialog"><slot /><slot name="footer" /></div>',
  },
  Select: {
    props: ['id', 'modelValue', 'options', 'ariaLabel'],
    emits: ['update:modelValue'],
    template: '<select :id="id" :value="modelValue" :aria-label="ariaLabel" @change="$emit(\'update:modelValue\', $event.target.value)"><option v-for="option in options" :key="String(option.value)" :value="option.value">{{ option.label }}</option></select>',
  },
  Toggle: {
    props: ['modelValue', 'ariaLabel'],
    emits: ['update:modelValue'],
    template: '<button type="button" data-test="enabled-toggle" :aria-label="ariaLabel" @click="$emit(\'update:modelValue\', !modelValue)" />',
  },
  Icon: true,
  RouterLink: { props: ['to'], template: '<a><slot /></a>' },
}

async function mountPanel(items: typeof coupon[] = []) {
  getPaymentDiscountCoupons.mockResolvedValue({ data: { items, total: items.length } })
  const wrapper = mount(PaymentDiscountCouponsPanel, { global: { stubs: panelStubs } })
  await flushPromises()
  return wrapper
}

describe('payment discount coupon panel', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    createPaymentDiscountCoupon.mockResolvedValue({ data: coupon })
    updatePaymentDiscountCoupon.mockResolvedValue({ data: coupon })
    getPaymentDiscountCouponUsages.mockResolvedValue({ data: { items: [], total: 0 } })
    getPaymentDiscountCouponAudits.mockResolvedValue({ data: { items: [], total: 0 } })
    getPlans.mockResolvedValue({ data: subscriptionPlans })
    copyToClipboard.mockResolvedValue(true)
  })

  it('omits an empty code and per-user cap so the server generates a code and applies its default cap', async () => {
    const wrapper = await mountPanel()

    await wrapper.get('[data-test="create-payment-coupon"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('#payment-discount-type').attributes('aria-label')).toBe('admin.paymentCoupons.discountType')
    expect(wrapper.get('#payment-discount-currency').attributes('aria-label')).toBe('admin.paymentCoupons.currency')
    expect(wrapper.get('[data-test="enabled-toggle"]').attributes('aria-label')).toBe('admin.paymentCoupons.enabled')
    await wrapper.get('#payment-discount-value').setValue('20.00')
    await wrapper.get('[data-test="payment-discount-coupon-form"]').trigger('submit')
    await flushPromises()

    expect(createPaymentDiscountCoupon).toHaveBeenCalledTimes(1)
    const payload = createPaymentDiscountCoupon.mock.calls[0][0]
    expect(payload).toMatchObject({
      discount_type: 'percent',
      discount_value: '20.00',
      currency: 'CNY',
      max_uses: 0,
      target_user_id: null,
      enabled: true,
      notes: '',
      order_types: ['balance', 'subscription'],
      plan_ids: [],
    })
    expect(payload).not.toHaveProperty('code')
    expect(payload).not.toHaveProperty('per_user_max_uses')
    wrapper.unmount()
  })

  it('normalizes a valid custom code and rejects an invalid code before requesting the API', async () => {
    const wrapper = await mountPanel()

    await wrapper.get('[data-test="create-payment-coupon"]').trigger('click')
    await flushPromises()
    await wrapper.get('#payment-discount-value').setValue('15')
    await wrapper.get('#payment-discount-code').setValue('payment-15')
    await wrapper.get('[data-test="payment-discount-coupon-form"]').trigger('submit')
    await flushPromises()

    expect(createPaymentDiscountCoupon).toHaveBeenCalledWith(expect.objectContaining({ code: 'PAYMENT-15' }))

    await wrapper.get('[data-test="create-payment-coupon"]').trigger('click')
    await flushPromises()
    await wrapper.get('#payment-discount-value').setValue('15')
    await wrapper.get('#payment-discount-code').setValue('short')
    await wrapper.get('[data-test="payment-discount-coupon-form"]').trigger('submit')

    expect(createPaymentDiscountCoupon).toHaveBeenCalledTimes(1)
    expect(showError).toHaveBeenCalledWith('admin.paymentCoupons.invalidForm')
    wrapper.unmount()
  })

  it('includes the loaded version in updates for compare-and-swap protection', async () => {
    const wrapper = await mountPanel([coupon])

    await wrapper.get('[data-test="edit-payment-coupon"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="payment-discount-coupon-form"]').trigger('submit')
    await flushPromises()

    expect(updatePaymentDiscountCoupon).toHaveBeenCalledWith(17, expect.objectContaining({
      version: 7,
      discount_type: 'percent',
      target_user_id: null,
    }))
    expect(updatePaymentDiscountCoupon.mock.calls[0][1]).not.toHaveProperty('code')
    wrapper.unmount()
  })

  it('keeps the editor stable when native number inputs provide numeric values', async () => {
    const wrapper = await mountPanel([coupon])

    await wrapper.get('[data-test="edit-payment-coupon"]').trigger('click')
    await flushPromises()
    await wrapper.get('#payment-discount-per-user').setValue(2)
    await wrapper.get('#payment-discount-target-user').setValue(42)
    await flushPromises()

    expect(wrapper.get('[data-test="dialog"]').exists()).toBe(true)
    await wrapper.get('[data-test="payment-discount-coupon-form"]').trigger('submit')
    await flushPromises()

    expect(updatePaymentDiscountCoupon).toHaveBeenCalledWith(17, expect.objectContaining({
      per_user_max_uses: 2,
      target_user_id: 42,
    }))
    expect(wrapper.find('[data-test="dialog"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('masks codes by default, then reveals, hides, and copies only on explicit actions', async () => {
    const wrapper = await mountPanel([coupon])

    expect(wrapper.get('[data-test="payment-coupon-code"]').text()).toBe('********')
    expect(wrapper.text()).not.toContain('PAYMENT20')

    await wrapper.get('[data-test="payment-coupon-code-visibility"]').trigger('click')
    expect(wrapper.get('[data-test="payment-coupon-code"]').text()).toBe('PAYMENT20')

    await wrapper.get('[data-test="payment-coupon-code-copy"]').trigger('click')
    expect(copyToClipboard).toHaveBeenCalledWith('PAYMENT20', 'admin.paymentCoupons.codeCopied')

    await wrapper.get('[data-test="payment-coupon-code-visibility"]').trigger('click')
    expect(wrapper.get('[data-test="payment-coupon-code"]').text()).toBe('********')
    wrapper.unmount()
  })

  it('saves a subscription-only scope with all subscription plans', async () => {
    const wrapper = await mountPanel()

    await wrapper.get('[data-test="create-payment-coupon"]').trigger('click')
    await flushPromises()
    await wrapper.get('#payment-discount-value').setValue('20')
    await wrapper.get('#payment-coupon-scope-subscription').setValue(true)
    await wrapper.get('#payment-coupon-plan-mode-all').setValue(true)
    await wrapper.get('[data-test="payment-discount-coupon-form"]').trigger('submit')
    await flushPromises()

    expect(createPaymentDiscountCoupon).toHaveBeenCalledWith(expect.objectContaining({
      order_types: ['subscription'],
      plan_ids: [],
    }))
    wrapper.unmount()
  })

  it('saves the selected subscription plan IDs, including plans that are no longer for sale', async () => {
    const wrapper = await mountPanel()

    await wrapper.get('[data-test="create-payment-coupon"]').trigger('click')
    await flushPromises()
    await wrapper.get('#payment-discount-value').setValue('20')
    await wrapper.get('#payment-coupon-scope-subscription').setValue(true)
    await wrapper.get('#payment-coupon-plan-mode-specific').setValue(true)
    await wrapper.get('[data-test="payment-coupon-plan-301"]').setValue(true)
    await wrapper.get('[data-test="payment-coupon-plan-302"]').setValue(true)
    await wrapper.get('[data-test="payment-discount-coupon-form"]').trigger('submit')
    await flushPromises()

    expect(createPaymentDiscountCoupon).toHaveBeenCalledWith(expect.objectContaining({
      order_types: ['subscription'],
      plan_ids: [301, 302],
    }))
    wrapper.unmount()
  })

  it('distinguishes same-name plans by their billing period in the selection list', async () => {
    getPlans.mockResolvedValue({
      data: [
        { id: 401, name: 'Plus', for_sale: true, validity_days: 1, validity_unit: 'month' },
        { id: 402, name: 'Plus', for_sale: true, validity_days: 3, validity_unit: 'month' },
      ],
    })
    const wrapper = await mountPanel()

    await wrapper.get('[data-test="create-payment-coupon"]').trigger('click')
    await flushPromises()
    await wrapper.get('#payment-coupon-scope-subscription').setValue(true)
    await wrapper.get('#payment-coupon-plan-mode-specific').setValue(true)

    expect(wrapper.text()).toContain('1 payment.validityUnits.monthOne')
    expect(wrapper.text()).toContain('3 payment.validityUnits.monthMany')
    wrapper.unmount()
  })

  it('clears selected plan IDs after switching the scope to balance only', async () => {
    const scopedCoupon = { ...coupon, order_types: ['subscription'] as const, plan_ids: [301] }
    const wrapper = await mountPanel([scopedCoupon])

    await wrapper.get('[data-test="edit-payment-coupon"]').trigger('click')
    await flushPromises()
    expect((wrapper.get('[data-test="payment-coupon-plan-301"]').element as HTMLInputElement).checked).toBe(true)
    await wrapper.get('#payment-coupon-scope-balance').setValue(true)
    await wrapper.get('[data-test="payment-discount-coupon-form"]').trigger('submit')
    await flushPromises()

    expect(updatePaymentDiscountCoupon).toHaveBeenCalledWith(17, expect.objectContaining({
      order_types: ['balance'],
      plan_ids: [],
    }))
    wrapper.unmount()
  })

  it('requires a successful plan refresh and retains a deleted historical plan ID on retry', async () => {
    getPlans.mockRejectedValueOnce(new Error('catalog unavailable')).mockResolvedValueOnce({ data: subscriptionPlans })
    const scopedCoupon = { ...coupon, order_types: ['subscription'] as const, plan_ids: [999] }
    const wrapper = await mountPanel([scopedCoupon])

    await wrapper.get('[data-test="edit-payment-coupon"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="payment-coupon-plans-error"]').exists()).toBe(true)
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()

    await wrapper.get('[data-test="payment-coupon-plans-retry"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="payment-coupon-missing-plans"]').exists()).toBe(true)
    expect((wrapper.get('[data-test="payment-coupon-missing-plan-999"]').element as HTMLInputElement).checked).toBe(true)

    await wrapper.get('[data-test="payment-discount-coupon-form"]').trigger('submit')
    await flushPromises()
    expect(updatePaymentDiscountCoupon).toHaveBeenCalledWith(17, expect.objectContaining({ plan_ids: [999] }))
    wrapper.unmount()
  })

  it('loads both bounded usage and audit histories for the selected coupon', async () => {
    getPaymentDiscountCouponUsages.mockResolvedValue({
      data: {
        items: [{
          id: 1,
          order_id: 99,
          user_id: 42,
          status: 'consumed',
          original_amount: '10.00',
          discount_amount: '2.00',
          pay_amount: '8.00',
          currency: 'CNY',
          created_at: '2030-02-01T00:00:00Z',
          updated_at: '2030-02-01T00:01:00Z',
          username: 'coupon-user',
        }, {
          id: 3,
          order_id: 100,
          user_id: 43,
          status: 'released',
          original_amount: '20.00',
          discount_amount: '4.00',
          pay_amount: '16.00',
          currency: 'CNY',
          created_at: '2030-02-02T00:00:00Z',
          updated_at: '2030-02-02T00:01:00Z',
        }],
        total: 2,
      },
    })
    getPaymentDiscountCouponAudits.mockResolvedValue({
      data: {
        items: [{
          id: 2,
          admin_user_id: 5,
          action: 'updated',
          detail: JSON.stringify({
            before: { order_types: ['balance'], plan_ids: [] },
            after: {
              code: 'PAYMENT20',
              discount_type: 'percent',
              discount_value: '20.00',
              currency: 'CNY',
              max_uses: 10,
              per_user_max_uses: 2,
              target_user_id: null,
              starts_at: '2030-01-01T00:00:00Z',
              expires_at: '2030-12-31T23:59:00Z',
              enabled: true,
              version: 7,
              notes: 'new customer discount',
              order_types: ['subscription'],
              plan_ids: [301],
            },
          }),
          created_at: '2030-02-01T00:00:00Z',
        }],
        total: 1,
      },
    })
    const wrapper = await mountPanel([coupon])

    await wrapper.get('[data-test="payment-coupon-history"]').trigger('click')
    await flushPromises()

    expect(getPaymentDiscountCouponUsages).toHaveBeenCalledWith(17, { page: 1, page_size: 20 })
    expect(getPaymentDiscountCouponAudits).toHaveBeenCalledWith(17, { page: 1, page_size: 20 })
    expect(wrapper.text()).toContain('#99')
    expect(wrapper.text()).toContain('coupon-user')
    expect(wrapper.text()).toContain('admin.paymentCoupons.deletedUser')
    expect(wrapper.text()).toContain('admin.paymentCoupons.auditActions.updated')
    expect(wrapper.text()).toContain('admin.paymentCoupons.auditBefore')
    expect(wrapper.text()).toContain('admin.paymentCoupons.auditFields.discountType')
    expect(wrapper.text()).toContain('admin.paymentCoupons.auditFields.orderTypes')
    expect(wrapper.text()).not.toContain('PAYMENT20')
    wrapper.unmount()
  })
})

describe('payment coupon entry points', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    promoList.mockResolvedValue({ items: [], total: 0 })
  })

  it('keeps registration gifts separate from payment discounts', async () => {
    const wrapper = shallowMount(PromoCodesView, {
      global: {
        stubs: {
          AppLayout: { template: '<main><slot /></main>' },
          TablePageLayout: { template: '<section><slot name="filters" /><slot name="table" /><slot name="pagination" /></section>' },
        },
      },
    })
    await flushPromises()

    expect(wrapper.findComponent(PaymentDiscountCouponsPanel).exists()).toBe(false)
    wrapper.unmount()
  })

  it('mounts payment discounts from the order-management page', () => {
    const wrapper = shallowMount(AdminPaymentCouponsView, {
      global: {
        stubs: {
          AppLayout: { template: '<main><slot /></main>' },
          RouterLink: { template: '<a><slot /></a>' },
          PaymentDiscountCouponsPanel: { template: '<div data-test="payment-discount-panel" />' },
        },
      },
    })

    expect(wrapper.find('[data-test="payment-discount-panel"]').exists()).toBe(true)
    wrapper.unmount()
  })
})
