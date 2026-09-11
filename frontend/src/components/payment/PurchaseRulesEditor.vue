<template>
  <fieldset class="space-y-3 rounded-lg border border-gray-200 p-3 dark:border-dark-600">
    <legend class="px-1 text-xs font-semibold text-gray-700 dark:text-gray-200">{{ t('payment.eligibility.adminTitle') }}</legend>
    <label class="block">
      <span class="input-label">{{ t('payment.eligibility.audience') }}</span>
      <input :value="userIDs" type="text" class="input" :aria-invalid="!idsValid" :placeholder="t('payment.eligibility.audiencePlaceholder')" @input="updateIDs" />
      <span class="mt-1 block text-xs text-gray-500">{{ t('payment.eligibility.audienceHint') }}</span>
    </label>
    <label class="block">
      <span class="input-label">{{ t('payment.eligibility.minimumLabel') }}</span>
      <input :value="amountText" type="number" min="0" step="0.01" class="input" :aria-invalid="!amountValid" @input="updateAmount" />
      <span class="mt-1 block text-xs text-gray-500">{{ t('payment.eligibility.minimumHint') }}</span>
    </label>
    <p v-if="!idsValid || !amountValid" role="alert" class="text-xs text-red-600">{{ t('payment.eligibility.invalidRules') }}</p>
  </fieldset>
</template>
<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PurchaseRules } from '@/types/payment'
const props = defineProps<{ modelValue?: PurchaseRules }>()
const emit = defineEmits<{ 'update:modelValue': [value: PurchaseRules]; validity: [value: boolean] }>()
const { t } = useI18n()
const userIDs = ref('')
const amountText = ref('0')
const idsValid = ref(true)
const amountValid = ref(true)
let lastEmitted = ''
function parsedIDs(): number[] | null {
  const entries = userIDs.value.trim() ? userIDs.value.trim().split(/[\s,，]+/) : []
  const ids = [...new Set(entries.map(Number))]
  return entries.every(value => /^\d+$/.test(value)) && ids.length <= 1000 && ids.every(id => Number.isSafeInteger(id) && id > 0) ? ids : null
}
function validateDraft() {
  const ids = parsedIDs()
  const amount = Number(amountText.value)
  idsValid.value = ids !== null
  amountValid.value = Number.isFinite(amount) && amount >= 0 && Math.abs(Math.round(amount * 100) - amount * 100) < 0.000001
  emit('validity', idsValid.value && amountValid.value)
  return ids !== null && amountValid.value ? { ...props.modelValue, visible_user_ids: ids, min_total_recharge: amount } : null
}
watch(() => props.modelValue, rules => {
  if (JSON.stringify(rules || {}) === lastEmitted) return
  if (rules != null && (typeof rules !== 'object' || Array.isArray(rules))) {
    idsValid.value = false
    amountValid.value = false
    emit('validity', false)
    return
  }
  const ids = rules?.visible_user_ids
  userIDs.value = Array.isArray(ids) ? ids.join(', ') : ids == null ? '' : String(ids)
  const amount = rules?.min_total_recharge
  amountText.value = amount == null ? '0' : String(amount)
  validateDraft()
  // Raw JSON must retain the typed contract, even when its text looks numeric.
  if ((ids != null && (!Array.isArray(ids) || ids.some(id => typeof id !== 'number'))) || (amount != null && typeof amount !== 'number')) {
    idsValid.value = ids == null || (Array.isArray(ids) && ids.every(id => typeof id === 'number' && Number.isSafeInteger(id) && id > 0))
    amountValid.value = amountValid.value && (amount == null || typeof amount === 'number')
    emit('validity', false)
  }
}, { immediate: true, deep: true })
function publishDraft() {
  const value = validateDraft()
  if (!value) return
  lastEmitted = JSON.stringify(value)
  emit('update:modelValue', value)
}
function updateIDs(event: Event) {
  userIDs.value = (event.target as HTMLInputElement).value
  publishDraft()
}
function updateAmount(event: Event) {
  amountText.value = (event.target as HTMLInputElement).value
  publishDraft()
}
</script>
