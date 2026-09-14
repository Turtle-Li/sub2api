<template>
  <section class="space-y-4" :aria-label="t('payment.admin.balanceProducts')">
    <div class="flex flex-col gap-3 rounded-xl border border-gray-200 bg-gray-50 p-4 sm:flex-row sm:items-start sm:justify-between dark:border-dark-600 dark:bg-dark-800">
      <div>
        <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('payment.admin.rechargeCatalog') }}</h2>
        <p class="mt-1 max-w-3xl text-sm leading-6 text-gray-500 dark:text-gray-400">{{ t('payment.admin.rechargeCatalogHint') }}</p>
      </div>
      <div class="flex shrink-0 gap-2">
        <button type="button" class="btn btn-secondary" :disabled="loading || saving" @click="load">
          <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
          {{ t('common.refresh') }}
        </button>
        <button type="button" class="btn btn-primary" :disabled="loading || saving || !catalogValid" @click="save">
          {{ saving ? t('common.processing') : t('common.save') }}
        </button>
      </div>
    </div>

    <div v-if="loadError" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">
      {{ loadError }}
    </div>
    <div v-if="storedCatalogInvalid" role="alert" class="rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-200">
      {{ t('payment.admin.rechargeCatalogInvalid') }}
    </div>

    <div v-if="loading" role="status" class="rounded-lg border border-gray-200 p-6 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400">
      {{ t('common.loading') }}
    </div>
    <template v-else>
      <RechargeOptionsEditor v-model="catalogJSON" @validity="catalogValid = $event" />

      <section class="rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-600 dark:bg-dark-800">
        <label for="recommended-recharge" class="input-label">{{ t('payment.admin.recommendedRecharge') }}</label>
        <select id="recommended-recharge" v-model.number="recommendedAmount" class="input mt-1 max-w-sm">
          <option :value="0">{{ t('payment.admin.noRecommendedRecharge') }}</option>
          <option
            v-for="option in enabledOptions"
            :key="`${option.amount}-${option.label || ''}`"
            :value="Number(option.amount)"
          >
            {{ option.label || option.amount }}
          </option>
        </select>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('payment.admin.recommendedRechargeHint') }}</p>
      </section>

      <details class="rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-600 dark:bg-dark-800">
        <summary class="cursor-pointer text-sm font-medium text-gray-700 dark:text-gray-200">{{ t('payment.eligibility.advancedJson') }}</summary>
        <textarea v-model="catalogJSON" rows="10" class="input mt-3 font-mono text-xs" spellcheck="false"></textarea>
      </details>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminPaymentAPI } from '@/api/admin/payment'
import { extractI18nErrorMessage } from '@/utils/apiError'
import type { RechargeOption } from '@/types/payment'
import Icon from '@/components/icons/Icon.vue'
import RechargeOptionsEditor from '@/components/payment/RechargeOptionsEditor.vue'

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(false)
const saving = ref(false)
const loadError = ref('')
const catalogJSON = ref('[]')
const catalogValid = ref(true)
const storedCatalogInvalid = ref(false)
const recommendedAmount = ref(0)

const parsedOptions = computed<Array<Record<string, unknown>>>(() => {
  try {
    const value = JSON.parse(catalogJSON.value || '[]') as unknown
    return Array.isArray(value) && value.every(item => item && typeof item === 'object' && !Array.isArray(item))
      ? value as Array<Record<string, unknown>>
      : []
  } catch {
    return []
  }
})

const enabledOptions = computed(() => parsedOptions.value.filter(option => {
  const amount = Number(option.amount)
  return Number.isFinite(amount) && amount > 0 && option.enabled !== false
}))

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    const response = await adminPaymentAPI.getConfig()
    const options = response.data.recharge_options || []
    catalogJSON.value = JSON.stringify(options, null, 2)
    storedCatalogInvalid.value = response.data.recharge_options_invalid === true
    recommendedAmount.value = Number(options.find(option => option.recommended && option.enabled !== false)?.amount || 0)
    catalogValid.value = true
  } catch (error: unknown) {
    loadError.value = extractI18nErrorMessage(error, t, 'payment.errors', t('common.error'))
  } finally {
    loading.value = false
  }
}

async function save() {
  if (saving.value || loading.value) return
  if (!catalogValid.value) {
    appStore.showError(t('payment.eligibility.invalidCatalog'))
    return
  }
  let options: RechargeOption[]
  try {
    const parsed = JSON.parse(catalogJSON.value || '[]') as unknown
    if (!Array.isArray(parsed)) throw new Error('catalog is not an array')
    const selected = Number(recommendedAmount.value) || 0
    options = parsed.map((item) => {
      if (!item || typeof item !== 'object' || Array.isArray(item)) throw new Error('catalog row is invalid')
      const option = { ...(item as RechargeOption) }
      delete option.recommended
      if (selected > 0 && option.enabled !== false && Number(option.amount) === selected) option.recommended = true
      return option
    })
  } catch {
    appStore.showError(t('payment.eligibility.invalidCatalog'))
    return
  }

  saving.value = true
  try {
    await adminPaymentAPI.updateConfig({ recharge_options: options })
    appStore.showSuccess(t('payment.admin.rechargeCatalogSaved'))
    await load()
  } catch (error: unknown) {
    appStore.showError(extractI18nErrorMessage(error, t, 'payment.errors', t('common.error')))
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>
