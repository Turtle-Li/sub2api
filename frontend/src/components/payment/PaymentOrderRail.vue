<template>
  <!--
    The running total follows the reader. On wide screens this is a sticky
    column beside the products; below `lg` it detaches and pins to the bottom of
    the viewport, which is where a checkout total belongs on a phone. Either way
    the amount and the pay button are visible at the moment of choosing, instead
    of three screens further down.
  -->
  <aside
    :class="[
      'z-30',
      'max-lg:fixed max-lg:inset-x-0 max-lg:bottom-0 max-lg:border-t max-lg:border-gray-200 max-lg:bg-white/95 max-lg:p-4 max-lg:backdrop-blur',
      'max-lg:dark:border-dark-700 max-lg:dark:bg-dark-900/95',
      'lg:sticky lg:top-6',
    ]"
  >
    <div class="payment-rail max-lg:rounded-none max-lg:border-0 max-lg:bg-transparent max-lg:p-0 max-lg:shadow-none">
      <div class="flex flex-col gap-4">
        <!-- Chosen product. Hidden on narrow screens, where the bar is a total
             plus an action and the product is already visible above it. -->
        <div v-if="productName" class="max-lg:hidden">
          <p class="payment-product-card__eyebrow">{{ t('payment.orderSummary') }}</p>
          <p class="mt-1.5 break-words text-sm font-semibold text-gray-900 dark:text-white">{{ productName }}</p>
          <p v-if="productMeta" class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ productMeta }}</p>
        </div>

        <div v-if="methods.length > 0" :class="methodsCollapsedOnMobile ? 'max-lg:hidden' : ''">
          <PaymentMethodSelector :methods="methods" :selected="selectedMethod" @select="emit('select-method', $event)" />
        </div>

        <div v-if="hasAmount" class="flex flex-col gap-2">
          <div v-if="showBreakdown" class="max-lg:hidden flex flex-col gap-2">
            <div class="payment-rail__row">
              <span class="payment-rail__label">{{ t('payment.paymentAmount') }}</span>
              <span class="payment-rail__value">{{ formatPay(baseAmount) }}</span>
            </div>
            <div v-if="feeAmount > 0" class="payment-rail__row">
              <span class="payment-rail__label">{{ t('payment.fee') }} ({{ feeRate }}%)</span>
              <span class="payment-rail__value">{{ formatPay(feeAmount) }}</span>
            </div>
          </div>

          <div class="payment-rail__row border-t border-gray-100 pt-2.5 dark:border-dark-700 max-lg:border-0 max-lg:pt-0">
            <span class="font-medium text-gray-700 dark:text-gray-300">{{ t('payment.actualPay') }}</span>
            <span class="payment-rail__total">{{ formatPay(totalAmount) }}</span>
          </div>

          <!-- What the account receives, kept visually subordinate to the
               charge so the two units never read as one number. -->
          <div v-if="creditLine" class="payment-rail__row">
            <span class="payment-rail__label">{{ creditLabel }}</span>
            <span class="payment-rail__value text-emerald-600 dark:text-emerald-400">{{ creditLine }}</span>
          </div>
        </div>

        <p v-if="notice" class="text-xs leading-relaxed text-amber-600 dark:text-amber-300">{{ notice }}</p>

        <button
          type="button"
          :class="['btn w-full py-3 text-base font-medium', buttonClass]"
          :disabled="disabled || submitting"
          @click="emit('submit')"
        >
          <span v-if="submitting" class="flex items-center justify-center gap-2">
            <span class="h-4 w-4 animate-spin rounded-full border-2 border-white border-t-transparent"></span>
            {{ t('common.processing') }}
          </span>
          <span v-else>{{ actionLabel }}</span>
        </button>

        <p v-if="footnote" class="text-center text-xs text-gray-400 dark:text-dark-500 max-lg:hidden">{{ footnote }}</p>
      </div>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import PaymentMethodSelector from './PaymentMethodSelector.vue'
import type { PaymentMethodOption } from './PaymentMethodSelector.vue'

const props = withDefaults(defineProps<{
  productName?: string
  productMeta?: string
  methods?: PaymentMethodOption[]
  selectedMethod?: string
  /** Gateway charge before fee, already in the gateway currency. */
  baseAmount?: number
  feeRate?: number
  feeAmount?: number
  totalAmount?: number
  /** Platform credit the account receives, pre-formatted with its unit noun. */
  creditLine?: string
  creditLabel?: string
  notice?: string
  footnote?: string
  actionLabel: string
  buttonClass?: string
  disabled?: boolean
  submitting?: boolean
  /** Format helper supplied by the page so currency handling stays in one place. */
  formatPay: (value: number) => string
  /** Subscriptions have a single price with no multiplier, so no breakdown. */
  showBreakdown?: boolean
  methodsCollapsedOnMobile?: boolean
}>(), {
  productName: '',
  productMeta: '',
  methods: () => [],
  selectedMethod: '',
  baseAmount: 0,
  feeRate: 0,
  feeAmount: 0,
  totalAmount: 0,
  creditLine: '',
  creditLabel: '',
  notice: '',
  footnote: '',
  buttonClass: 'btn-primary',
  disabled: false,
  submitting: false,
  showBreakdown: true,
  methodsCollapsedOnMobile: false,
})

const emit = defineEmits<{
  'select-method': [value: string]
  submit: []
}>()

const { t } = useI18n()

const hasAmount = computed(() => props.totalAmount > 0)
</script>
