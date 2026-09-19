<template>
  <section class="mt-7 border-t border-gray-200 pt-5 dark:border-dark-700">
    <div class="flex items-center gap-2">
      <Icon name="refresh" size="sm" class="text-primary-600 dark:text-primary-400" />
      <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('payment.resetShop.title') }}</h2>
    </div>
    <p class="mt-1.5 text-xs leading-relaxed text-gray-500 dark:text-gray-400">{{ t('payment.resetShop.hint') }}</p>
    <p class="mt-1 text-xs leading-relaxed text-gray-500 dark:text-gray-400">{{ t('payment.resetShop.tierBindingHint') }}</p>
    <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('payment.resetShop.noCoupons') }}</p>
    <p v-if="!offers.length" class="mt-3 text-sm text-gray-500">{{ t('payment.resetShop.requiresSubscription') }}</p>
    <div v-else class="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-2">
      <button v-for="offer in offers" :key="offer.subscription.id" type="button"
        class="group flex min-w-0 items-center gap-3 rounded-xl border border-gray-200 bg-white px-4 py-3.5 text-left transition hover:border-primary-400 hover:bg-primary-50/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 disabled:cursor-not-allowed disabled:opacity-70 dark:border-dark-700 dark:bg-dark-800 dark:hover:border-primary-600 dark:hover:bg-primary-950/30"
        :disabled="disabled || loading || offer.eligibility?.can_purchase === false" @click="select(offer.subscription)">
        <span class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-primary-50 text-primary-600 dark:bg-primary-900/30 dark:text-primary-300"><Icon name="refresh" size="sm" /></span>
          <span class="min-w-0 flex-1">
            <span class="block truncate text-sm font-semibold text-gray-900 dark:text-white">{{ offer.title }}</span>
            <span class="mt-0.5 block text-xs text-gray-500 dark:text-dark-400">{{ offer.description || t('payment.resetShop.singleCard') }}
              <PurchaseEligibilityHint :eligibility="offer.eligibility" /></span>
            <span v-if="offer.tier" class="mt-1 inline-flex rounded-full bg-primary-50 px-2 py-0.5 text-[10px] font-medium text-primary-700 dark:bg-primary-900/30 dark:text-primary-200">
              {{ t('payment.resetShop.tierRank', { rank: offer.tier.tier_rank }) }}
            </span>
          </span>
        <span class="shrink-0 text-right">
          <strong class="block text-lg font-semibold tabular-nums text-gray-900 dark:text-white">{{ offer.price }}</strong>
          <span class="text-[10px] text-gray-500 dark:text-dark-400">{{ t('payment.currencyUnit') }}</span>
        </span>
        <Icon name="chevronRight" size="xs" class="shrink-0 text-gray-400 group-hover:text-primary-500" />
      </button>
    </div>
    <p v-if="error" role="alert" class="mt-3 text-sm text-red-600 dark:text-red-400">{{ error }}</p>
    <ConfirmDialog :show="!!selected" :title="t('payment.resetShop.title')"
      :message="selected ? t('payment.resetShop.confirm', { name: selected.name, price: selected.quote.price.toFixed(2) }) : ''"
      :confirm-text="t('payment.resetShop.buy')"
      @confirm="purchase" @cancel="close">
      <p v-if="selected?.quote.reset_card_tier" class="mb-2 text-sm font-medium text-gray-900 dark:text-white">
        {{ t('payment.resetShop.tierRank', { rank: selected.quote.reset_card_tier.tier_rank }) }}
      </p>
      <p class="text-sm text-gray-600 dark:text-gray-300">
        {{ t(selected?.quote.reset_card_tier
          ? 'payment.resetShop.tierBindingConfirm'
          : 'payment.resetShop.exactBindingConfirm') }}
      </p>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    </ConfirmDialog>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { getResetCardQuote, type ResetCardQuote } from '@/api/subscriptions'
import { extractI18nErrorMessage } from '@/utils/apiError'
import type { UserSubscription } from '@/types'
import type { SubscriptionPlan } from '@/types/payment'
import Icon from '@/components/icons/Icon.vue'
import PurchaseEligibilityHint from './PurchaseEligibilityHint.vue'

const props = withDefaults(defineProps<{
  subscriptions: UserSubscription[]
  plans?: SubscriptionPlan[]
  disabled?: boolean
}>(), {
  plans: () => [],
  disabled: false,
})
const offers = computed(() => props.subscriptions.flatMap(subscription => {
  if (subscription.group?.platform !== 'openai' || subscription.status !== 'active' || (subscription.expires_at && Date.parse(subscription.expires_at) <= Date.now())) return []
  const monthlyPlans = props.plans.filter(plan => plan.group_id === subscription.group_id && plan.group_platform === 'openai' &&
    plan.currency?.toUpperCase() === 'CNY' && ((['month', 'months'].includes(plan.validity_unit || '') && plan.validity_days === 1) ||
      (['day', 'days', ''].includes(plan.validity_unit || '') && plan.validity_days === 30)))
  if (monthlyPlans.length !== 1) return []
  const plan = monthlyPlans[0]
  if (plan.reset_card_eligibility?.visible === false) return []
  const price = plan.entitlements?.reset_card_purchase_price ?? Math.round(plan.price / 3 * 100) / 100
  return Number.isFinite(price) && price > 0 ? [{
    subscription,
    price,
    eligibility: plan.reset_card_eligibility,
    title: plan.entitlements?.reset_card_title || subscription.group?.name,
    description: plan.entitlements?.reset_card_description,
    tier: plan.reset_card_tier,
  }] : []
}))
const emit = defineEmits<{ checkout: [payload: { subscription: UserSubscription; quote: ResetCardQuote }] }>()
const { t } = useI18n()
const loading = ref(false)
const error = ref('')
type Attempt = { quote: ResetCardQuote; name: string }
const selected = ref<Attempt | null>(null)

function errorMessage(value: unknown): string {
  return extractI18nErrorMessage(value, t, 'payment.errors', t('payment.resetShop.failed'))
}

async function select(sub: UserSubscription) {
  if (props.disabled || loading.value) return
  error.value = ''
  loading.value = true
  try {
    selected.value = {
      quote: await getResetCardQuote(sub.id),
      name: sub.group?.name || t('payment.groupFallback', { id: sub.group_id }),
    }
  } catch (err) {
    error.value = errorMessage(err)
  } finally {
    loading.value = false
  }
}

function close() {
  selected.value = null
}

async function purchase() {
  if (!selected.value || props.disabled) return
  const attempt = selected.value
  const subscription = props.subscriptions.find(item => item.id === attempt.quote.subscription_id)
  if (!subscription) {
    error.value = t('payment.resetShop.failed')
    return
  }
  selected.value = null
  emit('checkout', { subscription, quote: attempt.quote })
}
</script>
