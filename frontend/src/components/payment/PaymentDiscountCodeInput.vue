<template>
  <section class="rounded-lg border border-gray-200 bg-gray-50 p-3 dark:border-dark-700 dark:bg-dark-800/70" data-test="payment-discount-code">
    <div class="flex items-center justify-between gap-3">
      <label :for="inputId" class="text-sm font-medium text-gray-800 dark:text-gray-100">
        {{ t('payment.coupon.label') }}
      </label>
      <span v-if="applied" class="text-xs font-medium text-emerald-700 dark:text-emerald-300">
        {{ t('payment.coupon.applied') }}
      </span>
    </div>
    <div class="mt-2 flex gap-2">
      <input
        :id="inputId"
        :value="modelValue"
        type="text"
        autocomplete="off"
        autocapitalize="characters"
        spellcheck="false"
        maxlength="32"
        class="input min-w-0 flex-1 font-mono uppercase"
        :placeholder="t('payment.coupon.placeholder')"
        :disabled="disabled || applying"
        :aria-describedby="status ? `${inputId}-status` : undefined"
        :aria-invalid="error || undefined"
        @input="emit('update:modelValue', ($event.target as HTMLInputElement).value)"
        @keyup.enter.prevent="emit('apply')"
      />
      <button
        v-if="!applied"
        type="button"
        class="btn btn-secondary shrink-0"
        data-test="apply-payment-coupon"
        :disabled="disabled || applying || !modelValue.trim()"
        @click="emit('apply')"
      >
        {{ applying ? t('common.processing') : t('payment.coupon.apply') }}
      </button>
      <button
        v-else
        type="button"
        class="btn btn-secondary shrink-0"
        data-test="remove-payment-coupon"
        :disabled="disabled || applying"
        @click="emit('remove')"
      >
        {{ t('payment.coupon.remove') }}
      </button>
    </div>
    <p v-if="status" :id="`${inputId}-status`" aria-live="polite" :class="['mt-2 text-xs leading-relaxed', error ? 'text-red-600 dark:text-red-300' : 'text-gray-500 dark:text-dark-400']">
      {{ status }}
    </p>
    <div v-if="applied" class="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-xs text-gray-600 dark:text-dark-300 lg:hidden" data-test="payment-discount-mobile-breakdown">
      <span>{{ t('payment.coupon.originalAmount') }} {{ formatQuoteAmount(applied.original_amount, applied.currency) }}</span>
      <span class="text-emerald-700 dark:text-emerald-300">{{ t('payment.coupon.discount') }} -{{ formatQuoteAmount(applied.discount_amount, applied.currency) }}</span>
    </div>
  </section>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { PaymentDiscountQuote } from '@/types/payment'
import { formatPaymentAmount } from './currency'

withDefaults(defineProps<{
  modelValue: string
  applied?: PaymentDiscountQuote | null
  applying?: boolean
  disabled?: boolean
  status?: string
  error?: boolean
  inputId?: string
}>(), {
  applied: null,
  applying: false,
  disabled: false,
  status: '',
  error: false,
  inputId: 'payment-discount-code',
})

const emit = defineEmits<{
  'update:modelValue': [value: string]
  apply: []
  remove: []
}>()

const { t } = useI18n()

function formatQuoteAmount(value: string, currency: string): string {
  return formatPaymentAmount(Number(value), currency)
}
</script>
