<template>
  <section class="mt-7 border-t border-gray-200 pt-5 dark:border-dark-700">
    <div class="flex items-center gap-2">
      <Icon name="refresh" size="sm" class="text-primary-600 dark:text-primary-400" />
      <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('payment.resetShop.title') }}</h2>
    </div>
    <p class="mt-1.5 text-xs leading-relaxed text-gray-500 dark:text-gray-400">{{ t('payment.resetShop.hint') }}</p>
    <p v-if="!offers.length" class="mt-3 text-sm text-gray-500">{{ t('payment.resetShop.requiresSubscription') }}</p>
    <div v-else class="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-2">
      <button v-for="offer in offers" :key="offer.subscription.id" type="button"
        class="group flex min-w-0 items-center gap-3 rounded-xl border border-gray-200 bg-white px-4 py-3.5 text-left transition hover:border-primary-400 hover:bg-primary-50/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 disabled:cursor-wait disabled:opacity-60 dark:border-dark-700 dark:bg-dark-800 dark:hover:border-primary-600 dark:hover:bg-primary-950/30"
        :disabled="loading || buying" @click="select(offer.subscription)">
        <span class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-primary-50 text-primary-600 dark:bg-primary-900/30 dark:text-primary-300"><Icon name="refresh" size="sm" /></span>
        <span class="min-w-0 flex-1">
          <span class="block truncate text-sm font-semibold text-gray-900 dark:text-white">{{ offer.subscription.group?.name }}</span>
          <span class="mt-0.5 block text-xs text-gray-500 dark:text-dark-400">{{ t('payment.resetShop.singleCard') }}</span>
        </span>
        <span class="shrink-0 text-right">
          <strong class="block text-lg font-semibold tabular-nums text-gray-900 dark:text-white">{{ offer.price }}</strong>
          <span class="text-[10px] text-gray-500 dark:text-dark-400">{{ t('payment.creditUnit') }}</span>
        </span>
        <Icon name="chevronRight" size="xs" class="shrink-0 text-gray-400 group-hover:text-primary-500" />
      </button>
    </div>
    <p v-if="error" role="alert" class="mt-3 text-sm text-red-600 dark:text-red-400">{{ error }}</p>
    <p v-if="success" role="status" class="mt-3 text-sm text-emerald-600">{{ t('payment.resetShop.success') }}</p>
    <ConfirmDialog :show="!!selected" :title="t('payment.resetShop.title')"
      :message="selected ? t('payment.resetShop.confirm', { name: selected.name, price: selected.quote.price.toFixed(2) }) : ''"
      :confirm-text="buying ? t('common.processing') : t('payment.resetShop.buy')"
      @confirm="purchase" @cancel="close">
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    </ConfirmDialog>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { getResetCardQuote, purchaseResetCard, type ResetCardQuote } from '@/api/subscriptions'
import { useAuthStore } from '@/stores/auth'
import type { UserSubscription } from '@/types'
import type { SubscriptionPlan } from '@/types/payment'
import Icon from '@/components/icons/Icon.vue'

const props = withDefaults(defineProps<{ subscriptions: UserSubscription[]; plans?: SubscriptionPlan[] }>(), { plans: () => [] })
const offers = computed(() => props.subscriptions.flatMap(subscription => {
  if (subscription.group?.platform !== 'openai') return []
  const monthlyPlans = props.plans.filter(plan => plan.group_id === subscription.group_id && plan.group_platform === 'openai' &&
    plan.currency?.toUpperCase() === 'CNY' && ((['month', 'months'].includes(plan.validity_unit || '') && plan.validity_days === 1) ||
      (['day', 'days', ''].includes(plan.validity_unit || '') && plan.validity_days === 30)))
  if (monthlyPlans.length !== 1) return []
  const plan = monthlyPlans[0]
  const price = plan.entitlements?.reset_card_purchase_price ?? Math.round(plan.price / 3 * 100) / 100
  return Number.isFinite(price) && price > 0 ? [{ subscription, price }] : []
}))
const emit = defineEmits<{ purchased: [] }>()
const { t } = useI18n()
const authStore = useAuthStore()
const loading = ref(false)
const buying = ref(false)
const error = ref('')
const success = ref(false)
type Attempt = { quote: ResetCardQuote; key: string; name: string }
const selected = ref<Attempt | null>(null)
// Retain a failed/uncertain operation when the dialog is closed and reopened.
// A retry must not silently become a second wallet debit.
const attempts = new Map<number, Attempt>()

function storageKey(subscriptionID: number): string | null {
  return authStore.user?.id ? `reset-card-purchase:${authStore.user.id}:${subscriptionID}` : null
}

function restoreAttempt(subscriptionID: number): Attempt | undefined {
  const key = storageKey(subscriptionID)
  if (!key) return undefined
  try {
    const saved = JSON.parse(sessionStorage.getItem(key) || 'null') as Attempt | null
    if (saved?.quote?.subscription_id === subscriptionID && typeof saved.key === 'string' &&
        typeof saved.name === 'string' && Number.isFinite(saved.quote.price) && saved.quote.price > 0) return saved
  } catch { /* Storage is optional; the in-memory retry remains available. */ }
  return undefined
}

function rememberAttempt(attempt: Attempt) {
  const key = storageKey(attempt.quote.subscription_id)
  try { if (key) sessionStorage.setItem(key, JSON.stringify(attempt)) } catch { /* See above. */ }
}

function forgetAttempt(subscriptionID: number) {
  attempts.delete(subscriptionID)
  const key = storageKey(subscriptionID)
  try { if (key) sessionStorage.removeItem(key) } catch { /* See above. */ }
}

function errorMessage(value: unknown): string {
  const candidate = value as { message?: string }
  return candidate?.message || t('payment.resetShop.failed')
}

async function select(sub: UserSubscription) {
  if (loading.value || buying.value) return
  error.value = ''
  success.value = false
  loading.value = true
  try {
    selected.value = attempts.get(sub.id) || restoreAttempt(sub.id) || {
      quote: await getResetCardQuote(sub.id),
      key: crypto.randomUUID(),
      name: sub.group?.name || t('payment.groupFallback', { id: sub.group_id }),
    }
    attempts.set(sub.id, selected.value)
  } catch (err) {
    error.value = errorMessage(err)
  } finally {
    loading.value = false
  }
}

function close() {
  if (!buying.value) selected.value = null
}

async function purchase() {
  if (!selected.value || buying.value) return
  const attempt = selected.value
  buying.value = true
  error.value = ''
  // A tab switch or page reload after a lost response must replay this debit.
  rememberAttempt(attempt)
  try {
    await purchaseResetCard(attempt.quote, attempt.key)
    forgetAttempt(attempt.quote.subscription_id)
    selected.value = null
    success.value = true
    emit('purchased')
    // The purchase is committed; a refresh failure must not invite a new debit.
    void authStore.refreshUser().catch(() => {})
  } catch (err) {
    error.value = errorMessage(err)
    const failure = err as { code?: string | number; reason?: string }
    if (failure.reason === 'RESET_CARD_QUOTE_CHANGED' || failure.code === 'RESET_CARD_QUOTE_CHANGED') {
      forgetAttempt(attempt.quote.subscription_id)
      selected.value = null
    }
  } finally {
    buying.value = false
  }
}
</script>
