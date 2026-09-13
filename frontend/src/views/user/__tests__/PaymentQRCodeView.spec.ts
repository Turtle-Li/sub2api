import { describe, expect, it, vi } from 'vitest'
import { shallowMount } from '@vue/test-utils'

const routeState = vi.hoisted(() => ({
  query: {} as Record<string, unknown>,
}))
const routerPush = vi.hoisted(() => vi.fn())

vi.mock('vue-router', async () => {
  const actual = await vi.importActual<typeof import('vue-router')>('vue-router')
  return {
    ...actual,
    useRoute: () => routeState,
    useRouter: () => ({ push: routerPush }),
  }
})

import PaymentQRCodeView from '../PaymentQRCodeView.vue'
import PaymentStatusPanel from '@/components/payment/PaymentStatusPanel.vue'

describe('PaymentQRCodeView', () => {
  it('uses the shared status shell and never substitutes a hosted URL as QR data', () => {
    routeState.query = {
      order_id: '42',
      pay_url: 'https://pay.example.com/hosted/42',
      expires_at: '2026-09-13T12:00:00.000Z',
      payment_type: 'wxpay',
      out_trade_no: 'sub2_42',
    }

    const wrapper = shallowMount(PaymentQRCodeView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          PaymentStatusPanel: true,
        },
      },
    })
    const panel = wrapper.findComponent(PaymentStatusPanel)

    expect(panel.props()).toMatchObject({
      orderId: 42,
      qrCode: '',
      payUrl: 'https://pay.example.com/hosted/42',
      outTradeNo: 'sub2_42',
    })
  })
})
