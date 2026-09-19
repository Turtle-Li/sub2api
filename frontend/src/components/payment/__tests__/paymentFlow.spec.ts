import { describe, expect, it } from 'vitest'
import type { CreateOrderResult, MethodLimit } from '@/types/payment'
import {
  buildCreateOrderPayload,
  clearResetCardCheckoutAttempt,
  discardResetCardCheckoutAttemptForSelectionChange,
  clearPaymentRecoverySnapshot,
  createResetCardCheckoutFingerprint,
  decidePaymentLaunch,
  getOrCreateResetCardCheckoutAttempt,
  getVisibleMethods,
  matchResetCardCheckoutAttemptForResume,
  readPaymentRecoverySnapshot,
  recordResetCardCheckoutOrder,
  RESET_CARD_CHECKOUT_ATTEMPT_STORAGE_KEY,
  type PaymentRecoverySnapshot,
  validateAlipayCheckoutFrameUrl,
  writePaymentRecoverySnapshot,
} from '@/components/payment/paymentFlow'

function methodLimit(overrides: Partial<MethodLimit> = {}): MethodLimit {
  return {
    daily_limit: 0,
    daily_used: 0,
    daily_remaining: 0,
    single_min: 0,
    single_max: 0,
    fee_rate: 0,
    available: true,
    ...overrides,
  }
}

function createOrderResult(overrides: Partial<CreateOrderResult> = {}): CreateOrderResult {
  return {
    order_id: 101,
    amount: 88,
    pay_amount: 88,
    fee_rate: 0,
    expires_at: '2099-01-01T00:10:00.000Z',
    ...overrides,
  }
}

function alipayCheckoutFrameUrl(options: {
  host?: string
  pathname?: string
  qrPayMode?: string | number
  qrcodeWidth?: string | number
  method?: string
  signType?: string
  sign?: string
  fragment?: string
} = {}): string {
  const url = new URL(`https://${options.host || 'openapi.alipay.com'}${options.pathname || '/gateway.do'}`)
  url.searchParams.set('method', options.method || 'alipay.trade.page.pay')
  url.searchParams.set('biz_content', JSON.stringify({
    out_trade_no: 'sub2_101',
    qr_pay_mode: options.qrPayMode ?? '4',
    qrcode_width: options.qrcodeWidth ?? '224',
  }))
  url.searchParams.set('sign_type', options.signType || 'RSA2')
  url.searchParams.set('sign', options.sign ?? 'signed-payload')
  url.hash = options.fragment || ''
  return url.toString()
}

describe('getVisibleMethods', () => {
  it('normalizes provider aliases and keeps stripe as a top-level method', () => {
    const visible = getVisibleMethods({
      alipay_direct: methodLimit({ single_min: 5 }),
      wxpay: methodLimit({ single_max: 100 }),
      stripe: methodLimit({ fee_rate: 3 }),
      airwallex: methodLimit({ single_min: 10 }),
    })

    expect(visible).toEqual({
      alipay: methodLimit({ single_min: 5 }),
      wxpay: methodLimit({ single_max: 100 }),
      stripe: methodLimit({ fee_rate: 3 }),
      airwallex: methodLimit({ single_min: 10 }),
    })
  })

  it('prefers canonical visible methods over aliases when both exist', () => {
    const visible = getVisibleMethods({
      alipay: methodLimit({ single_min: 2 }),
      alipay_direct: methodLimit({ single_min: 9 }),
      wxpay_direct: methodLimit({ fee_rate: 1.2 }),
    })

    expect(visible.alipay.single_min).toBe(2)
    expect(visible.wxpay.fee_rate).toBe(1.2)
  })

  it('keeps custom EasyPay methods as visible methods', () => {
    const visible = getVisibleMethods({
      ldc: methodLimit({ single_min: 3 }),
      usdt_trc20: methodLimit({ fee_rate: 1 }),
    })

    expect(visible).toEqual({
      ldc: methodLimit({ single_min: 3 }),
      usdt_trc20: methodLimit({ fee_rate: 1 }),
    })
  })
})

describe('validateAlipayCheckoutFrameUrl', () => {
  it('accepts only the signed Alipay page-pay QR endpoint', () => {
    const valid = alipayCheckoutFrameUrl()
    expect(validateAlipayCheckoutFrameUrl(valid)).toBe(valid)
  })

  it.each([
    'http://openapi.alipay.com/gateway.do?method=alipay.trade.page.pay&biz_content=%7B%22qr_pay_mode%22%3A%224%22%2C%22qrcode_width%22%3A%22224%22%7D&sign_type=RSA2&sign=signed',
    'https://checkout.example.invalid/gateway.do?method=alipay.trade.page.pay&biz_content=%7B%22qr_pay_mode%22%3A%224%22%2C%22qrcode_width%22%3A%22224%22%7D&sign_type=RSA2&sign=signed',
    'https://openapi.alipay.com:443/gateway.do?method=alipay.trade.page.pay&biz_content=%7B%22qr_pay_mode%22%3A%224%22%2C%22qrcode_width%22%3A%22224%22%7D&sign_type=RSA2&sign=signed',
    'https://openapi.alipay.com@gateway.do/gateway.do?method=alipay.trade.page.pay&biz_content=%7B%22qr_pay_mode%22%3A%224%22%2C%22qrcode_width%22%3A%22224%22%7D&sign_type=RSA2&sign=signed',
    alipayCheckoutFrameUrl({ pathname: '/gateway.do/' }),
    alipayCheckoutFrameUrl({ fragment: 'return' }),
    alipayCheckoutFrameUrl({ method: 'alipay.trade.precreate' }),
    alipayCheckoutFrameUrl({ qrPayMode: '2' }),
    alipayCheckoutFrameUrl({ qrcodeWidth: 320 }),
    alipayCheckoutFrameUrl({ signType: 'RSA' }),
    alipayCheckoutFrameUrl({ sign: '' }),
    ` ${alipayCheckoutFrameUrl()}`,
  ])('rejects an untrusted frame URL: %s', (value) => {
    expect(validateAlipayCheckoutFrameUrl(value)).toBe('')
  })

  it('rejects duplicated signed parameters', () => {
    const url = new URL(alipayCheckoutFrameUrl())
    url.searchParams.append('method', 'alipay.trade.page.pay')
    expect(validateAlipayCheckoutFrameUrl(url.toString())).toBe('')
  })

  it('rejects a signed frame URL above the backend-aligned length limit', () => {
    const oversized = `${alipayCheckoutFrameUrl()}&padding=${'x'.repeat(16384)}`
    expect(validateAlipayCheckoutFrameUrl(oversized)).toBe('')
  })

  it('rejects a signed frame URL with whitespace in its signature', () => {
    expect(validateAlipayCheckoutFrameUrl(alipayCheckoutFrameUrl({ sign: ' signed-payload ' }))).toBe('')
  })
})

describe('decidePaymentLaunch', () => {
  it('does not route an Alipay response through Stripe merely because it contains a client secret', () => {
    const decision = decidePaymentLaunch(createOrderResult({
      client_secret: 'cs_test',
      resume_token: 'resume-1',
    }), {
      visibleMethod: 'alipay',
      orderType: 'balance',
      isMobile: false,
    })

    expect(decision.kind).toBe('unhandled')
    expect(decision.paymentState.paymentType).toBe('alipay')
    expect(decision.recovery.resumeToken).toBe('resume-1')
    expect(decision.recovery.outTradeNo).toBe('')
  })

  it('routes a Stripe button click to its dedicated checkout without a preselected sub-method', () => {
    const decision = decidePaymentLaunch(createOrderResult({
      client_secret: 'cs_test',
    }), {
      visibleMethod: 'stripe',
      orderType: 'balance',
      isMobile: false,
      stripePopupUrl: '/payment/stripe?order_id=101',
      stripeRouteUrl: '/payment/stripe?order_id=101',
    })

    expect(decision.kind).toBe('stripe_popup')
    expect(decision.stripeMethod).toBeUndefined()
  })

  it('does not route a WeChat response through Stripe merely because it contains a client secret', () => {
    const decision = decidePaymentLaunch(createOrderResult({
      client_secret: 'cs_test',
    }), {
      visibleMethod: 'wxpay',
      orderType: 'subscription',
      isMobile: true,
    })

    expect(decision.kind).toBe('unhandled')
    expect(decision.paymentState.orderType).toBe('subscription')
  })

  it('routes Airwallex client secrets through the hosted Airwallex page', () => {
    const decision = decidePaymentLaunch(createOrderResult({
      client_secret: 'awx_cs',
      intent_id: 'int_awx',
      currency: 'CNY',
      country_code: 'CN',
      payment_env: 'demo',
      out_trade_no: 'sub2_awx',
    }), {
      visibleMethod: 'airwallex',
      orderType: 'balance',
      isMobile: false,
      airwallexRouteUrl: '/payment/airwallex?order_id=101',
    })

    expect(decision.kind).toBe('airwallex_route')
    expect(decision.paymentState.payUrl).toBe('/payment/airwallex?order_id=101')
    expect(decision.paymentState.intentId).toBe('int_awx')
    expect(decision.paymentState.currency).toBe('CNY')
    expect(decision.paymentState.countryCode).toBe('CN')
    expect(decision.paymentState.paymentEnv).toBe('demo')
  })

  it('keeps hosted redirect metadata for recovery flows', () => {
    const decision = decidePaymentLaunch(createOrderResult({
      pay_url: 'https://pay.example.com/session/abc',
      payment_mode: 'popup',
      resume_token: 'resume-2',
      out_trade_no: 'sub2_abc',
    }), {
      visibleMethod: 'wxpay',
      orderType: 'balance',
      isMobile: false,
    })

    expect(decision.kind).toBe('redirect_waiting')
    expect(decision.paymentState.payUrl).toBe('https://pay.example.com/session/abc')
    expect(decision.recovery.paymentMode).toBe('popup')
    expect(decision.recovery.outTradeNo).toBe('sub2_abc')
    expect(decision.recovery.resumeToken).toBe('resume-2')
  })

  it('uses an actual QR payload on mobile when the provider supplies both QR and hosted URLs', () => {
    const decision = decidePaymentLaunch(createOrderResult({
      pay_url: 'https://pay.example.com/mobile/session',
      qr_code: 'https://pay.example.com/qr/session',
    }), {
      visibleMethod: 'alipay',
      orderType: 'balance',
      isMobile: true,
    })

    expect(decision.kind).toBe('qr_waiting')
    expect(decision.paymentState.payUrl).toBe('https://pay.example.com/mobile/session')
    expect(decision.paymentState.qrCode).toBe('https://pay.example.com/qr/session')
  })

  it('keeps QR flow on desktop when both pay_url and qr_code are present', () => {
    const decision = decidePaymentLaunch(createOrderResult({
      pay_url: 'https://pay.example.com/desktop/session',
      qr_code: 'https://pay.example.com/qr/session',
    }), {
      visibleMethod: 'wxpay',
      orderType: 'balance',
      isMobile: false,
    })

    expect(decision.kind).toBe('qr_waiting')
    expect(decision.paymentState.qrCode).toBe('https://pay.example.com/qr/session')
  })

  it('keeps a hosted URL as a redirect and never treats it as QR data', () => {
    const decision = decidePaymentLaunch(createOrderResult({
      pay_url: 'https://pay.example.com/hosted/session',
      payment_mode: 'redirect',
    }), {
      visibleMethod: 'alipay',
      orderType: 'balance',
      isMobile: false,
    })

    expect(decision.kind).toBe('redirect_waiting')
    expect(decision.paymentState.payUrl).toBe('https://pay.example.com/hosted/session')
    expect(decision.paymentState.qrCode).toBe('')
  })

  it('keeps a valid Alipay checkout frame separate from QR and suppresses redirect launch', () => {
    const checkoutFrameUrl = alipayCheckoutFrameUrl()
    const decision = decidePaymentLaunch(createOrderResult({
      payment_mode: 'redirect',
      pay_url: 'https://pay.example.com/hosted/session',
      checkout_frame_url: checkoutFrameUrl,
    }), {
      visibleMethod: 'alipay',
      orderType: 'balance',
      isMobile: false,
    })

    expect(decision.kind).toBe('qr_waiting')
    expect(decision.paymentState.checkoutFrameUrl).toBe(checkoutFrameUrl)
    expect(decision.recovery.checkoutFrameUrl).toBe(checkoutFrameUrl)
    expect(decision.paymentState.qrCode).toBe('')
    expect(decision.paymentState.payUrl).toBe('https://pay.example.com/hosted/session')
  })

  it('returns wechat oauth launch when backend requires in-app authorization', () => {
    const decision = decidePaymentLaunch(createOrderResult({
      result_type: 'oauth_required',
      payment_type: 'wxpay',
      oauth: {
        authorize_url: '/api/v1/auth/oauth/wechat/payment/start?payment_type=wxpay',
        appid: 'wx123',
        scope: 'snsapi_base',
        redirect_url: '/auth/wechat/payment/callback',
      },
    }), {
      visibleMethod: 'wxpay',
      orderType: 'balance',
      isMobile: true,
    })

    expect(decision.kind).toBe('wechat_oauth')
    expect(decision.oauth?.authorize_url).toContain('/api/v1/auth/oauth/wechat/payment/start')
    expect(decision.paymentState.paymentType).toBe('wxpay')
  })

  it('keeps the server-issued payment discount in the provider recovery state', () => {
    const discount = {
      code_id: 44,
      code: 'SAVE2026',
      original_amount: '100.00',
      discount_amount: '20.00',
      pay_amount: '80.00',
      currency: 'CNY',
    }
    const decision = decidePaymentLaunch(createOrderResult({
      pay_amount: 80,
      qr_code: 'weixin://wxpay/bizpayurl?pr=coupon',
      payment_discount: discount,
    }), {
      visibleMethod: 'wxpay',
      orderType: 'balance',
      isMobile: true,
    })

    expect(decision.paymentState.paymentDiscount).toEqual(discount)
    expect(decision.recovery.paymentDiscount).toEqual(discount)
  })

  it('returns wechat jsapi launch when backend has a jsapi payload ready', () => {
    const decision = decidePaymentLaunch(createOrderResult({
      result_type: 'jsapi_ready',
      payment_type: 'wxpay',
      jsapi: {
        appId: 'wx123',
        timeStamp: '1712345678',
        nonceStr: 'nonce-123',
        package: 'prepay_id=wx123',
        signType: 'RSA',
        paySign: 'signed-payload',
      },
    }), {
      visibleMethod: 'wxpay',
      orderType: 'subscription',
      isMobile: true,
    })

    expect(decision.kind).toBe('wechat_jsapi')
    expect(decision.jsapi?.appId).toBe('wx123')
    expect(decision.paymentState.orderType).toBe('subscription')
  })

  it('forces qr_waiting for mobile alipay when forceQRCode is enabled', () => {
    const decision = decidePaymentLaunch(createOrderResult({
      pay_url: 'https://pay.example.com/mobile/session',
      qr_code: 'https://pay.example.com/qr/session',
    }), {
      visibleMethod: 'alipay',
      orderType: 'balance',
      isMobile: true,
      forceQRCode: true,
    })

    expect(decision.kind).toBe('qr_waiting')
    expect(decision.paymentState.qrCode).toBe('https://pay.example.com/qr/session')
  })

  it('launches the Alipay app for a mobile precreate order', () => {
    const decision = decidePaymentLaunch(createOrderResult({
      qr_code: 'https://qr.alipay.com/dynamic-order-101',
      alipay_mobile_precreate_deep_link: true,
    }), {
      visibleMethod: 'alipay',
      orderType: 'balance',
      isMobile: true,
    })

    expect(decision.kind).toBe('alipay_deep_link')
    expect(decision.paymentState.qrCode).toBe('https://qr.alipay.com/dynamic-order-101')
    expect(decision.paymentState.alipayMobilePrecreateDeepLink).toBe(true)
  })

  it('keeps the desktop Alipay QR flow when a precreate marker is present', () => {
    const decision = decidePaymentLaunch(createOrderResult({
      qr_code: 'https://qr.alipay.com/dynamic-order-102',
      alipay_mobile_precreate_deep_link: true,
    }), {
      visibleMethod: 'alipay',
      orderType: 'balance',
      isMobile: false,
    })

    expect(decision.kind).toBe('qr_waiting')
  })

  it('keeps a real QR payload usable for non-Alipay methods when forceQRCode is enabled', () => {
    const decision = decidePaymentLaunch(createOrderResult({
      pay_url: 'https://pay.example.com/mobile/session',
      qr_code: 'https://pay.example.com/qr/session',
    }), {
      visibleMethod: 'wxpay',
      orderType: 'balance',
      isMobile: true,
      forceQRCode: true,
    })

    expect(decision.kind).toBe('qr_waiting')
  })

  it('keeps a terminal reset-card replay in a status-only shell when no launch material remains', () => {
    const decision = decidePaymentLaunch(createOrderResult({
      order_id: 902,
      status: 'COMPLETED',
      payment_type: 'wxpay',
      out_trade_no: 'sub2_reset_902',
      qr_code: '',
      pay_url: '',
    }), {
      visibleMethod: 'wxpay',
      orderType: 'reset_card',
      isMobile: true,
    })

    expect(decision.kind).toBe('status_waiting')
    expect(decision.paymentState.orderId).toBe(902)
    expect(decision.paymentState.qrCode).toBe('')
  })

  it('rejects a pending response that has no QR, hosted URL, or JSAPI payload', () => {
    const decision = decidePaymentLaunch(createOrderResult({
      status: 'PENDING',
      payment_type: 'alipay',
      out_trade_no: 'sub2_missing-launch-material',
      qr_code: '',
      pay_url: '',
    }), {
      visibleMethod: 'alipay',
      orderType: 'balance',
      isMobile: false,
    })

    expect(decision.kind).toBe('unhandled')
  })

  it('keeps an already-expired pending response in status checking instead of opening its stale QR', () => {
    const decision = decidePaymentLaunch(createOrderResult({
      status: 'PENDING',
      payment_type: 'alipay',
      out_trade_no: 'sub2_expired-before-launch',
      qr_code: 'https://qr.example.test/stale',
      expires_at: '2026-09-19T00:00:00.000Z',
    }), {
      visibleMethod: 'alipay',
      orderType: 'balance',
      isMobile: false,
      now: Date.parse('2026-09-19T00:01:00.000Z'),
    })

    expect(decision.kind).toBe('status_waiting')
  })
})

describe('buildCreateOrderPayload', () => {
  it('normalizes visible method aliases and attaches a canonical result URL', () => {
    expect(buildCreateOrderPayload({
      amount: 88,
      paymentType: 'alipay_direct',
      orderType: 'balance',
      origin: 'https://app.example.com/',
      isMobile: true,
      isWechatBrowser: false,
    })).toEqual({
      amount: 88,
      payment_type: 'alipay',
      order_type: 'balance',
      return_url: 'https://app.example.com/payment/result',
      is_mobile: true,
      payment_source: 'hosted_redirect',
    })
  })

  it('uses WeChat in-app resume source for visible WeChat payments in the WeChat browser', () => {
    expect(buildCreateOrderPayload({
      amount: 128,
      paymentType: 'wxpay',
      orderType: 'subscription',
      planId: 7,
      origin: 'https://app.example.com',
      isMobile: false,
      isWechatBrowser: true,
    })).toEqual({
      amount: 128,
      payment_type: 'wxpay',
      order_type: 'subscription',
      plan_id: 7,
      return_url: 'https://app.example.com/payment/result',
      is_mobile: false,
      payment_source: 'wechat_in_app_resume',
    })
  })

  it('carries an opaque reset-card tier revision into order creation', () => {
    expect(buildCreateOrderPayload({
      amount: 40,
      paymentType: 'wxpay',
      orderType: 'reset_card',
      planId: 7,
      subscriptionId: 91,
      resetCardTierRevision: 'v1:3:gpt:2:123',
      isMobile: false,
      isWechatBrowser: false,
    })).toMatchObject({
      order_type: 'reset_card',
      plan_id: 7,
      subscription_id: 91,
      reset_card_tier_revision: 'v1:3:gpt:2:123',
    })
  })

  it('carries reset-card quantity and purchase-time use intent only for reset-card orders', () => {
    expect(buildCreateOrderPayload({
      amount: 120,
      paymentType: 'alipay',
      orderType: 'reset_card',
      planId: 7,
      subscriptionId: 91,
      resetCardQuantity: 3,
      resetCardUseOnPurchase: true,
      isMobile: false,
      isWechatBrowser: false,
    })).toMatchObject({
      amount: 120,
      reset_card_quantity: 3,
      reset_card_use_on_purchase: true,
    })
  })

  it('passes is_mobile: false when forceQRCode is enabled for alipay', () => {
    expect(buildCreateOrderPayload({
      amount: 50,
      paymentType: 'alipay',
      orderType: 'balance',
      origin: 'https://app.example.com',
      isMobile: true,
      isWechatBrowser: false,
      forceQRCode: true,
    })).toMatchObject({
      is_mobile: false,
    })
  })

  it('keeps is_mobile true when mobile precreate takes priority over forceQRCode', () => {
    expect(buildCreateOrderPayload({
      amount: 50,
      paymentType: 'alipay',
      orderType: 'balance',
      origin: 'https://app.example.com',
      isMobile: true,
      isWechatBrowser: false,
      forceQRCode: true,
      mobilePrecreateDeepLink: true,
    })).toMatchObject({
      is_mobile: true,
    })
  })

  it('still passes is_mobile: true when forceQRCode is enabled for non-alipay methods', () => {
    expect(buildCreateOrderPayload({
      amount: 50,
      paymentType: 'wxpay',
      orderType: 'balance',
      origin: 'https://app.example.com',
      isMobile: true,
      isWechatBrowser: false,
      forceQRCode: true,
    })).toMatchObject({
      is_mobile: true,
    })
  })
})

describe('readPaymentRecoverySnapshot', () => {
  it('restores an unexpired snapshot when the resume token matches', () => {
    const snapshot: PaymentRecoverySnapshot = {
      orderId: 33,
      amount: 18,
      qrCode: '',
      expiresAt: '2099-01-01T00:10:00.000Z',
      paymentType: 'alipay',
      payUrl: 'https://pay.example.com/session/33',
      checkoutFrameUrl: alipayCheckoutFrameUrl(),
      outTradeNo: 'sub2_33',
      clientSecret: '',
      intentId: '',
      currency: '',
      countryCode: '',
      paymentEnv: '',
      payAmount: 18,
      orderType: 'balance',
      paymentMode: 'popup',
      resumeToken: 'resume-33',
      createdAt: Date.UTC(2099, 0, 1, 0, 0, 0),
    }

    const restored = readPaymentRecoverySnapshot(JSON.stringify(snapshot), {
      now: Date.UTC(2099, 0, 1, 0, 1, 0),
      resumeToken: 'resume-33',
    })

    expect(restored?.orderId).toBe(33)
    expect(restored?.checkoutFrameUrl).toBe(snapshot.checkoutFrameUrl)
  })

  it('drops an invalid persisted checkout frame without discarding the owned order recovery', () => {
    const restored = readPaymentRecoverySnapshot(JSON.stringify({
      orderId: 34,
      amount: 18,
      qrCode: '',
      expiresAt: '2099-01-01T00:10:00.000Z',
      paymentType: 'alipay',
      payUrl: 'https://pay.example.com/session/34',
      checkoutFrameUrl: 'https://checkout.example.invalid/gateway.do?method=alipay.trade.page.pay',
      outTradeNo: 'sub2_34',
      clientSecret: '',
      intentId: '',
      currency: '',
      countryCode: '',
      paymentEnv: '',
      payAmount: 18,
      orderType: 'balance',
      paymentMode: 'popup',
      resumeToken: 'resume-34',
      createdAt: Date.UTC(2099, 0, 1, 0, 0, 0),
    }), {
      now: Date.UTC(2099, 0, 1, 0, 1, 0),
      resumeToken: 'resume-34',
    })

    expect(restored?.orderId).toBe(34)
    expect(restored?.checkoutFrameUrl).toBe('')
  })

  it('retains a recent snapshot after its browser deadline so the server can settle it', () => {
    const expiredSnapshot: PaymentRecoverySnapshot = {
      orderId: 55,
      amount: 18,
      qrCode: '',
      expiresAt: '2024-01-01T00:10:00.000Z',
      paymentType: 'wxpay',
      payUrl: 'https://pay.example.com/session/55',
      outTradeNo: 'sub2_55',
      clientSecret: '',
      intentId: '',
      currency: '',
      countryCode: '',
      paymentEnv: '',
      payAmount: 18,
      orderType: 'balance',
      paymentMode: 'popup',
      resumeToken: 'resume-55',
      createdAt: Date.UTC(2024, 0, 1, 0, 0, 0),
    }

    expect(readPaymentRecoverySnapshot(JSON.stringify(expiredSnapshot), {
      now: Date.UTC(2024, 0, 1, 0, 20, 0),
      resumeToken: 'resume-55',
    })?.orderId).toBe(55)

    expect(readPaymentRecoverySnapshot(JSON.stringify(expiredSnapshot), {
      now: Date.UTC(2024, 0, 1, 3, 0, 0),
      resumeToken: 'resume-55',
    })).toBeNull()

    expect(readPaymentRecoverySnapshot(JSON.stringify({
      ...expiredSnapshot,
      outTradeNo: 'sub2_55',
      expiresAt: '2099-01-01T00:10:00.000Z',
    }), {
      now: Date.UTC(2099, 0, 1, 0, 1, 0),
      resumeToken: 'other-token',
    })).toBeNull()
  })

  it('keeps backward compatibility with snapshots written before outTradeNo existed', () => {
    const restored = readPaymentRecoverySnapshot(JSON.stringify({
      orderId: 44,
      amount: 18,
      qrCode: '',
      expiresAt: '2099-01-01T00:10:00.000Z',
      paymentType: 'alipay',
      payUrl: 'https://pay.example.com/session/44',
      clientSecret: '',
      payAmount: 18,
      orderType: 'balance',
      paymentMode: 'popup',
      resumeToken: 'resume-44',
      createdAt: Date.UTC(2099, 0, 1, 0, 0, 0),
    }), {
      now: Date.UTC(2099, 0, 1, 0, 1, 0),
      resumeToken: 'resume-44',
    })

    expect(restored?.orderId).toBe(44)
    expect(restored?.outTradeNo).toBe('')
  })

  it('keeps backward compatibility with snapshots written before Airwallex fields existed', () => {
    const restored = readPaymentRecoverySnapshot(JSON.stringify({
      orderId: 45,
      amount: 28,
      qrCode: '',
      expiresAt: '2099-01-01T00:10:00.000Z',
      paymentType: 'airwallex',
      payUrl: '/payment/airwallex?order_id=45',
      outTradeNo: 'sub2_45',
      clientSecret: 'awx_cs',
      payAmount: 28,
      orderType: 'balance',
      paymentMode: '',
      resumeToken: 'resume-45',
      createdAt: Date.UTC(2099, 0, 1, 0, 0, 0),
    }), {
      now: Date.UTC(2099, 0, 1, 0, 1, 0),
      resumeToken: 'resume-45',
    })

    expect(restored?.orderId).toBe(45)
    expect(restored?.intentId).toBe('')
    expect(restored?.currency).toBe('')
    expect(restored?.countryCode).toBe('')
    expect(restored?.paymentEnv).toBe('')
  })

  it('restores a valid persisted payment discount as display-only recovery data', () => {
    const restored = readPaymentRecoverySnapshot(JSON.stringify({
      orderId: 46,
      amount: 100,
      qrCode: 'weixin://wxpay/bizpayurl?pr=coupon',
      expiresAt: '2099-01-01T00:10:00.000Z',
      paymentType: 'wxpay',
      payUrl: '',
      outTradeNo: 'sub2_46',
      clientSecret: '',
      intentId: '',
      currency: 'CNY',
      countryCode: '',
      paymentEnv: '',
      payAmount: 80,
      orderType: 'balance',
      paymentMode: 'native',
      resumeToken: 'resume-46',
      paymentDiscount: {
        code_id: 46,
        code: 'SAVE2026',
        original_amount: '100.00',
        discount_amount: '20.00',
        pay_amount: '80.00',
        currency: 'CNY',
      },
      createdAt: Date.UTC(2099, 0, 1, 0, 0, 0),
    }), {
      now: Date.UTC(2099, 0, 1, 0, 1, 0),
      resumeToken: 'resume-46',
    })

    expect(restored?.paymentDiscount).toMatchObject({ code: 'SAVE2026', pay_amount: '80.00' })
  })

  it('keeps recoverable orders independently and clears only the matched terminal order', () => {
    const entries = new Map<string, string>()
    const now = Date.now()
    const storage = {
      getItem: (key: string) => entries.get(key) ?? null,
      setItem: (key: string, value: string) => entries.set(key, value),
      removeItem: (key: string) => entries.delete(key),
    }
    const first: PaymentRecoverySnapshot = {
      orderId: 11,
      amount: 18,
      qrCode: '',
      expiresAt: '2026-09-13T01:00:00.000Z',
      paymentType: 'alipay',
      payUrl: 'https://pay.example.com/11',
      outTradeNo: 'sub2_11',
      clientSecret: '',
      intentId: '',
      currency: '',
      countryCode: '',
      paymentEnv: '',
      payAmount: 18,
      orderType: 'balance',
      paymentMode: 'redirect',
      resumeToken: 'resume-11',
      createdAt: now,
    }
    const second = { ...first, orderId: 12, outTradeNo: 'sub2_12', resumeToken: 'resume-12', createdAt: now + 60_000 }

    writePaymentRecoverySnapshot(storage, first)
    writePaymentRecoverySnapshot(storage, second)
    expect(readPaymentRecoverySnapshot(storage.getItem('payment.recovery.current'), {
      now: now + 120_000,
    })?.orderId).toBe(12)

    clearPaymentRecoverySnapshot(storage, 'payment.recovery.current', { orderId: 12 })
    expect(readPaymentRecoverySnapshot(storage.getItem('payment.recovery.current'), {
      now: now + 120_000,
    })?.orderId).toBe(11)
  })
})

describe('reset-card checkout attempts', () => {
  const attemptInput = {
    userId: 9,
    subscriptionId: 21,
    groupId: 4,
    planId: 7,
    amount: 40,
    monthlyPrice: 120,
    expiresAt: '2099-01-01T00:00:00Z',
    paymentType: 'wxpay',
  }

  function memoryStorage() {
    const values = new Map<string, string>()
    return {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
      removeItem: (key: string) => values.delete(key),
    }
  }

  it('writes before the request, reuses after response loss, and clears only its recorded terminal order', () => {
    const storage = memoryStorage()
    let generated = 0
    const createKey = () => `reset-card-payment-${++generated}`

    const first = getOrCreateResetCardCheckoutAttempt(storage, attemptInput, createKey)
    expect(storage.getItem(RESET_CARD_CHECKOUT_ATTEMPT_STORAGE_KEY)).toContain(first.idempotencyKey)

    const retry = getOrCreateResetCardCheckoutAttempt(storage, attemptInput, createKey)
    expect(retry).toEqual(first)
    expect(generated).toBe(1)

    recordResetCardCheckoutOrder(storage, first, 88)
    clearResetCardCheckoutAttempt(storage, { orderId: 89 })
    expect(storage.getItem(RESET_CARD_CHECKOUT_ATTEMPT_STORAGE_KEY)).toContain('"orderId":88')

    clearResetCardCheckoutAttempt(storage, { orderId: 88 })
    expect(storage.getItem(RESET_CARD_CHECKOUT_ATTEMPT_STORAGE_KEY)).toBeNull()
  })

  it('replaces the key when any quote-bound checkout input changes', () => {
    const storage = memoryStorage()
    let generated = 0
    const createKey = () => `reset-card-payment-${++generated}`
    const first = getOrCreateResetCardCheckoutAttempt(storage, attemptInput, createKey)
    const changedQuote = getOrCreateResetCardCheckoutAttempt(storage, {
      ...attemptInput,
      amount: 41,
      expiresAt: '2099-02-01T00:00:00Z',
    }, createKey)

    expect(changedQuote.idempotencyKey).not.toBe(first.idempotencyKey)
    expect(changedQuote.fingerprint).not.toBe(first.fingerprint)
    expect(createResetCardCheckoutFingerprint({ ...attemptInput, validityDays: 15, expiresAt: '2026-10-01' }))
      .toBe(createResetCardCheckoutFingerprint({ ...attemptInput, validityDays: 15, expiresAt: '2026-10-02' }))
    expect(createResetCardCheckoutFingerprint({ ...attemptInput, paymentType: 'alipay' }))
      .not.toBe(first.fingerprint)
    expect(createResetCardCheckoutFingerprint({ ...attemptInput, tierRevision: 'v1:3:gpt:2:124' }))
      .not.toBe(first.fingerprint)
    expect(createResetCardCheckoutFingerprint({ ...attemptInput, quantity: 2 }))
      .not.toBe(first.fingerprint)
    expect(createResetCardCheckoutFingerprint({ ...attemptInput, useOnPurchase: true }))
      .not.toBe(first.fingerprint)
    expect(createResetCardCheckoutFingerprint({ ...attemptInput, couponCode: 'SAVE2026', couponRevision: 'revision-1' }))
      .not.toBe(first.fingerprint)
  })

  it('clears an unbound retry when choices change but never reuses a pending order key', () => {
    const storage = memoryStorage()
    let generated = 0
    const createKey = () => `reset-card-payment-${++generated}`
    const first = getOrCreateResetCardCheckoutAttempt(storage, attemptInput, createKey)

    discardResetCardCheckoutAttemptForSelectionChange(storage)
    expect(storage.getItem(RESET_CARD_CHECKOUT_ATTEMPT_STORAGE_KEY)).toBeNull()

    const pending = getOrCreateResetCardCheckoutAttempt(storage, attemptInput, createKey)
    recordResetCardCheckoutOrder(storage, pending, 88)
    discardResetCardCheckoutAttemptForSelectionChange(storage)
    expect(storage.getItem(RESET_CARD_CHECKOUT_ATTEMPT_STORAGE_KEY)).toContain(pending.idempotencyKey)

    const changed = getOrCreateResetCardCheckoutAttempt(storage, { ...attemptInput, quantity: 2 }, createKey)
    expect(changed.idempotencyKey).not.toBe(first.idempotencyKey)
    expect(changed.idempotencyKey).not.toBe(pending.idempotencyKey)
  })

  it('matches an OAuth resume only to the browser attempt named by its signed hash payload', async () => {
    const storage = memoryStorage()
    const key = 'reset-card-payment-oauth-attempt'
    const attempt = getOrCreateResetCardCheckoutAttempt(storage, attemptInput, () => key)
    const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(key))
    const hash = Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, '0')).join('')
    const payload = btoa(JSON.stringify({
      tk: 'wechat_payment_resume',
      ot: 'reset_card',
      ikh: hash,
    })).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')

    await expect(matchResetCardCheckoutAttemptForResume(storage, `${payload}.signature`))
      .resolves.toEqual(attempt)

    const otherPayload = btoa(JSON.stringify({
      tk: 'wechat_payment_resume',
      ot: 'reset_card',
      ikh: 'f'.repeat(64),
    })).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
    await expect(matchResetCardCheckoutAttemptForResume(storage, `${otherPayload}.signature`))
      .resolves.toBeNull()
  })
})
