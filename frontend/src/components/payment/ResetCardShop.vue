<template>
  <section class="card mt-6 p-5">
    <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('payment.resetShop.title') }}</h2>
    <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('payment.resetShop.hint') }}</p>
    <p v-if="!subscriptions.length" class="mt-3 text-sm text-gray-500">{{ t('payment.resetShop.requiresSubscription') }}</p>
    <div v-else class="mt-4 space-y-3">
      <div v-for="sub in subscriptions" :key="sub.id" class="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-gray-200 p-3 dark:border-dark-600">
        <span class="text-sm font-medium">{{ sub.group?.name || t('payment.groupFallback', { id: sub.group_id }) }}</span>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="loading || buying" @click="select(sub)">{{ t('payment.resetShop.quote') }}</button>
      </div>
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
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { getResetCardQuote, purchaseResetCard, type ResetCardQuote } from '@/api/subscriptions'
import { useAuthStore } from '@/stores/auth'
import type { UserSubscription } from '@/types'

defineProps<{ subscriptions: UserSubscription[] }>()
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
