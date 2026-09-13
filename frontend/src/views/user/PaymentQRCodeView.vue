<template>
  <AppLayout>
    <div class="mx-auto max-w-md py-8">
      <PaymentStatusPanel
        :order-id="orderId"
        :amount="amount"
        :pay-amount="payAmount"
        :qr-code="qrCode"
        :expires-at="expiresAt"
        :payment-type="paymentType"
        :pay-url="payUrl"
        :order-type="orderType"
        :currency="currency"
        :out-trade-no="outTradeNo"
        :mobile-alipay-deep-link="mobileAlipayDeepLink"
        @done="router.push('/purchase')"
      />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { useRoute, useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import PaymentStatusPanel from '@/components/payment/PaymentStatusPanel.vue'

const route = useRoute()
const router = useRouter()

function queryString(key: string): string {
  const value = route.query[key]
  if (Array.isArray(value)) return typeof value[0] === 'string' ? value[0] : ''
  return typeof value === 'string' ? value : ''
}

function positiveInteger(value: string): number {
  const parsed = Number(value)
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : 0
}

function positiveNumber(value: string): number | undefined {
  const parsed = Number(value)
  return Number.isFinite(parsed) && parsed > 0 ? parsed : undefined
}

const orderId = positiveInteger(queryString('order_id'))
const amount = positiveNumber(queryString('amount'))
const payAmount = positiveNumber(queryString('pay_amount'))
const qrCode = queryString('qr') || queryString('qr_code')
const expiresAt = queryString('expires_at')
const paymentType = queryString('payment_type')
const payUrl = queryString('pay_url')
const orderType = queryString('order_type')
const currency = queryString('currency')
const outTradeNo = queryString('out_trade_no')
const mobileAlipayDeepLink = queryString('alipay_mobile_precreate_deep_link') === 'true'
</script>
