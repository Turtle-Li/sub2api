<template>
  <section class="card p-6" data-testid="pricing-currency-settings-card">
    <div class="mb-4">
      <h3 class="text-base font-semibold text-gray-900 dark:text-white">
        {{ t('admin.settings.pricingCurrency.title') }}
      </h3>
      <p class="mt-1 max-w-3xl text-sm text-gray-500 dark:text-gray-400">
        {{ t('admin.settings.pricingCurrency.description') }}
      </p>
    </div>

    <div v-if="loading" class="flex items-center gap-2 text-sm text-gray-500 dark:text-gray-400">
      <span class="h-4 w-4 animate-spin rounded-full border-2 border-primary-600 border-t-transparent" />
      {{ t('common.loading') }}
    </div>

    <div v-else class="space-y-4">
      <div
        role="note"
        class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-900/70 dark:bg-amber-950/30 dark:text-amber-100"
      >
        {{ t('admin.settings.pricingCurrency.migrationNotice') }}
      </div>

      <div class="grid gap-4 md:grid-cols-2">
        <div>
          <p class="mb-1 text-xs font-medium text-gray-600 dark:text-gray-400">
            {{ t('admin.settings.pricingCurrency.settlementCurrency') }}
          </p>
          <output
            data-testid="pricing-currency-settlement-currency"
            class="flex h-10 items-center rounded-lg border border-gray-200 bg-gray-50 px-3 font-medium text-gray-900 dark:border-dark-600 dark:bg-dark-800 dark:text-white"
          >
            {{ form.settlement_currency }} {{ form.settlement_currency === 'CNY' ? '(¥)' : '($)' }}
          </output>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.settings.pricingCurrency.settlementCurrencyHint') }}
          </p>
        </div>

        <div>
          <label for="pricing-currency-rate" class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
            {{ t('admin.settings.pricingCurrency.usdToCnyRate') }}
          </label>
          <input
            id="pricing-currency-rate"
            v-model.number="form.usd_to_cny_rate"
            data-testid="pricing-currency-rate"
            type="number"
            min="0.000001"
            step="0.000001"
            class="input w-full"
            :class="{ 'border-red-500': !rateIsValid }"
          >
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.settings.pricingCurrency.rateHint') }}
          </p>
        </div>
      </div>

      <p class="text-sm text-gray-600 dark:text-gray-300">
        {{ t('admin.settings.pricingCurrency.subscriptionScope') }}
      </p>

      <div class="flex justify-end">
        <button
          type="button"
          class="btn btn-primary"
          data-testid="pricing-currency-save"
          :disabled="saving || !rateIsValid"
          @click="save"
        >
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api'
import type { PricingCurrencySettings } from '@/api/admin/settings'
import { useAppStore } from '@/stores'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const appStore = useAppStore()
const loading = ref(true)
const saving = ref(false)

const form = reactive<PricingCurrencySettings>({
  settlement_currency: 'USD',
  usd_to_cny_rate: 6.75,
})

const rateIsValid = computed(() =>
  Number.isFinite(Number(form.usd_to_cny_rate)) && Number(form.usd_to_cny_rate) > 0
)

function applySettings(settings: PricingCurrencySettings) {
  form.settlement_currency = settings.settlement_currency === 'CNY' ? 'CNY' : 'USD'
  form.usd_to_cny_rate = Number(settings.usd_to_cny_rate)
}

async function load() {
  loading.value = true
  try {
    applySettings(await adminAPI.settings.getPricingCurrencySettings())
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.settings.pricingCurrency.loadFailed')))
  } finally {
    loading.value = false
  }
}

async function save() {
  if (!rateIsValid.value) {
    appStore.showError(t('admin.settings.pricingCurrency.invalidRate'))
    return
  }
  saving.value = true
  try {
    const saved = await adminAPI.settings.updatePricingCurrencySettings({
      settlement_currency: form.settlement_currency,
      usd_to_cny_rate: Number(form.usd_to_cny_rate),
    })
    applySettings(saved)
    await appStore.fetchPublicSettings?.(true)
    appStore.showSuccess(t('admin.settings.pricingCurrency.saved'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.settings.pricingCurrency.saveFailed')))
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>
