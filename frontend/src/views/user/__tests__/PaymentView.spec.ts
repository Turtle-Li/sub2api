import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, shallowMount } from '@vue/test-utils'
import PaymentView from '../PaymentView.vue'
import {
  PAYMENT_RECOVERY_STORAGE_KEY,
  RESET_CARD_CHECKOUT_ATTEMPT_STORAGE_KEY,
} from '@/components/payment/paymentFlow'
import { formatPaymentAmount } from '@/components/payment/currency'
import AmountInput from '@/components/payment/AmountInput.vue'
import SubscriptionPlanCard from '@/components/payment/SubscriptionPlanCard.vue'
import en from '@/i18n/locales/en'
import zh from '@/i18n/locales/zh'
import PaymentOrderRail from '@/components/payment/PaymentOrderRail.vue'
import PaymentMethodSelector from '@/components/payment/PaymentMethodSelector.vue'
import ResetCardShop from '@/components/payment/ResetCardShop.vue'
import PaymentStatusPanel from '@/components/payment/PaymentStatusPanel.vue'
import PaymentDiscountCodeInput from '@/components/payment/PaymentDiscountCodeInput.vue'
import type { CheckoutInfoResponse, MethodLimit, SubscriptionPlan } from '@/types/payment'

const routeState = vi.hoisted(() => ({
  path: '/purchase',
  query: {} as Record<string, unknown>,
}))

const routerReplace = vi.hoisted(() => vi.fn())
const routerPush = vi.hoisted(() => vi.fn())
const routerResolve = vi.hoisted(() => vi.fn(() => ({ href: '/payment/stripe?mock=1' })))
const createOrder = vi.hoisted(() => vi.fn())
const refreshUser = vi.hoisted(() => vi.fn())
const fetchActiveSubscriptions = vi.hoisted(() => vi.fn().mockResolvedValue(undefined))
const showError = vi.hoisted(() => vi.fn())
const showInfo = vi.hoisted(() => vi.fn())
const showWarning = vi.hoisted(() => vi.fn())
const getCheckoutInfo = vi.hoisted(() => vi.fn())
const getCouponQuote = vi.hoisted(() => vi.fn())
const bridgeInvoke = vi.hoisted(() => vi.fn())
const translate = vi.hoisted(() => vi.fn((key: string) => key))
const isMobileDevice = vi.hoisted(() => vi.fn(() => true))
// Public settings live in a reactive holder so tests can flip feature flags after mount
// and exercise the watchers that react to them.
const appStoreState = vi.hoisted(() => ({
  setPublicSettings: (_value: Record<string, unknown> | undefined) => {},
}))
const subscriptionStoreState = vi.hoisted(() => ({
  setActiveSubscriptions: (_value: unknown[]) => {},
}))

vi.mock('vue-router', async () => {
  const actual = await vi.importActual<typeof import('vue-router')>('vue-router')
  return {
    ...actual,
    useRoute: () => routeState,
    useRouter: () => ({
      replace: routerReplace,
      push: routerPush,
      resolve: routerResolve,
    }),
  }
})

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: translate,
    }),
  }
})

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    user: {
      id: 9,
      username: 'demo-user',
      balance: 0,
    },
    refreshUser,
  }),
}))

vi.mock('@/stores/payment', () => ({
  usePaymentStore: () => ({
    createOrder,
  }),
}))

vi.mock('@/stores/subscriptions', async () => {
  const { reactive } = await import('vue')
  const state = reactive({ activeSubscriptions: [] as unknown[] })
  subscriptionStoreState.setActiveSubscriptions = (value) => {
    state.activeSubscriptions = value
  }
  return {
    useSubscriptionStore: () => ({
      get activeSubscriptions() {
        return state.activeSubscriptions
      },
      fetchActiveSubscriptions,
    }),
  }
})

vi.mock('@/stores', async () => {
  const { reactive } = await import('vue')
  const state = reactive({ cachedPublicSettings: undefined as Record<string, unknown> | undefined })
  appStoreState.setPublicSettings = (value) => {
    state.cachedPublicSettings = value
  }
  return {
    useAppStore: () => ({
      showError,
      showInfo,
      showWarning,
      get cachedPublicSettings() {
        return state.cachedPublicSettings
      },
    }),
  }
})

vi.mock('@/api/payment', () => ({
  paymentAPI: {
    getCheckoutInfo,
    getCouponQuote,
  },
}))

vi.mock('@/utils/device', () => ({
  isMobileDevice,
}))

enableAutoUnmount(afterEach)

afterEach(() => {
  isMobileDevice.mockReset().mockReturnValue(true)
  subscriptionStoreState.setActiveSubscriptions([])
})

function checkoutInfoFixture(overrides: Partial<CheckoutInfoResponse> = {}) {
  const wxpayMethod: MethodLimit = {
    daily_limit: 0,
    daily_used: 0,
    daily_remaining: 0,
    single_min: 0,
    single_max: 0,
    fee_rate: 0,
    available: true,
  }
  const data: CheckoutInfoResponse = {
    methods: {
      wxpay: wxpayMethod,
    },
    global_min: 0,
    global_max: 0,
    plans: [],
    balance_disabled: false,
    balance_recharge_multiplier: 1,
    subscription_usd_to_cny_rate: 0,
    recharge_fee_rate: 0,
    recharge_options: [],
    help_text: '',
    help_image_url: '',
    stripe_publishable_key: '',
  }

  return {
    data: { ...data, ...overrides },
  }
}

function checkoutInfoWithPlansFixture(options: {
  checkout?: Partial<CheckoutInfoResponse>
  method?: Partial<MethodLimit>
  plan?: Partial<SubscriptionPlan>
} = {}) {
  const base = checkoutInfoFixture(options.checkout).data
  const plan: SubscriptionPlan = {
    id: 7,
    group_id: 3,
    name: 'Starter',
    description: '',
    price: 128,
    original_price: 0,
    validity_days: 30,
    validity_unit: 'day',
    rate_multiplier: 1,
    daily_limit_usd: null,
    weekly_limit_usd: null,
    monthly_limit_usd: null,
    features: [],
    group_platform: 'openai',
    sort_order: 1,
    for_sale: true,
    group_name: 'OpenAI',
    ...options.plan,
  }

  return {
    data: {
      ...base,
      methods: {
        ...base.methods,
        wxpay: {
          ...base.methods.wxpay,
          ...options.method,
        },
      },
      plans: [plan],
    },
  }
}

function jsapiOrderFixture(resumeToken: string) {
  return {
    order_id: 123,
    amount: 88,
    pay_amount: 88,
    fee_rate: 0,
    expires_at: '2099-01-01T00:10:00.000Z',
    payment_type: 'wxpay',
    out_trade_no: 'sub2_jsapi_123',
    result_type: 'jsapi_ready' as const,
    resume_token: resumeToken,
    jsapi: {
      appId: 'wx123',
      timeStamp: '1712345678',
      nonceStr: 'nonce',
      package: 'prepay_id=wx123',
      signType: 'RSA',
      paySign: 'signed',
    },
  }
}

function oauthOrderFixture() {
  return {
    order_id: 456,
    amount: 128,
    pay_amount: 128,
    fee_rate: 0,
    expires_at: '2099-01-01T00:10:00.000Z',
    payment_type: 'wxpay',
    result_type: 'oauth_required' as const,
    oauth: {
      authorize_url: '/api/v1/auth/oauth/wechat/payment/start?payment_type=wxpay&redirect=%2Fpurchase%3Ffrom%3Dwechat',
      appid: 'wx123',
      scope: 'snsapi_base',
      redirect_url: '/auth/wechat/payment/callback',
    },
  }
}

async function resetCardWechatResumeToken(idempotencyKey: string): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(idempotencyKey))
  const hash = Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, '0')).join('')
  const tokenPayload = btoa(JSON.stringify({
    tk: 'wechat_payment_resume',
    ot: 'reset_card',
    ikh: hash,
  })).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
  return `${tokenPayload}.signature`
}

async function mountSubscriptionConfirm(options: Parameters<typeof checkoutInfoWithPlansFixture>[0] = {}) {
  vi.useRealTimers()
  routeState.path = '/purchase'
  routeState.query = {
    tab: 'subscription',
    group: '3',
  }
  routerReplace.mockReset().mockResolvedValue(undefined)
  routerPush.mockReset().mockResolvedValue(undefined)
  routerResolve.mockClear()
  createOrder.mockReset()
  refreshUser.mockReset()
  fetchActiveSubscriptions.mockReset().mockResolvedValue(undefined)
  showError.mockReset()
  showInfo.mockReset()
  showWarning.mockReset()
  getCheckoutInfo.mockReset().mockResolvedValue(checkoutInfoWithPlansFixture(options))
  bridgeInvoke.mockReset()
  window.localStorage.clear()
  ;(window as Window & { WeixinJSBridge?: { invoke: typeof bridgeInvoke } }).WeixinJSBridge = undefined

  const wrapper = shallowMount(PaymentView, {
    global: {
      stubs: {
        AppLayout: {
          template: '<div><slot /></div>',
        },
        Teleport: true,
        Transition: false,
      },
    },
  })
  await flushPromises()
  await flushPromises()
  return wrapper
}

async function mountSubscriptionPlanList(planCount: number) {
  vi.useRealTimers()
  routeState.path = '/purchase'
  routeState.query = { tab: 'subscription' }
  routerReplace.mockReset().mockResolvedValue(undefined)
  routerPush.mockReset().mockResolvedValue(undefined)
  routerResolve.mockClear()
  createOrder.mockReset()
  refreshUser.mockReset()
  fetchActiveSubscriptions.mockReset().mockResolvedValue(undefined)
  showError.mockReset()
  showInfo.mockReset()
  showWarning.mockReset()
  const basePlan = checkoutInfoWithPlansFixture().data.plans[0]
  const plans = Array.from({ length: planCount }, (_, index) => ({
    ...basePlan,
    id: index + 1,
    name: `Plan ${index + 1}`,
  }))
  getCheckoutInfo.mockReset().mockResolvedValue(checkoutInfoFixture({ plans }))
  bridgeInvoke.mockReset()
  window.localStorage.clear()
  ;(window as Window & { WeixinJSBridge?: { invoke: typeof bridgeInvoke } }).WeixinJSBridge = undefined

  const wrapper = shallowMount(PaymentView, {
    global: {
      stubs: {
        AppLayout: {
          template: '<div><slot /></div>',
        },
        Teleport: true,
        Transition: false,
      },
    },
  })
  await flushPromises()
  await flushPromises()
  return wrapper
}

async function mountSubscriptionPlans(
  plans: SubscriptionPlan[],
  query: Record<string, unknown> = { tab: 'subscription' },
) {
  vi.useRealTimers()
  routeState.path = '/purchase'
  routeState.query = query
  routerReplace.mockReset().mockResolvedValue(undefined)
  routerPush.mockReset().mockResolvedValue(undefined)
  routerResolve.mockClear()
  createOrder.mockReset()
  refreshUser.mockReset()
  fetchActiveSubscriptions.mockReset().mockResolvedValue(undefined)
  showError.mockReset()
  showInfo.mockReset()
  showWarning.mockReset()
  getCheckoutInfo.mockReset().mockResolvedValue(checkoutInfoFixture({ plans }))
  bridgeInvoke.mockReset()
  window.localStorage.clear()
  ;(window as Window & { WeixinJSBridge?: { invoke: typeof bridgeInvoke } }).WeixinJSBridge = undefined

  const wrapper = shallowMount(PaymentView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        Teleport: true,
        Transition: false,
      },
    },
  })
  await flushPromises()
  await flushPromises()
  return wrapper
}

describe('PaymentView help text', () => {
  beforeEach(() => {
    vi.useRealTimers()
    routeState.path = '/purchase'
    routeState.query = {}
    createOrder.mockReset()
    window.localStorage.clear()
  })

  async function mountHelp(help_text: string, help_image_url = '') {
    getCheckoutInfo.mockReset().mockResolvedValue(checkoutInfoFixture({ help_text, help_image_url }))
    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    return wrapper
  }

  it('renders headings, emphasis, links, and lists in payment help without starting checkout', async () => {
    const wrapper = await mountHelp('## Recharge help\n\n**Read first**\n\n- [Contact support](https://example.com/help)')
    const help = wrapper.get('.markdown-body')
    expect(help.get('h2').text()).toBe('Recharge help')
    expect(help.get('strong').text()).toBe('Read first')
    expect(help.get('li a').attributes('href')).toBe('https://example.com/help')
    expect(createOrder).not.toHaveBeenCalled()
  })

  it('removes scripts, event handlers, and unsafe URLs from rendered help', async () => {
    const wrapper = await mountHelp([
      '<script>alert(1)</script>',
      '<img src="https://example.com/help.png" onerror="alert(1)">',
      '[Unsafe](javascript:alert%281%29)',
      '[Support](https://example.com/help)',
    ].join('\n\n'))
    const help = wrapper.get('.markdown-body')
    expect(help.find('script').exists()).toBe(false)
    expect(help.get('img').attributes('onerror')).toBeUndefined()
    expect(help.findAll('a').map(link => link.attributes('href'))).toEqual([undefined, 'https://example.com/help'])
  })

  it('keeps plain-text soft line breaks and the separate help image preview', async () => {
    const wrapper = await mountHelp('First line\nSecond line', 'https://example.com/help.png')
    const help = wrapper.get('.markdown-body')
    expect(help.get('p').text()).toBe('First line\nSecond line')
    expect(help.find('br').exists()).toBe(false)
    await wrapper.get('img').trigger('click')
    expect(wrapper.findAll('img')).toHaveLength(2)
    expect(wrapper.findAll('img')[1].attributes('src')).toBe('https://example.com/help.png')
  })

  it('keeps image-only help without an empty Markdown container', async () => {
    const wrapper = await mountHelp('', 'https://example.com/help.png')
    expect(wrapper.find('.markdown-body').exists()).toBe(false)
    expect(wrapper.get('img').attributes('src')).toBe('https://example.com/help.png')
  })
})

describe('PaymentView subscription plan grid', () => {
  it('refreshes linked-group catalog data on focus while preserving the selected plan', async () => {
    const plan = { ...checkoutInfoWithPlansFixture().data.plans[0], name: 'Plus', validity_days: 1, validity_unit: 'months', period_label: 'month', weekly_limit_usd: 110 }
    const wrapper = await mountSubscriptionPlans([plan])
    let finish: ((value: unknown) => void) | undefined
    getCheckoutInfo.mockReset().mockImplementation(() => new Promise(resolve => { finish = resolve }))
    window.dispatchEvent(new Event('focus'))
    window.dispatchEvent(new Event('focus'))
    expect(getCheckoutInfo).toHaveBeenCalledTimes(1)
    finish?.(checkoutInfoFixture({ plans: [{ ...plan, weekly_limit_usd: 220, monthly_limit_usd: 880, rate_multiplier: 0.5, description: 'Updated group' }] }))
    await flushPromises()
    const card = wrapper.findComponent(SubscriptionPlanCard)
    expect(card.props('plan')).toMatchObject({ id: plan.id, weekly_limit_usd: 220, rate_multiplier: 0.5, description: 'Updated group' })
    expect(card.props('selected')).toBe(true)
    wrapper.unmount()
    window.dispatchEvent(new Event('focus'))
    expect(getCheckoutInfo).toHaveBeenCalledTimes(1)
  })

  it('keeps the selected plan visible when its period changes during catalog refresh', async () => {
    const base = checkoutInfoWithPlansFixture().data.plans[0]
    const plus = { ...base, id: 1, name: 'Plus', period_label: 'month' }
    const pro = { ...base, id: 2, name: '5X Pro', period_label: 'month' }
    const wrapper = await mountSubscriptionPlans([plus, pro])
    getCheckoutInfo.mockResolvedValue(checkoutInfoFixture({ plans: [{ ...plus, period_label: 'quarter' }, pro] }))
    window.dispatchEvent(new Event('focus'))
    await flushPromises()
    const cards = wrapper.findAllComponents(SubscriptionPlanCard)
    expect(cards).toHaveLength(1)
    expect(cards[0].props('plan').id).toBe(plus.id)
    expect(cards[0].props('selected')).toBe(true)
    expect(wrapper.findAll('button').find(button => button.text().includes('payment.periods.quarter'))?.attributes('aria-pressed')).toBe('true')
  })

  it('clears an unavailable selected plan after catalog refresh', async () => {
    const plan = { ...checkoutInfoWithPlansFixture().data.plans[0], name: 'Plus', validity_days: 1, validity_unit: 'months', period_label: 'month' }
    const wrapper = await mountSubscriptionPlans([plan])
    getCheckoutInfo.mockResolvedValue(checkoutInfoFixture({ plans: [] }))
    window.dispatchEvent(new Event('focus'))
    await flushPromises()
    expect(wrapper.findComponent(PaymentOrderRail).props('disabled')).toBe(true)
    expect(wrapper.findAllComponents(SubscriptionPlanCard)).toHaveLength(0)
  })

  it('refuses a gated recharge deep link and does not enable payment', async () => {
    routeState.query = { tab: 'recharge', amount: '599' }
    getCheckoutInfo.mockReset().mockResolvedValue(checkoutInfoFixture({ recharge_mode: 'fixed', recharge_options: [{ amount: 599, enabled: true, sort_order: 0, eligibility: { can_purchase: false, reason: 'minimum_recharge', required_total_recharge: 1000, current_total_recharge: 0 } }] }))
    const wrapper = shallowMount(PaymentView, { global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, Teleport: true, Transition: false } } })
    await flushPromises()
    const rail = wrapper.findComponent(PaymentOrderRail)
    expect(rail.props('disabled')).toBe(true)
    expect(rail.props('notice')).toBe('payment.eligibility.minimum')
  })

  it.each([3, 4, 6])('keeps %i plans on the existing mobile/tablet/desktop grid', async (planCount) => {
    const wrapper = await mountSubscriptionPlanList(planCount)
    const cards = wrapper.findAllComponents(SubscriptionPlanCard)

    expect(cards).toHaveLength(planCount)
    expect([...(cards[0].element.parentElement?.classList ?? [])]).toEqual(expect.arrayContaining([
      'grid',
      'grid-cols-1',
      'sm:grid-cols-2',
      'xl:grid-cols-3',
    ]))
  })

  it.each([true, false])('keeps the order summary consistent after a period switch (same group: %s)', async (sameGroup) => {
    routeState.query = { tab: 'subscription' }
    const base = checkoutInfoWithPlansFixture().data.plans[0]
    const quarterly = { ...base, id: 31, period_label: 'quarter', price: 324 }
    const annual = { ...base, id: 32, group_id: sameGroup ? base.group_id : 999, period_label: 'year', price: 1152 }
    getCheckoutInfo.mockReset().mockResolvedValue(checkoutInfoFixture({ plans: [quarterly, annual] }))
    const wrapper = shallowMount(PaymentView, { global: { stubs: {
      AppLayout: { template: '<div><slot /></div>' }, Teleport: true, Transition: false,
    } } })
    await flushPromises()
    const rail = wrapper.findComponent(PaymentOrderRail)
    expect(rail.props('disabled')).toBe(true)
    wrapper.findComponent(SubscriptionPlanCard).vm.$emit('select', quarterly)
    await flushPromises()
    expect(rail.props('totalAmount')).toBe(324)
    const year = wrapper.findAll('button').find(button => button.text().includes('payment.periods.year'))!
    await year.trigger('click')
    expect(rail.props('disabled')).toBe(!sameGroup)
    expect(rail.props('totalAmount')).toBe(sameGroup ? 1152 : 0)
    expect(wrapper.findComponent(SubscriptionPlanCard).props('selected')).toBe(sameGroup)
  })

  it('shows monthly plans first and switches the visible set to annual plans', async () => {
    const basePlan = checkoutInfoWithPlansFixture().data.plans[0]
    const wrapper = await mountSubscriptionPlans([
      { ...basePlan, id: 1, name: 'Plus', period_label: 'month' },
      { ...basePlan, id: 2, name: '5X Pro', period_label: 'month' },
      { ...basePlan, id: 3, name: '季度 Plus', period_label: 'quarter' },
      { ...basePlan, id: 4, name: '年度 Plus', period_label: 'year' },
    ])

    expect(wrapper.findAllComponents(SubscriptionPlanCard)).toHaveLength(2)
    expect(wrapper.findAll('button')
      .map(button => button.text())
      .filter(label => label.startsWith('payment.periods.')))
      .toEqual(['payment.periods.month', 'payment.periods.quarter', 'payment.periods.year'])
    const annualTab = wrapper.findAll('button').find(button => button.text().includes('payment.periods.year'))
    expect(annualTab).toBeDefined()
    await annualTab!.trigger('click')
    expect(wrapper.findAllComponents(SubscriptionPlanCard)).toHaveLength(1)
  })

  it('classifies three and twelve calendar months as quarterly and annual plans', async () => {
    const basePlan = checkoutInfoWithPlansFixture().data.plans[0]
    const wrapper = await mountSubscriptionPlans([
      { ...basePlan, id: 5, name: 'Three months', validity_days: 3, validity_unit: 'months' },
      { ...basePlan, id: 6, name: 'Twelve months', validity_days: 12, validity_unit: 'months' },
    ])

    expect(wrapper.findAll('button')
      .map(button => button.text())
      .filter(label => label.startsWith('payment.periods.')))
      .toEqual(['payment.periods.quarter', 'payment.periods.year'])
    expect(wrapper.findComponent(SubscriptionPlanCard).props('plan')).toMatchObject({ id: 5 })

    const annualTab = wrapper.findAll('button').find(button => button.text().includes('payment.periods.year'))
    await annualTab!.trigger('click')
    expect(wrapper.findComponent(SubscriptionPlanCard).props('plan')).toMatchObject({ id: 6 })
  })

  it('automatically selects the eligible monthly Plus plan regardless of catalog order', async () => {
    const basePlan = checkoutInfoWithPlansFixture().data.plans[0]
    const monthlyPlus = { ...basePlan, id: 11, name: 'Plus', period_label: 'month', price: 128 }
    const monthlyPro = { ...basePlan, id: 12, name: '5X Pro', period_label: 'month', price: 328 }
    const quarterlyPlus = { ...basePlan, id: 13, name: 'Plus', period_label: 'quarter', price: 348 }

    for (const plans of [
      [monthlyPro, quarterlyPlus, monthlyPlus],
      [quarterlyPlus, monthlyPlus, monthlyPro],
    ]) {
      const wrapper = await mountSubscriptionPlans(plans)
      const cards = wrapper.findAllComponents(SubscriptionPlanCard)
      const selectedCard = cards.find(card => card.props('selected'))

      expect(wrapper.findComponent(PaymentOrderRail).props('productName')).toBe('Plus')
      expect(selectedCard?.props('plan')).toMatchObject({ id: monthlyPlus.id, period_label: 'month' })
      expect(wrapper.findAll('button').find(button => button.text().includes('payment.periods.month'))?.attributes('aria-pressed')).toBe('true')
      wrapper.unmount()
    }
  })

  it('does not auto-select an unavailable monthly Plus plan', async () => {
    const basePlan = checkoutInfoWithPlansFixture().data.plans[0]
    const wrapper = await mountSubscriptionPlans([
      { ...basePlan, id: 21, name: '5X Pro', period_label: 'month', price: 328 },
      {
        ...basePlan,
        id: 22,
        name: 'Plus',
        period_label: 'month',
        price: 128,
        eligibility: {
          can_purchase: false,
          reason: 'minimum_recharge',
          required_total_recharge: 1000,
          current_total_recharge: 0,
        },
      },
    ])

    expect(wrapper.findComponent(PaymentOrderRail).props('disabled')).toBe(true)
    expect(wrapper.findAllComponents(SubscriptionPlanCard).every(card => !card.props('selected'))).toBe(true)
  })
})

describe('PaymentView recharge rate preview', () => {
  it('uses the selected payment method currency in both locale templates', async () => {
    translate.mockClear()
    routeState.path = '/purchase'
    routeState.query = {}
    getCheckoutInfo.mockReset().mockResolvedValue(checkoutInfoFixture({
      balance_recharge_multiplier: 0.5,
      methods: {
        stripe: {
          ...checkoutInfoFixture().data.methods.wxpay,
          currency: 'USD',
        },
      },
    }))

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    wrapper.getComponent(AmountInput).vm.$emit('update:modelValue', 10)
    await flushPromises()

    expect(translate).toHaveBeenCalledWith('payment.rechargeRatePreview', {
      currency: 'USD',
      usd: '0.50',
    })
    expect(en.payment.rechargeRatePreview).toBe('Current rate: 1 {currency} = {usd} USD')
    expect(zh.payment.rechargeRatePreview).toBe('当前倍率：1 {currency} = {usd} USD')
  })
})

describe('PaymentView subscription confirmation amounts', () => {
  it('shows converted CNY pay amount using the subscription rate, not the balance multiplier', async () => {
    const wrapper = await mountSubscriptionConfirm({
      checkout: {
        balance_recharge_multiplier: 0.14,
        subscription_usd_to_cny_rate: 7.15,
      },
      method: {
        currency: 'CNY',
      },
      plan: {
        price: 9.99,
        original_price: 12.99,
      },
    })

    const rail = wrapper.findComponent(PaymentOrderRail)
    expect(rail.props('baseAmount')).toBe(71.43)
    expect(rail.props('totalAmount')).toBe(71.43)
    // 换算必须使用订阅汇率（×7.15），而不是余额倍率（÷0.14 = 71.36）
    expect(rail.props('totalAmount')).not.toBe(71.36)
    expect(rail.props('actionLabel')).toContain(formatPaymentAmount(71.43, 'CNY'))

    // The plan cards price the same plan through the same rule.
    const card = wrapper.findAllComponents(SubscriptionPlanCard)[0]
    expect(card.props('displayCurrency')).toBe('CNY')
    expect(card.props('usdToCnyRate')).toBe(7.15)
  })

  it('keeps plan price when the subscription rate is not configured or payment currency is not CNY', async () => {
    // opt-in 回归锁：即使余额倍率已配置，未配置订阅汇率时 CNY 订阅仍按 price 直付
    const cnyWrapper = await mountSubscriptionConfirm({
      checkout: {
        balance_recharge_multiplier: 0.14,
        subscription_usd_to_cny_rate: 0,
      },
      method: {
        currency: 'CNY',
      },
      plan: {
        price: 7.99,
      },
    })

    expect(cnyWrapper.findComponent(PaymentOrderRail).props('totalAmount')).toBe(7.99)
    expect(cnyWrapper.findAllComponents(SubscriptionPlanCard)[0].props('usdToCnyRate')).toBe(0)

    const usdWrapper = await mountSubscriptionConfirm({
      checkout: {
        subscription_usd_to_cny_rate: 7.15,
      },
      method: {
        currency: 'USD',
      },
      plan: {
        price: 7.99,
        original_price: 9.99,
      },
    })

    // A USD gateway is charged the plan price as-is even though a rate exists.
    expect(usdWrapper.findComponent(PaymentOrderRail).props('totalAmount')).toBe(7.99)
    const usdCard = usdWrapper.findAllComponents(SubscriptionPlanCard)[0]
    expect(usdCard.props('displayCurrency')).toBe('USD')
    expect(usdCard.props('usdToCnyRate')).toBe(7.15)
  })

  it('adds fee rate after CNY rate conversion to match backend pay_amount', async () => {
    const wrapper = await mountSubscriptionConfirm({
      checkout: {
        subscription_usd_to_cny_rate: 7.15,
        recharge_fee_rate: 2.5,
      },
      method: {
        currency: 'CNY',
      },
      plan: {
        price: 9.99,
      },
    })

    const rail = wrapper.findComponent(PaymentOrderRail)
    expect(rail.props('baseAmount')).toBe(71.43)
    expect(rail.props('feeAmount')).toBe(1.79)
    expect(rail.props('totalAmount')).toBe(73.22)
    expect(rail.props('actionLabel')).toContain(formatPaymentAmount(73.22, 'CNY'))
  })
})

describe('PaymentView payment discount coupons', () => {
  function couponQuote(code = 'SAVE2026', payAmount = '80.00') {
    return {
      code_id: 2026,
      code,
      version: 3,
      original_amount: '100.00',
      discount_amount: '20.00',
      pay_amount: payAmount,
      currency: 'CNY',
      revision: `revision-${code}`,
    }
  }

  function paymentOrder(discount = couponQuote()) {
    return {
      order_id: 701,
      amount: 100,
      pay_amount: Number(discount.pay_amount),
      fee_rate: 0,
      expires_at: '2099-01-01T00:10:00.000Z',
      payment_type: 'wxpay',
      qr_code: 'weixin://wxpay/bizpayurl?pr=coupon-order',
      out_trade_no: 'sub2_coupon_701',
      payment_discount: discount,
    }
  }

  async function mountCouponCheckout() {
    vi.useRealTimers()
    routeState.path = '/purchase'
    routeState.query = {}
    routerReplace.mockReset().mockResolvedValue(undefined)
    routerPush.mockReset().mockResolvedValue(undefined)
    routerResolve.mockClear()
    createOrder.mockReset()
    getCouponQuote.mockReset()
    refreshUser.mockReset()
    fetchActiveSubscriptions.mockReset().mockResolvedValue(undefined)
    showError.mockReset()
    showInfo.mockReset()
    showWarning.mockReset()
    bridgeInvoke.mockReset()
    getCheckoutInfo.mockReset().mockResolvedValue(checkoutInfoFixture({
      recharge_mode: 'fixed',
      recharge_options: [{ amount: 100, label: 'Coupon tier', sort_order: 1, enabled: true }],
    }))
    window.localStorage.clear()
    ;(window as Window & { WeixinJSBridge?: { invoke: typeof bridgeInvoke } }).WeixinJSBridge = undefined

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          BaseDialog: { template: '<div><slot /></div>' },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()
    return wrapper
  }

  async function applyCoupon(wrapper: ReturnType<typeof shallowMount>, code = 'save2026') {
    const input = wrapper.findComponent(PaymentDiscountCodeInput)
    input.vm.$emit('update:modelValue', code)
    await flushPromises()
    input.vm.$emit('apply')
    await flushPromises()
  }

  it('uses a matching quote as the final price and sends its code, revision, and stable key', async () => {
    const wrapper = await mountCouponCheckout()
    getCouponQuote.mockResolvedValue({ data: couponQuote() })
    createOrder.mockResolvedValue(paymentOrder())

    await applyCoupon(wrapper)

    const rail = wrapper.findComponent(PaymentOrderRail)
    expect(getCouponQuote).toHaveBeenCalledWith({
      coupon_code: 'SAVE2026',
      amount: 100,
      payment_type: 'wxpay',
      order_type: 'balance',
    })
    expect(rail.props('totalAmount')).toBe('80.00')
    expect(rail.props('discount')).toMatchObject({ code: 'SAVE2026', pay_amount: '80.00' })
    expect(rail.props('disabled')).toBe(false)

    rail.vm.$emit('submit')
    await flushPromises()

    expect(createOrder).toHaveBeenCalledWith(expect.objectContaining({
      coupon_code: 'SAVE2026',
      coupon_revision: 'revision-SAVE2026',
    }), {
      headers: {
        'Idempotency-Key': expect.stringMatching(/^payment-coupon-/),
      },
    })
    getCheckoutInfo.mockClear()
    window.dispatchEvent(new Event('focus'))
    await flushPromises()
    expect(getCheckoutInfo).not.toHaveBeenCalled()
    expect(wrapper.findComponent(PaymentStatusPanel).props('paymentDiscount')).toMatchObject({
      code: 'SAVE2026',
      pay_amount: '80.00',
    })
  })

  it('requires a fresh coupon quote after refreshing the catalog', async () => {
    const wrapper = await mountCouponCheckout()
    getCouponQuote.mockResolvedValue({ data: couponQuote() })
    await applyCoupon(wrapper)
    window.dispatchEvent(new Event('focus'))
    await flushPromises()
    const rail = wrapper.findComponent(PaymentOrderRail)
    expect(rail.props('discount')).toBeNull()
    expect(rail.props('disabled')).toBe(true)
    expect(rail.props('notice')).toBe('payment.coupon.reapply')
  })

  it('clears a server-rejected stale quote and lets the user apply the same code again', async () => {
    const wrapper = await mountCouponCheckout()
    getCouponQuote.mockResolvedValue({ data: couponQuote() })
    createOrder.mockRejectedValue({ reason: 'COUPON_QUOTE_CHANGED' })
    await applyCoupon(wrapper)
    const rail = wrapper.findComponent(PaymentOrderRail)
    rail.vm.$emit('submit')
    await flushPromises()
    expect(rail.props('discount')).toBeNull()
    expect(rail.props('disabled')).toBe(true)
    expect(wrapper.findComponent(PaymentDiscountCodeInput).props('modelValue')).toBe('save2026')
    expect(rail.props('notice')).toBe('payment.coupon.reapply')
    getCouponQuote.mockResolvedValue({ data: couponQuote('SAVE2026', '75.00') })
    wrapper.findComponent(PaymentDiscountCodeInput).vm.$emit('apply')
    await flushPromises()
    expect(rail.props('totalAmount')).toBe('75.00')
    expect(rail.props('disabled')).toBe(false)
  })

  it('discards a late quote after the code changes', async () => {
    let resolveFirst: ((value: { data: ReturnType<typeof couponQuote> }) => void) | undefined
    let resolveSecond: ((value: { data: ReturnType<typeof couponQuote> }) => void) | undefined
    const first = new Promise<{ data: ReturnType<typeof couponQuote> }>(resolve => { resolveFirst = resolve })
    const second = new Promise<{ data: ReturnType<typeof couponQuote> }>(resolve => { resolveSecond = resolve })
    const wrapper = await mountCouponCheckout()
    getCouponQuote.mockImplementationOnce(() => first).mockImplementationOnce(() => second)

    await applyCoupon(wrapper, 'first2026')
    await applyCoupon(wrapper, 'second2026')
    resolveSecond?.({ data: couponQuote('SECOND2026', '70.00') })
    await flushPromises()
    resolveFirst?.({ data: couponQuote('FIRST2026', '60.00') })
    await flushPromises()

    const rail = wrapper.findComponent(PaymentOrderRail)
    expect(rail.props('totalAmount')).toBe('70.00')
    expect(rail.props('discount')).toMatchObject({ code: 'SECOND2026', pay_amount: '70.00' })
  })

  it('invalidates an applied quote when the selected payment method changes', async () => {
    const wrapper = await mountCouponCheckout()
    getCouponQuote.mockResolvedValue({ data: couponQuote() })

    await applyCoupon(wrapper)
    const rail = wrapper.findComponent(PaymentOrderRail)
    expect(rail.props('disabled')).toBe(false)
    rail.vm.$emit('select-method', 'alipay')
    await flushPromises()

    expect(rail.props('discount')).toBeNull()
    expect(rail.props('disabled')).toBe(true)
    expect(rail.props('notice')).toBe('payment.coupon.reapply')
  })

  it('removes the quote before a normal order is created', async () => {
    const wrapper = await mountCouponCheckout()
    getCouponQuote.mockResolvedValue({ data: couponQuote() })
    createOrder.mockResolvedValue(paymentOrder({
      ...couponQuote(),
      code: 'SERVER-ONLY',
      discount_amount: '0.00',
      pay_amount: '100.00',
    }))

    await applyCoupon(wrapper)
    wrapper.findComponent(PaymentDiscountCodeInput).vm.$emit('remove')
    await flushPromises()

    const rail = wrapper.findComponent(PaymentOrderRail)
    expect(rail.props('discount')).toBeNull()
    expect(rail.props('totalAmount')).toBe(100)
    rail.vm.$emit('submit')
    await flushPromises()
    expect(createOrder).toHaveBeenCalledWith(expect.not.objectContaining({
      coupon_code: expect.anything(),
      coupon_revision: expect.anything(),
    }), undefined)
  })

  it('restores the authoritative discount after OAuth starts without an order or recovery snapshot', async () => {
    const firstVisit = await mountCouponCheckout()
    getCouponQuote.mockResolvedValue({ data: couponQuote() })
    createOrder.mockResolvedValue({ ...oauthOrderFixture(), order_id: 0, amount: 100, pay_amount: 80 })
    await applyCoupon(firstVisit)
    firstVisit.findComponent(PaymentOrderRail).vm.$emit('submit')
    await flushPromises()
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBeNull()
    firstVisit.unmount()

    routeState.query = {
      wechat_resume: '1',
      wechat_resume_token: 'signed-coupon-no-prior-order',
      payment_type: 'wxpay',
      order_type: 'balance',
    }
    createOrder.mockReset().mockResolvedValue(paymentOrder())
    const resumed = shallowMount(PaymentView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          BaseDialog: { template: '<div><slot /></div>' },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()
    const request = createOrder.mock.calls[0]?.[0]
    expect(request).toMatchObject({ wechat_resume_token: 'signed-coupon-no-prior-order' })
    expect(request).not.toHaveProperty('coupon_code')
    expect(request).not.toHaveProperty('coupon_revision')
    expect(resumed.findComponent(PaymentStatusPanel).props('paymentDiscount')).toMatchObject({
      code: 'SAVE2026', original_amount: '100.00', discount_amount: '20.00', pay_amount: '80.00',
    })
    const stored = JSON.parse(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY) || '{}')
    expect(stored.orders['701'].paymentDiscount).toMatchObject({ code: 'SAVE2026', pay_amount: '80.00' })
    resumed.unmount()
  })

  it('keeps a signed WeChat resume coupon as display state without replaying coupon fields', async () => {
    const resumeToken = 'signed-coupon-resume'
    routeState.path = '/purchase'
    routeState.query = {
      wechat_resume: '1',
      wechat_resume_token: resumeToken,
      payment_type: 'wxpay',
      order_type: 'balance',
    }
    routerReplace.mockReset().mockResolvedValue(undefined)
    createOrder.mockReset().mockResolvedValue({
      order_id: 702,
      amount: 100,
      pay_amount: 80,
      fee_rate: 0,
      expires_at: '2099-01-01T00:10:00.000Z',
      payment_type: 'wxpay',
      qr_code: 'weixin://wxpay/bizpayurl?pr=signed-coupon-resume',
      out_trade_no: 'sub2_coupon_702',
    })
    getCheckoutInfo.mockReset().mockResolvedValue(checkoutInfoFixture({
      recharge_mode: 'fixed',
      recharge_options: [{ amount: 100, label: 'Coupon tier', sort_order: 1, enabled: true }],
    }))
    window.localStorage.clear()
    window.localStorage.setItem(PAYMENT_RECOVERY_STORAGE_KEY, JSON.stringify({
      orderId: 701,
      amount: 100,
      qrCode: '',
      expiresAt: '2099-01-01T00:10:00.000Z',
      paymentType: 'wxpay',
      payUrl: '',
      outTradeNo: 'sub2_coupon_701',
      clientSecret: '',
      intentId: '',
      currency: 'CNY',
      countryCode: '',
      paymentEnv: '',
      payAmount: 80,
      orderType: 'balance',
      paymentMode: 'native',
      resumeToken,
      paymentDiscount: couponQuote(),
      createdAt: Date.now(),
    }))

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          BaseDialog: { template: '<div><slot /></div>' },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    const request = createOrder.mock.calls[0]?.[0]
    expect(request).toMatchObject({ wechat_resume_token: resumeToken, order_type: 'balance' })
    expect(request).not.toHaveProperty('coupon_code')
    expect(request).not.toHaveProperty('coupon_revision')
    expect(wrapper.findComponent(PaymentStatusPanel).props('paymentDiscount')).toMatchObject({
      code: 'SAVE2026',
      pay_amount: '80.00',
    })
  })
})

describe('PaymentView desktop deep links', () => {
  beforeEach(() => {
    vi.useRealTimers()
    routeState.path = '/purchase'
    routeState.query = {}
    routerReplace.mockReset().mockResolvedValue(undefined)
    routerPush.mockReset().mockResolvedValue(undefined)
    routerResolve.mockClear()
    createOrder.mockReset()
    refreshUser.mockReset()
    fetchActiveSubscriptions.mockReset().mockResolvedValue(undefined)
    showError.mockReset()
    showInfo.mockReset()
    showWarning.mockReset()
    bridgeInvoke.mockReset()
    window.localStorage.clear()
    ;(window as Window & { WeixinJSBridge?: { invoke: typeof bridgeInvoke } }).WeixinJSBridge = undefined
  })

  it('opens the exact subscription plan selected by the desktop app', async () => {
    routeState.query = {
      source: 'desktop',
      tab: 'subscription',
      plan_id: '8',
      group: '3',
    }
    const basePlan = checkoutInfoWithPlansFixture().data.plans[0]
    getCheckoutInfo.mockResolvedValue(checkoutInfoFixture({
      plans: [
        { ...basePlan, id: 7, name: 'Plus', period_label: 'month' },
        { ...basePlan, id: 8, group_id: 4, name: '5X Pro', period_label: 'quarter' },
      ],
    }))

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          AppLayout: {
            template: '<div><slot /></div>',
          },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(wrapper.findComponent(PaymentOrderRail).props('productName')).toBe('5X Pro')
    expect(wrapper.findAllComponents(SubscriptionPlanCard)).toHaveLength(1)
    expect(wrapper.findComponent(SubscriptionPlanCard).props('selected')).toBe(true)
    expect(wrapper.findComponent(SubscriptionPlanCard).props('plan')).toMatchObject({ id: 8 })
  })

  it('restores the recharge amount selected by the desktop app', async () => {
    routeState.query = {
      source: 'desktop',
      tab: 'recharge',
      amount: '200',
    }
    getCheckoutInfo.mockResolvedValue(checkoutInfoFixture())

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          AppLayout: {
            template: '<div><slot /></div>',
          },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(wrapper.findComponent(AmountInput).props('modelValue')).toBe(200)
  })

  it('uses enabled recharge presets returned by the server', async () => {
    getCheckoutInfo.mockResolvedValue(checkoutInfoFixture({
      recharge_options: [
        { amount: 120, label: 'Popular', sort_order: 20, enabled: true },
        { amount: 30, label: 'Starter', sort_order: 10, enabled: true },
        { amount: 50, label: 'Hidden', sort_order: 5, enabled: false },
      ],
    }))

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    const amountInput = wrapper.findComponent(AmountInput)
    expect(amountInput.props('amounts')).toEqual([30, 120])
    expect(amountInput.props('modelValue')).toBe(30)
    expect(amountInput.props('options')).toEqual([
      { amount: 30, label: 'Starter', sort_order: 10, enabled: true },
      { amount: 120, label: 'Popular', sort_order: 20, enabled: true },
    ])
  })

  it('ignores a desktop recharge amount that is not one of the configured tiers', async () => {
    routeState.query = {
      source: 'desktop',
      tab: 'recharge',
      amount: '200',
    }
    getCheckoutInfo.mockResolvedValue(checkoutInfoFixture({
      recharge_options: [
        { amount: 30, label: 'Starter', sort_order: 10, enabled: true },
        { amount: 120, label: 'Popular', sort_order: 20, enabled: true },
      ],
    }))

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(wrapper.findComponent(AmountInput).props('modelValue')).toBe(30)
  })

  // An empty tier list has two very different causes. Previously both fell back
  // to the hardcoded [20, 50, 100, 200, 500], so when the server had tiers that
  // all sat outside the visible method limits the page offered five amounts the
  // server was guaranteed to reject with INVALID_RECHARGE_OPTION.
  it('offers the built-in amounts only when the server accepts custom amounts', async () => {
    getCheckoutInfo.mockResolvedValue(checkoutInfoFixture({
      recharge_mode: 'custom',
      recharge_options: [],
    }))

    const custom = shallowMount(PaymentView, {
      global: {
        stubs: { AppLayout: { template: '<div><slot /></div>' }, Teleport: true, Transition: false },
      },
    })
    await flushPromises()
    await flushPromises()
    expect(custom.findComponent(AmountInput).props('amounts')).toEqual([20, 50, 100, 200, 500])
  })

  it('offers no amounts when the server enforces tiers but none are selectable', async () => {
    getCheckoutInfo.mockResolvedValue(checkoutInfoFixture({
      recharge_mode: 'fixed',
      // Every configured tier is filtered out by the visible method limits.
      recharge_options: [],
    }))

    const fixed = shallowMount(PaymentView, {
      global: {
        stubs: { AppLayout: { template: '<div><slot /></div>' }, Teleport: true, Transition: false },
      },
    })
    await flushPromises()
    await flushPromises()
    expect(fixed.findComponent(AmountInput).props('amounts')).toEqual([])
  })

  // The order rail collapses its method picker below `lg` — the bottom bar has
  // room for a total and an action, not a picker. The page must therefore
  // render its own selector, or phone users cannot choose how to pay at all.
  it('renders a method selector outside the rail for narrow viewports', async () => {
    getCheckoutInfo.mockResolvedValue(checkoutInfoFixture({
      recharge_options: [{ amount: 30, label: 'Starter', sort_order: 10, enabled: true }],
    }))

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: { AppLayout: { template: '<div><slot /></div>' }, Teleport: true, Transition: false },
      },
    })
    await flushPromises()
    await flushPromises()

    const rail = wrapper.findComponent(PaymentOrderRail)
    expect(rail.props('methodsCollapsedOnMobile')).toBe(true)

    const selectors = wrapper.findAllComponents(PaymentMethodSelector)
    expect(selectors.length).toBeGreaterThan(0)
    const mobile = selectors[selectors.length - 1]
    expect(mobile.props('methods')).toEqual(rail.props('methods'))
    expect(mobile.element.closest('.lg\\:hidden')).not.toBeNull()
  })

  // Older servers do not send recharge_mode; keep the previous inference there.
  it('falls back to inferring the mode when the server does not report one', async () => {
    getCheckoutInfo.mockResolvedValue(checkoutInfoFixture({ recharge_options: [] }))

    const legacy = shallowMount(PaymentView, {
      global: {
        stubs: { AppLayout: { template: '<div><slot /></div>' }, Teleport: true, Transition: false },
      },
    })
    await flushPromises()
    await flushPromises()
    expect(legacy.findComponent(AmountInput).props('amounts')).toEqual([20, 50, 100, 200, 500])
  })
})

describe('PaymentView reset-card quick entry navigation', () => {
  beforeEach(() => {
    vi.useRealTimers()
    routeState.path = '/purchase'
    routeState.query = {
      tab: 'subscription',
      group: '3',
      purchase: 'reset_card',
      subscription_id: '22',
    }
    routerReplace.mockReset().mockResolvedValue(undefined)
    routerPush.mockReset().mockResolvedValue(undefined)
    createOrder.mockReset()
    fetchActiveSubscriptions.mockReset().mockImplementation(async () => {
      subscriptionStoreState.setActiveSubscriptions([{
        id: 22,
        group_id: 3,
        status: 'active',
        expires_at: '2099-01-01T00:00:00Z',
        group: { name: 'Plus', platform: 'openai' },
      }])
    })
    showError.mockReset()
    showInfo.mockReset()
    showWarning.mockReset()
    window.localStorage.clear()
  })

  it('targets the fetched subscription without opening the renewal picker or creating an order', async () => {
    const basePlan = checkoutInfoWithPlansFixture().data.plans[0]
    getCheckoutInfo.mockResolvedValue(checkoutInfoFixture({
      plans: [
        { ...basePlan, id: 7, name: 'Plus', period_label: 'month', currency: 'CNY' },
        { ...basePlan, id: 8, name: '5X Pro', period_label: 'quarter', currency: 'CNY', validity_unit: 'month', validity_days: 3 },
      ],
    }))

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          Teleport: { template: '<div><slot /></div>' },
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(wrapper.findComponent(ResetCardShop).props('targetSubscriptionId')).toBe(22)
    expect(wrapper.findAll('button').some((button) => button.text() === 'payment.resetShop.quickEntry')).toBe(true)
    expect(wrapper.find('.fixed.inset-0.z-50').exists()).toBe(false)
    expect(createOrder).not.toHaveBeenCalled()
    expect(fetchActiveSubscriptions).toHaveBeenCalledTimes(1)
  })
})

describe('PaymentView payment recovery', () => {
  beforeEach(() => {
    vi.useRealTimers()
    routeState.path = '/purchase'
    routeState.query = {}
    routerReplace.mockReset().mockResolvedValue(undefined)
    routerPush.mockReset().mockResolvedValue(undefined)
    routerResolve.mockClear()
    createOrder.mockReset()
    refreshUser.mockReset()
    fetchActiveSubscriptions.mockReset().mockResolvedValue(undefined)
    showError.mockReset()
    showInfo.mockReset()
    showWarning.mockReset()
    bridgeInvoke.mockReset()
    window.localStorage.clear()
    ;(window as Window & { WeixinJSBridge?: { invoke: typeof bridgeInvoke } }).WeixinJSBridge = undefined
  })

  it('restores a custom EasyPay method as the selected payment method', async () => {
    getCheckoutInfo.mockResolvedValue(checkoutInfoFixture({
      methods: {
        wxpay: checkoutInfoFixture().data.methods.wxpay,
        ldc: {
          daily_limit: 0,
          daily_used: 0,
          daily_remaining: 0,
          single_min: 0,
          single_max: 0,
          fee_rate: 0,
          available: true,
          display_name: 'LDC Pay',
        },
      },
    }))
    window.localStorage.setItem(PAYMENT_RECOVERY_STORAGE_KEY, JSON.stringify({
      orderId: 888,
      amount: 66,
      qrCode: 'ldc-qr',
      expiresAt: '2099-01-01T00:10:00.000Z',
      paymentType: 'ldc',
      payUrl: 'https://pay.example.com/ldc',
      outTradeNo: 'sub2_ldc_888',
      clientSecret: '',
      intentId: '',
      currency: '',
      countryCode: '',
      paymentEnv: '',
      payAmount: 66,
      orderType: 'balance',
      paymentMode: 'popup',
      resumeToken: '',
      createdAt: Date.now(),
    }))

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          AppLayout: {
            template: '<div><slot /></div>',
          },
          PaymentStatusPanel: {
            template: '<button data-test="payment-done" @click="$emit(\'done\')" />',
          },
          BaseDialog: {
            template: '<div><slot /></div>',
          },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()
    await wrapper.find('[data-test="payment-done"]').trigger('click')
    await flushPromises()

    expect(wrapper.findComponent(PaymentOrderRail).props('selectedMethod')).toBe('ldc')
  })

  it('hides the checkout dialog without unmounting its resumable payment panel', async () => {
    getCheckoutInfo.mockResolvedValue(checkoutInfoFixture())
    window.localStorage.setItem(PAYMENT_RECOVERY_STORAGE_KEY, JSON.stringify({
      orderId: 321,
      amount: 66,
      qrCode: 'provider-qr',
      expiresAt: '2099-01-01T00:10:00.000Z',
      paymentType: 'wxpay',
      payUrl: '',
      outTradeNo: 'sub2_resume_321',
      clientSecret: '',
      intentId: '',
      currency: '',
      countryCode: '',
      paymentEnv: '',
      payAmount: 66,
      orderType: 'balance',
      paymentMode: 'native',
      resumeToken: 'resume-321',
      createdAt: Date.now(),
    }))

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          BaseDialog: {
            template: '<div><button data-test="hide-dialog" @click="$emit(\'close\')" /><slot /></div>',
          },
          PaymentStatusPanel: { template: '<div data-test="payment-panel" />' },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(wrapper.find('[data-test="payment-panel"]').exists()).toBe(true)
    await wrapper.get('[data-test="hide-dialog"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="resume-payment"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="payment-panel"]').exists()).toBe(true)
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toContain('sub2_resume_321')
  })

  it('creates reset-card checkout with its subscription id and idempotency header', async () => {
    routeState.query = { tab: 'subscription' }
    getCheckoutInfo.mockResolvedValue(checkoutInfoWithPlansFixture())
    createOrder.mockResolvedValue({
      order_id: 901,
      amount: 40,
      pay_amount: 40,
      fee_rate: 0,
      expires_at: '2099-01-01T00:10:00.000Z',
      payment_type: 'wxpay',
      qr_code: 'weixin://wxpay/bizpayurl?pr=reset-card',
      out_trade_no: 'sub2_reset_901',
    })

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          BaseDialog: { template: '<div><slot /></div>' },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    wrapper.findComponent(PaymentDiscountCodeInput).vm.$emit('update:modelValue', 'SUBONLY2026')
    await flushPromises()
    getCouponQuote.mockClear()
    const resetCheckout = {
      subscription: { id: 91 },
      quote: {
        subscription_id: 91,
        group_id: 3,
        plan_id: 7,
        monthly_price: 120,
        price: 40,
        expires_at: '2099-01-01T00:00:00Z',
        reset_card_tier_revision: 'v1:3:gpt:2:123',
      },
    }
    wrapper.findComponent(ResetCardShop).vm.$emit('checkout', resetCheckout)
    wrapper.findComponent(ResetCardShop).vm.$emit('checkout', resetCheckout)
    await flushPromises()

    expect(createOrder).toHaveBeenCalledTimes(1)
    expect(getCouponQuote).not.toHaveBeenCalled()
    expect(createOrder.mock.calls[0]?.[0]).not.toHaveProperty('coupon_code')
    expect(createOrder.mock.calls[0]?.[0]).not.toHaveProperty('coupon_revision')
    expect(createOrder).toHaveBeenCalledWith(expect.objectContaining({
      amount: 40,
      order_type: 'reset_card',
      plan_id: 7,
      subscription_id: 91,
      reset_card_tier_revision: 'v1:3:gpt:2:123',
    }), {
      headers: {
        'Idempotency-Key': expect.stringMatching(/^reset-card-payment-/),
      },
    })
  })

  it('reuses the persisted reset-card checkout key after a create-order response is lost', async () => {
    routeState.query = { tab: 'subscription' }
    getCheckoutInfo.mockResolvedValue(checkoutInfoWithPlansFixture())
    createOrder
      .mockRejectedValueOnce(new Error('response lost'))
      .mockResolvedValueOnce({
        order_id: 902,
        amount: 40,
        pay_amount: 40,
        fee_rate: 0,
        expires_at: '2099-01-01T00:10:00.000Z',
        payment_type: 'wxpay',
        qr_code: 'weixin://wxpay/bizpayurl?pr=reset-card-retry',
        out_trade_no: 'sub2_reset_902',
      })

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          BaseDialog: { template: '<div><slot /></div>' },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    const checkout = {
      subscription: { id: 91 },
      quote: {
        subscription_id: 91,
        group_id: 3,
        plan_id: 7,
        monthly_price: 120,
        price: 40,
        expires_at: '2099-01-01T00:00:00Z',
      },
    }
    wrapper.findComponent(ResetCardShop).vm.$emit('checkout', checkout)
    await flushPromises()

    const firstKey = createOrder.mock.calls[0]?.[1]?.headers?.['Idempotency-Key']
    expect(firstKey).toMatch(/^reset-card-payment-/)
    expect(window.localStorage.getItem(RESET_CARD_CHECKOUT_ATTEMPT_STORAGE_KEY)).toContain(firstKey)

    wrapper.findComponent(ResetCardShop).vm.$emit('checkout', checkout)
    await flushPromises()

    expect(createOrder).toHaveBeenCalledTimes(2)
    expect(createOrder.mock.calls[1]?.[1]?.headers?.['Idempotency-Key']).toBe(firstKey)
    expect(window.localStorage.getItem(RESET_CARD_CHECKOUT_ATTEMPT_STORAGE_KEY)).toContain('"orderId":902')
  })

  it('uses the first eligible wallet method when the global selector is on Stripe', async () => {
    routeState.query = { tab: 'subscription' }
    const method = (singleMax: number): MethodLimit => ({
      daily_limit: 0,
      daily_used: 0,
      daily_remaining: 0,
      single_min: 0,
      single_max: singleMax,
      fee_rate: 0,
      available: true,
      currency: 'CNY',
    })
    getCheckoutInfo.mockResolvedValue(checkoutInfoWithPlansFixture({
      checkout: {
        recharge_fee_rate: 10,
        methods: {
          stripe: method(1000),
          alipay: method(43),
          wxpay: method(44),
        },
      },
    }))
    createOrder.mockResolvedValue({
      order_id: 903,
      amount: 40,
      pay_amount: 44,
      fee_rate: 10,
      expires_at: '2099-01-01T00:10:00.000Z',
      payment_type: 'wxpay',
      qr_code: 'weixin://wxpay/bizpayurl?pr=reset-card-wallet',
      out_trade_no: 'sub2_reset_903',
    })

    const wrapper = shallowMount(PaymentView, {
      global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, BaseDialog: { template: '<div><slot /></div>' }, Teleport: true, Transition: false } },
    })
    await flushPromises()
    await flushPromises()
    wrapper.findComponent(PaymentOrderRail).vm.$emit('select-method', 'stripe')
    await flushPromises()

    wrapper.findComponent(ResetCardShop).vm.$emit('checkout', {
      subscription: { id: 91 },
      quote: { subscription_id: 91, group_id: 3, plan_id: 7, monthly_price: 120, price: 40, expires_at: '2099-01-01T00:00:00Z' },
    })
    await flushPromises()

    expect(createOrder).toHaveBeenCalledWith(expect.objectContaining({ payment_type: 'wxpay', order_type: 'reset_card' }), expect.anything())
    expect(wrapper.findComponent(PaymentOrderRail).props('selectedMethod')).toBe('wxpay')
  })

  it('does not create a reset-card order when no eligible wallet method can charge its quoted amount', async () => {
    routeState.query = { tab: 'subscription' }
    const method = (singleMax: number): MethodLimit => ({
      daily_limit: 0,
      daily_used: 0,
      daily_remaining: 0,
      single_min: 0,
      single_max: singleMax,
      fee_rate: 0,
      available: true,
      currency: 'CNY',
    })
    getCheckoutInfo.mockResolvedValue(checkoutInfoWithPlansFixture({
      checkout: {
        recharge_fee_rate: 10,
        methods: {
          stripe: method(1000),
          alipay: method(43),
          wxpay: method(43),
        },
      },
    }))

    const wrapper = shallowMount(PaymentView, {
      global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, Teleport: true, Transition: false } },
    })
    await flushPromises()
    await flushPromises()
    wrapper.findComponent(PaymentOrderRail).vm.$emit('select-method', 'stripe')

    wrapper.findComponent(ResetCardShop).vm.$emit('checkout', {
      subscription: { id: 91 },
      quote: { subscription_id: 91, group_id: 3, plan_id: 7, monthly_price: 120, price: 40, expires_at: '2099-01-01T00:00:00Z' },
    })
    await flushPromises()

    expect(createOrder).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('payment.resetShop.paymentUnavailable')
  })

  it('keeps a terminal reset-card replay in the local status shell instead of reporting an unhandled launch', async () => {
    routeState.query = { tab: 'subscription' }
    getCheckoutInfo.mockResolvedValue(checkoutInfoWithPlansFixture())
    createOrder.mockResolvedValue({
      order_id: 904,
      status: 'COMPLETED',
      amount: 40,
      pay_amount: 40,
      fee_rate: 0,
      expires_at: '2099-01-01T00:10:00.000Z',
      payment_type: 'wxpay',
      out_trade_no: 'sub2_reset_904',
    })

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          BaseDialog: { template: '<div><slot /></div>' },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()
    wrapper.findComponent(ResetCardShop).vm.$emit('checkout', {
      subscription: { id: 91 },
      quote: { subscription_id: 91, group_id: 3, plan_id: 7, monthly_price: 120, price: 40, expires_at: '2099-01-01T00:00:00Z' },
    })
    await flushPromises()

    const panel = wrapper.findComponent(PaymentStatusPanel)
    expect(showError).not.toHaveBeenCalled()
    expect(panel.exists()).toBe(true)
    expect(panel.props('orderId')).toBe(904)
    expect(panel.props('qrCode')).toBe('')
    expect(createOrder).toHaveBeenCalledTimes(1)
  })

  it('keeps the raw reset-card idempotency key out of the OAuth redirect URL', async () => {
    routeState.query = { tab: 'subscription' }
    getCheckoutInfo.mockResolvedValue(checkoutInfoWithPlansFixture())
    createOrder.mockResolvedValue({ ...oauthOrderFixture(), order_id: 905, amount: 40, pay_amount: 40 })
    const originalLocation = window.location
    const locationState = { href: 'http://localhost/purchase', origin: 'http://localhost' }
    Object.defineProperty(window, 'location', { configurable: true, value: locationState })

    const wrapper = shallowMount(PaymentView, {
      global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, Teleport: true, Transition: false } },
    })
    await flushPromises()
    await flushPromises()
    wrapper.findComponent(ResetCardShop).vm.$emit('checkout', {
      subscription: { id: 91 },
      quote: { subscription_id: 91, group_id: 3, plan_id: 7, monthly_price: 120, price: 40, expires_at: '2099-01-01T00:00:00Z' },
    })
    await flushPromises()

    const localKey = createOrder.mock.calls[0]?.[1]?.headers?.['Idempotency-Key']
    const redirect = new URL(locationState.href, 'http://localhost').searchParams.get('redirect') || ''
    expect(localKey).toMatch(/^reset-card-payment-/)
    expect(redirect).not.toContain('payment_idempotency_key')
    expect(redirect).not.toContain(localKey)

    Object.defineProperty(window, 'location', { configurable: true, value: originalLocation })
  })

  it('does not reserve a desktop popup for a native WeChat QR checkout', async () => {
    routeState.query = { tab: 'subscription' }
    isMobileDevice.mockReturnValue(false)
    getCheckoutInfo.mockResolvedValue(checkoutInfoWithPlansFixture())
    createOrder.mockResolvedValue({
      order_id: 906,
      amount: 40,
      pay_amount: 40,
      fee_rate: 0,
      expires_at: '2099-01-01T00:10:00.000Z',
      payment_type: 'wxpay',
      qr_code: 'weixin://wxpay/bizpayurl?pr=reset-card-qr',
      out_trade_no: 'sub2_reset_906',
    })
    const openSpy = vi.spyOn(window, 'open').mockReturnValue(null)

    const wrapper = shallowMount(PaymentView, {
      global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, Teleport: true, Transition: false } },
    })
    await flushPromises()
    await flushPromises()
    wrapper.findComponent(ResetCardShop).vm.$emit('checkout', {
      subscription: { id: 91 },
      quote: { subscription_id: 91, group_id: 3, plan_id: 7, monthly_price: 120, price: 40, expires_at: '2099-01-01T00:00:00Z' },
    })
    await flushPromises()

    expect(openSpy).not.toHaveBeenCalled()
    openSpy.mockRestore()
  })

  it('navigates a synchronously reserved desktop popup for hosted Alipay', async () => {
    routeState.query = { tab: 'subscription' }
    isMobileDevice.mockReturnValue(false)
    const checkout = checkoutInfoWithPlansFixture()
    checkout.data.methods = {
      alipay: { ...checkout.data.methods.wxpay, currency: 'CNY' },
    }
    getCheckoutInfo.mockResolvedValue(checkout)
    createOrder.mockResolvedValue({
      order_id: 907,
      amount: 40,
      pay_amount: 40,
      fee_rate: 0,
      expires_at: '2099-01-01T00:10:00.000Z',
      payment_type: 'alipay',
      pay_url: 'https://pay.example.com/reset-card/907',
      payment_mode: 'popup',
      out_trade_no: 'sub2_reset_907',
    })
    const popup = { closed: false, close: vi.fn(), location: { href: '' } } as unknown as Window
    const openSpy = vi.spyOn(window, 'open').mockReturnValue(popup)

    const wrapper = shallowMount(PaymentView, {
      global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, Teleport: true, Transition: false } },
    })
    await flushPromises()
    await flushPromises()
    wrapper.findComponent(ResetCardShop).vm.$emit('checkout', {
      subscription: { id: 91 },
      quote: { subscription_id: 91, group_id: 3, plan_id: 7, monthly_price: 120, price: 40, expires_at: '2099-01-01T00:00:00Z' },
    })
    await flushPromises()

    expect(openSpy).toHaveBeenCalledOnce()
    expect(openSpy).toHaveBeenCalledWith('', 'paymentPopup', expect.any(String))
    expect(popup.location.href).toBe('https://pay.example.com/reset-card/907')
    expect(popup.close).not.toHaveBeenCalled()
    openSpy.mockRestore()
  })

  it('falls back to a full-page hosted redirect when the reserved desktop popup is blocked', async () => {
    routeState.query = { tab: 'subscription' }
    isMobileDevice.mockReturnValue(false)
    const checkout = checkoutInfoWithPlansFixture()
    checkout.data.methods = {
      alipay: { ...checkout.data.methods.wxpay, currency: 'CNY' },
    }
    getCheckoutInfo.mockResolvedValue(checkout)
    createOrder.mockResolvedValue({
      order_id: 908,
      amount: 40,
      pay_amount: 40,
      fee_rate: 0,
      expires_at: '2099-01-01T00:10:00.000Z',
      payment_type: 'alipay',
      pay_url: 'https://pay.example.com/reset-card/908',
      payment_mode: 'popup',
      out_trade_no: 'sub2_reset_908',
    })
    const originalLocation = window.location
    const locationState = { href: 'http://localhost/purchase', origin: 'http://localhost' }
    Object.defineProperty(window, 'location', { configurable: true, value: locationState })
    const openSpy = vi.spyOn(window, 'open').mockReturnValue(null)

    const wrapper = shallowMount(PaymentView, {
      global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, Teleport: true, Transition: false } },
    })
    await flushPromises()
    await flushPromises()
    wrapper.findComponent(ResetCardShop).vm.$emit('checkout', {
      subscription: { id: 91 },
      quote: { subscription_id: 91, group_id: 3, plan_id: 7, monthly_price: 120, price: 40, expires_at: '2099-01-01T00:00:00Z' },
    })
    await flushPromises()

    expect(openSpy).toHaveBeenNthCalledWith(1, '', 'paymentPopup', expect.any(String))
    expect(openSpy).toHaveBeenNthCalledWith(2, 'https://pay.example.com/reset-card/908', 'paymentPopup', expect.any(String))
    expect(locationState.href).toBe('https://pay.example.com/reset-card/908')

    openSpy.mockRestore()
    Object.defineProperty(window, 'location', { configurable: true, value: originalLocation })
  })
})

describe('PaymentView WeChat JSAPI flow', () => {
  beforeEach(() => {
    vi.useRealTimers()
    routeState.path = '/purchase'
    routeState.query = {
      wechat_resume: '1',
      wechat_resume_token: 'resume-token-123',
    }
    routerReplace.mockReset().mockResolvedValue(undefined)
    routerPush.mockReset().mockResolvedValue(undefined)
    routerResolve.mockClear()
    createOrder.mockReset()
    refreshUser.mockReset()
    fetchActiveSubscriptions.mockReset().mockResolvedValue(undefined)
    showError.mockReset()
    showInfo.mockReset()
    showWarning.mockReset()
    getCheckoutInfo.mockReset().mockResolvedValue(checkoutInfoFixture())
    bridgeInvoke.mockReset()
    window.localStorage.clear()
    ;(window as Window & { WeixinJSBridge?: { invoke: typeof bridgeInvoke } }).WeixinJSBridge = {
      invoke: bridgeInvoke,
    }
  })

  afterEach(() => {
    appStoreState.setPublicSettings(undefined)
  })

  it('keeps the recoverable order open for server confirmation after JSAPI reports success', async () => {
    createOrder.mockResolvedValue(jsapiOrderFixture('resume-token-123'))
    bridgeInvoke.mockImplementation((_action, _payload, callback) => {
      callback({ err_msg: 'get_brand_wcpay_request:ok' })
    })

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(routerReplace).toHaveBeenCalledWith({ path: '/purchase', query: {} })
    expect(routerPush).not.toHaveBeenCalled()
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toContain('resume-token-123')
    wrapper.unmount()
  })

  it('preserves the resumable order when JSAPI reports cancellation', async () => {
    createOrder.mockResolvedValue(jsapiOrderFixture('resume-token-cancel'))
    bridgeInvoke.mockImplementation((_action, _payload, callback) => {
      callback({ err_msg: 'get_brand_wcpay_request:cancel' })
    })

    shallowMount(PaymentView, {
      global: {
        stubs: {
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(showInfo).toHaveBeenCalledWith('payment.qr.cancelled')
    expect(routerPush).not.toHaveBeenCalled()
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toContain('resume-token-cancel')
  })

  it('keeps recovery state when JSAPI never becomes available', async () => {
    vi.useFakeTimers()
    createOrder.mockResolvedValue(jsapiOrderFixture('resume-token-missing-bridge'))
    ;(window as Window & { WeixinJSBridge?: { invoke: typeof bridgeInvoke } }).WeixinJSBridge = undefined

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          Teleport: true,
          Transition: false,
        },
      },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(4000)
    await flushPromises()
    await flushPromises()

    expect(showError).toHaveBeenCalledWith(
      'payment.errors.wechatJsapiUnavailable payment.errors.wechatOpenInWeChatHint',
    )
    expect(routerPush).not.toHaveBeenCalled()
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toContain('resume-token-missing-bridge')
    expect(wrapper.html()).not.toContain('payment-status-panel-stub')
  })

  it('does not silently clear a different recovery snapshot before handling WeChat resume params', async () => {
    createOrder.mockRejectedValueOnce(new Error('resume failed'))
    window.localStorage.setItem(PAYMENT_RECOVERY_STORAGE_KEY, JSON.stringify({
      orderId: 999,
      amount: 66,
      qrCode: 'stale-qr',
      expiresAt: '2099-01-01T00:10:00.000Z',
      paymentType: 'alipay',
      payUrl: 'https://pay.example.com/stale',
      outTradeNo: 'stale-out-trade-no',
      clientSecret: '',
      intentId: '',
      currency: '',
      countryCode: '',
      paymentEnv: '',
      payAmount: 66,
      orderType: 'balance',
      paymentMode: 'popup',
      resumeToken: '',
      createdAt: Date.UTC(2099, 0, 1, 0, 0, 0),
    }))

    shallowMount(PaymentView, {
      global: {
        stubs: {
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(createOrder).toHaveBeenCalledWith(expect.objectContaining({
      wechat_resume_token: 'resume-token-123',
    }), undefined)
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toContain('stale-out-trade-no')
  })

  it('keeps subscription resume context for token-only WeChat callbacks', async () => {
    routeState.query = {
      wechat_resume: '1',
      wechat_resume_token: 'resume-subscription-7',
      payment_type: 'wxpay_direct',
      order_type: 'subscription',
      plan_id: '7',
    }
    getCheckoutInfo.mockResolvedValue(checkoutInfoWithPlansFixture())
    createOrder.mockResolvedValue(oauthOrderFixture())

    const originalLocation = window.location
    const locationState = {
      href: 'http://localhost/purchase',
      origin: 'http://localhost',
    }
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: locationState,
    })

    shallowMount(PaymentView, {
      global: {
        stubs: {
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(routerReplace).toHaveBeenCalledWith({ path: '/purchase', query: {} })
    expect(createOrder).toHaveBeenCalledWith(expect.objectContaining({
      payment_type: 'wxpay',
      order_type: 'subscription',
      plan_id: 7,
      wechat_resume_token: 'resume-subscription-7',
    }), undefined)
    expect(locationState.href).toContain('/api/v1/auth/oauth/wechat/payment/start?')
    expect(new URL(locationState.href, 'http://localhost').searchParams.get('redirect')).toBe(
      '/purchase?from=wechat&payment_type=wxpay&order_type=subscription&plan_id=7',
    )

    Object.defineProperty(window, 'location', {
      configurable: true,
      value: originalLocation,
    })
  })

  it('clears a subscription WeChat resume without creating an order when subscriptions are disabled', async () => {
    appStoreState.setPublicSettings({ subscription_enabled: false })
    routeState.query = {
      wechat_resume: '1',
      wechat_resume_token: 'resume-subscription-disabled',
      payment_type: 'wxpay_direct',
      order_type: 'subscription',
      plan_id: '7',
    }
    getCheckoutInfo.mockResolvedValue(checkoutInfoWithPlansFixture())

    shallowMount(PaymentView, {
      global: {
        stubs: {
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(routerReplace).toHaveBeenCalledWith({ path: '/purchase', query: {} })
    expect(createOrder).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('payment.errors.PLAN_NOT_AVAILABLE')
  })

  it('continues a balance WeChat resume when subscriptions are disabled', async () => {
    appStoreState.setPublicSettings({ subscription_enabled: false })
    routeState.query = {
      wechat_resume: '1',
      wechat_resume_token: 'resume-balance-subscription-disabled',
      payment_type: 'wxpay_direct',
      order_type: 'balance',
    }
    createOrder.mockResolvedValue(jsapiOrderFixture('resume-balance-subscription-disabled'))
    bridgeInvoke.mockImplementation((_action, _payload, callback) => {
      callback({ err_msg: 'get_brand_wcpay_request:ok' })
    })

    shallowMount(PaymentView, {
      global: {
        stubs: {
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(routerReplace).toHaveBeenCalledWith({ path: '/purchase', query: {} })
    expect(createOrder).toHaveBeenCalledWith(expect.objectContaining({
      payment_type: 'wxpay',
      order_type: 'balance',
      wechat_resume_token: 'resume-balance-subscription-disabled',
    }), undefined)
    expect(showError).not.toHaveBeenCalled()
  })

  it('binds a reset-card OAuth resume order to the matching browser attempt', async () => {
    const idempotencyKey = 'reset-card-payment-oauth-attempt'
    const resumeToken = await resetCardWechatResumeToken(idempotencyKey)
    routeState.query = {
      wechat_resume: '1',
      wechat_resume_token: resumeToken,
      payment_type: 'wxpay_direct',
      order_type: 'reset_card',
      plan_id: '7',
      subscription_id: '91',
      reset_card_tier_revision: 'v1:3:gpt:2:123',
    }
    window.localStorage.setItem(RESET_CARD_CHECKOUT_ATTEMPT_STORAGE_KEY, JSON.stringify({
      fingerprint: 'reset-card-quote-fingerprint',
      idempotencyKey,
    }))
    getCheckoutInfo.mockResolvedValue(checkoutInfoWithPlansFixture())
    createOrder.mockResolvedValue({
      order_id: 909,
      amount: 40,
      pay_amount: 40,
      fee_rate: 0,
      expires_at: '2099-01-01T00:10:00.000Z',
      payment_type: 'wxpay',
      qr_code: 'weixin://wxpay/bizpayurl?pr=reset-card-oauth-resume',
      out_trade_no: 'sub2_reset_909',
    })

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          Teleport: true,
          Transition: false,
        },
      },
    })
    await vi.waitFor(() => expect(routerReplace).toHaveBeenCalledWith({ path: '/purchase', query: {} }))
    await vi.waitFor(() => expect(createOrder).toHaveBeenCalledTimes(1))

    expect(createOrder).toHaveBeenCalledWith(expect.objectContaining({
      payment_type: 'wxpay',
      order_type: 'reset_card',
      plan_id: 7,
      subscription_id: 91,
      reset_card_tier_revision: 'v1:3:gpt:2:123',
      wechat_resume_token: resumeToken,
    }), {
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
    })
    expect(window.localStorage.getItem(RESET_CARD_CHECKOUT_ATTEMPT_STORAGE_KEY))
      .toContain('"orderId":909')
    wrapper.unmount()
  })

  it('reuses the matched reset-card attempt key when OAuth-resumed JSAPI falls back to QR', async () => {
    const idempotencyKey = 'reset-card-payment-oauth-jsapi-retry'
    const resumeToken = await resetCardWechatResumeToken(idempotencyKey)
    routeState.query = {
      wechat_resume: '1',
      wechat_resume_token: resumeToken,
      payment_type: 'wxpay_direct',
      order_type: 'reset_card',
      plan_id: '7',
      subscription_id: '91',
      reset_card_tier_revision: 'v1:3:gpt:2:123',
    }
    window.localStorage.setItem(RESET_CARD_CHECKOUT_ATTEMPT_STORAGE_KEY, JSON.stringify({
      fingerprint: 'reset-card-quote-fingerprint',
      idempotencyKey,
    }))
    getCheckoutInfo.mockResolvedValue(checkoutInfoWithPlansFixture())
    createOrder
      .mockResolvedValueOnce({ ...jsapiOrderFixture(resumeToken), order_id: 910 })
      .mockResolvedValueOnce({
        order_id: 910,
        amount: 40,
        pay_amount: 40,
        fee_rate: 0,
        expires_at: '2099-01-01T00:10:00.000Z',
        payment_type: 'wxpay',
        qr_code: 'weixin://wxpay/bizpayurl?pr=reset-card-oauth-fallback',
        out_trade_no: 'sub2_reset_910',
      })
    bridgeInvoke.mockImplementation((_action, _payload, callback) => {
      callback({ err_msg: 'get_brand_wcpay_request:fail' })
    })

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          Teleport: true,
          Transition: false,
        },
      },
    })
    await vi.waitFor(() => expect(createOrder).toHaveBeenCalledTimes(2))

    const idempotencyHeaders = {
      headers: { 'Idempotency-Key': idempotencyKey },
    }
    expect(createOrder).toHaveBeenNthCalledWith(1, expect.objectContaining({
      payment_type: 'wxpay',
      order_type: 'reset_card',
      plan_id: 7,
      subscription_id: 91,
      reset_card_tier_revision: 'v1:3:gpt:2:123',
      wechat_resume_token: resumeToken,
    }), idempotencyHeaders)
    expect(createOrder).toHaveBeenNthCalledWith(2, expect.objectContaining({
      payment_type: 'wxpay',
      order_type: 'reset_card',
      plan_id: 7,
      subscription_id: 91,
      reset_card_tier_revision: 'v1:3:gpt:2:123',
      is_mobile: false,
      payment_source: 'hosted_redirect',
    }), idempotencyHeaders)
    expect(JSON.parse(window.localStorage.getItem(RESET_CARD_CHECKOUT_ATTEMPT_STORAGE_KEY) || '{}')).toMatchObject({
      idempotencyKey,
      orderId: 910,
    })
    wrapper.unmount()
  })

  it('relies on the signed token instead of forwarding a raw URL idempotency key during WeChat resume', async () => {
    routeState.query = {
      wechat_resume: '1',
      wechat_resume_token: 'resume-token-h5',
      payment_type: 'wxpay_direct',
      payment_idempotency_key: 'wechat-fallback-key',
    }
    createOrder
      .mockRejectedValueOnce({ reason: 'WECHAT_H5_NOT_AUTHORIZED' })
      .mockResolvedValueOnce({
        order_id: 778,
        amount: 88,
        pay_amount: 88,
        fee_rate: 0,
        expires_at: '2099-01-01T00:10:00.000Z',
        payment_type: 'wxpay',
        qr_code: 'weixin://wxpay/bizpayurl?pr=fallback-native',
        out_trade_no: 'sub2_qr_778',
      })

    shallowMount(PaymentView, {
      global: {
        stubs: {
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(createOrder).toHaveBeenNthCalledWith(1, expect.objectContaining({
      payment_type: 'wxpay',
      is_mobile: true,
      wechat_resume_token: 'resume-token-h5',
    }), undefined)
    expect(createOrder).toHaveBeenNthCalledWith(2, expect.objectContaining({
      payment_type: 'wxpay',
      is_mobile: false,
      payment_source: 'hosted_redirect',
    }), undefined)
    expect(routerReplace).toHaveBeenCalledWith({ path: '/purchase', query: {} })
    expect(showWarning).toHaveBeenCalledWith('payment.errors.mobilePaymentFallbackToQr')
    expect(showError).not.toHaveBeenCalled()
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toContain('weixin://wxpay/bizpayurl?pr=fallback-native')
  })
})

describe('PaymentView subscription feature flag', () => {
  afterEach(() => {
    appStoreState.setPublicSettings(undefined)
  })

  function tabLabels(wrapper: Awaited<ReturnType<typeof mountSubscriptionPlanList>>) {
    return wrapper
      .findAll('button')
      .map((button) => button.text())
      .filter((text) => text === 'payment.tabTopUp' || text === 'payment.tabSubscribe')
  }

  it('keeps the top-up / subscribe switcher when subscription_enabled is absent (opt-out default)', async () => {
    const wrapper = await mountSubscriptionPlanList(2)

    expect(tabLabels(wrapper)).toEqual(['payment.tabTopUp', 'payment.tabSubscribe'])
    expect(wrapper.findAllComponents(SubscriptionPlanCard)).toHaveLength(2)
    wrapper.unmount()
  })

  it('drops the subscribe tab, hides the switcher and ignores ?tab=subscription when subscriptions are disabled', async () => {
    appStoreState.setPublicSettings({ subscription_enabled: false })
    const wrapper = await mountSubscriptionPlanList(2)

    expect(tabLabels(wrapper)).toEqual([])
    expect(wrapper.findAllComponents(SubscriptionPlanCard)).toHaveLength(0)
    expect(wrapper.findComponent(AmountInput).exists()).toBe(true)
    wrapper.unmount()
  })

  it('shows an unavailable notice instead of a doomed top-up form when balance recharge is disabled too', async () => {
    appStoreState.setPublicSettings({ subscription_enabled: false })
    const wrapper = await mountSubscriptionConfirm({ checkout: { balance_disabled: true } })

    expect(tabLabels(wrapper)).toEqual([])
    expect(wrapper.findAllComponents(SubscriptionPlanCard)).toHaveLength(0)
    expect(wrapper.text()).not.toContain('payment.confirmSubscription')
    expect(wrapper.findComponent(AmountInput).exists()).toBe(false)
    expect(wrapper.text()).toContain('payment.billingUnavailable')
    wrapper.unmount()
  })

  it('falls back from the subscribe tab to top-up when the flag flips off after mount', async () => {
    const wrapper = await mountSubscriptionPlanList(2)
    expect(wrapper.findAllComponents(SubscriptionPlanCard)).toHaveLength(2)

    appStoreState.setPublicSettings({ subscription_enabled: false })
    await flushPromises()

    expect(tabLabels(wrapper)).toEqual([])
    expect(wrapper.findAllComponents(SubscriptionPlanCard)).toHaveLength(0)
    expect(wrapper.findComponent(AmountInput).exists()).toBe(true)
    wrapper.unmount()
  })

  it('enters the subscribe tab when a subscription-only site turns subscriptions back on', async () => {
    appStoreState.setPublicSettings({ subscription_enabled: false })
    const wrapper = await mountSubscriptionConfirm({ checkout: { balance_disabled: true } })
    expect(wrapper.text()).toContain('payment.billingUnavailable')

    appStoreState.setPublicSettings({ subscription_enabled: true })
    await flushPromises()

    expect(wrapper.text()).not.toContain('payment.billingUnavailable')
    expect(wrapper.findComponent(AmountInput).exists()).toBe(false)
    expect(wrapper.findAllComponents(SubscriptionPlanCard).length).toBeGreaterThan(0)
    wrapper.unmount()
  })
})
