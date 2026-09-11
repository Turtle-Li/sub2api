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
      <input :value="modelValue?.min_total_recharge || 0" type="number" min="0" step="0.01" class="input" :aria-invalid="!amountValid" @input="updateAmount" />
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
const idsValid = ref(true)
const amountValid = ref(true)
watch(() => props.modelValue?.visible_user_ids, ids => {
  if (idsValid.value) userIDs.value = (ids || []).join(', ')
}, { immediate: true })
function updateIDs(event: Event) {
  userIDs.value = (event.target as HTMLInputElement).value
  const entries = userIDs.value.trim() ? userIDs.value.trim().split(/[\s,，]+/) : []
  const ids = [...new Set(entries.map(Number))]
  idsValid.value = entries.every(value => /^\d+$/.test(value)) && ids.length <= 1000 && ids.every(id => Number.isSafeInteger(id) && id > 0)
  if (idsValid.value) emit('update:modelValue', { ...props.modelValue, visible_user_ids: ids })
  emit('validity', idsValid.value && amountValid.value)
}
function updateAmount(event: Event) {
  const value = Number((event.target as HTMLInputElement).value)
  amountValid.value = Number.isFinite(value) && value >= 0 && Math.abs(Math.round(value * 100) - value * 100) < 0.000001
  if (amountValid.value) emit('update:modelValue', { ...props.modelValue, min_total_recharge: value })
  emit('validity', idsValid.value && amountValid.value)
}
</script>
