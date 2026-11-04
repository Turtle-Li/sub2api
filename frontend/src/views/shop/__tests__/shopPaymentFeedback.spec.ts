import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import ShopProductView from '../ShopProductView.vue'
import { PAYMENT_RECOVERY_STORAGE_KEY, readPaymentRecoverySnapshot } from '@/components/payment/paymentFlow'

const createOrder = vi.hoisted(() => vi.fn())
const push = vi.hoisted(() => vi.fn())
vi.mock('vue-router', () => ({ useRoute: () => ({ params: { id: '1' } }), useRouter: () => ({ push }) }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ contactInfo: '' }) }))
vi.mock('@/api/payment', () => ({ paymentAPI: { getCheckoutInfo: async () => ({ data: { methods: { alipay: { currency: 'CNY' } } } }) } }))
vi.mock('@/api/shop', () => ({ shopAPI: {
  product: async () => ({ data: { id: 1, revision: 1, name: '测试代充', price: 0.1, enabled: true, fulfillment_mode: 'session_topup', duration_value: 1, duration_unit: 'month' } }),
  createOrder,
} }))
const PaymentPanel = defineComponent({ name: 'PaymentStatusPanel', emits: ['done', 'settled'], template: '<div data-test="checkout" />' })
const options = { global: { stubs: {
  AppLayout: { template: '<main><slot /></main>' },
  RouterLink: { template: '<a><slot /></a>' },
  BaseDialog: { template: '<section><slot /></section>' },
  PaymentStatusPanel: PaymentPanel,
} } }
enableAutoUnmount(afterEach)
beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  createOrder.mockResolvedValue({ data: { order_id: 42, amount: 0.1, pay_amount: 0.1, payment_type: 'alipay', pay_url: '', qr_code: '', expires_at: '2099-01-01T00:00:00Z', resume_token: 'synthetic-resume-token', out_trade_no: 'test-42' } })
})
async function openCheckout(wrapper: ReturnType<typeof mount>) {
  await flushPromises()
  const button = wrapper.findAll('button').find(item => item.text() === '购买')
  expect(button).toBeDefined()
  await button?.trigger('click')
  await flushPromises()
}
describe('shop checkout feedback', () => {
  it.each(['cancelled', 'expired'])('stays on the purchase page after %s and creates a fresh attempt only on the next purchase', async outcome => {
    // Given: a checkout is open.
    const wrapper = mount(ShopProductView, options)
    await openCheckout(wrapper)
    const firstKey = createOrder.mock.calls[0]?.[1]
    // When: the server-confirmed terminal outcome is dismissed.
    wrapper.getComponent(PaymentPanel).vm.$emit('settled', outcome)
    wrapper.getComponent(PaymentPanel).vm.$emit('done')
    await flushPromises()
    // Then: no detail navigation or implicit purchase occurs; the next purchase is fresh.
    expect(push).not.toHaveBeenCalled()
    expect(wrapper.findComponent(PaymentPanel).exists()).toBe(false)
    expect(createOrder).toHaveBeenCalledTimes(1)
    await openCheckout(wrapper)
    expect(createOrder.mock.calls[1]?.[1]).not.toBe(firstKey)
  })
  it('retains the same checkout attempt after a pending window is dismissed', async () => {
    const wrapper = mount(ShopProductView, options)
    await openCheckout(wrapper)
    const firstKey = createOrder.mock.calls[0]?.[1]
    wrapper.getComponent(PaymentPanel).vm.$emit('done')
    await flushPromises()
    expect(push).not.toHaveBeenCalled()
    await openCheckout(wrapper)
    expect(createOrder.mock.calls[1]?.[1]).toBe(firstKey)
  })
  it('keeps the payment success reminder visible until the buyer continues to the order', async () => {
    const wrapper = mount(ShopProductView, options)
    await openCheckout(wrapper)
    wrapper.getComponent(PaymentPanel).vm.$emit('settled', 'success')
    await flushPromises()
    expect(push).not.toHaveBeenCalled()
    expect(wrapper.findComponent(PaymentPanel).exists()).toBe(true)
    wrapper.getComponent(PaymentPanel).vm.$emit('done')
    await flushPromises()
    expect(push).toHaveBeenCalledTimes(1)
    expect(push).toHaveBeenCalledWith('/shop/orders/42')
  })
  it('binds provider-return recovery to the shop checkout before displaying payment', async () => {
    const wrapper = mount(ShopProductView, options)
    await openCheckout(wrapper)
    const snapshot = readPaymentRecoverySnapshot(localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY))
    expect(snapshot).toMatchObject({ orderId: 42, orderType: 'manual_shop', resumeToken: 'synthetic-resume-token' })
  })
})
