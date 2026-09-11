<template>
  <div class="space-y-3">
    <p v-if="!options" role="alert" class="text-sm text-red-600">{{ t('payment.eligibility.invalidCatalog') }}</p>
    <template v-else>
      <section v-for="(option, index) in options" :key="rowKeys[index]" class="space-y-3 rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-600 dark:bg-dark-800">
        <div class="flex items-center justify-between gap-3">
          <strong class="text-sm">{{ option.label || t('payment.eligibility.tier', { number: index + 1 }) }}</strong>
          <button type="button" class="text-xs text-red-600" @click="remove(index)">{{ t('common.delete') }}</button>
        </div>
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <label v-for="field in textFields" :key="field.key" class="block">
            <span class="input-label">{{ t(field.label) }}</span>
            <input :value="option[field.key] || ''" type="text" class="input" @input="update(index, field.key, ($event.target as HTMLInputElement).value)" />
          </label>
          <label v-for="field in numericFields" :key="field.key" class="block">
            <span class="input-label">{{ t(field.label) }}</span>
            <input :value="option[field.key] || 0" type="number" :min="field.key === 'amount' ? 0.01 : 0" :step="field.step" class="input" @input="update(index, field.key, Number(($event.target as HTMLInputElement).value))" />
          </label>
        </div>
        <p class="text-xs text-gray-500">{{ t('payment.eligibility.concurrencyHint') }}</p>
        <label class="flex items-center gap-2 text-sm"><input type="checkbox" :checked="option.enabled !== false" @change="update(index, 'enabled', ($event.target as HTMLInputElement).checked)" />{{ t('payment.admin.forSale') }}</label>
        <PurchaseRulesEditor :model-value="rulesFor(option)" @update:model-value="update(index, 'purchase_rules', $event)" @validity="setValidity(index, $event)" />
      </section>
      <button type="button" class="btn btn-secondary" @click="add">{{ t('payment.eligibility.addTier') }}</button>
    </template>
  </div>
</template>
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import PurchaseRulesEditor from './PurchaseRulesEditor.vue'
import type { PurchaseRules } from '@/types/payment'
const props = defineProps<{ modelValue: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string]; validity: [value: boolean] }>()
const { t } = useI18n()
const invalidRows = ref(new Set<number>())
const rowKeys = ref<number[]>([])
let nextKey = 0
let lastEmitted = ''
const options = computed<Array<Record<string, unknown>> | null>(() => {
  try {
    const value = JSON.parse(props.modelValue)
    return Array.isArray(value) && value.every(item => item && typeof item === 'object' && !Array.isArray(item)) ? value : null
  } catch { return null }
})
watch(() => props.modelValue, value => {
  if (value !== lastEmitted) {
    invalidRows.value.clear()
    rowKeys.value = (options.value || []).map(() => ++nextKey)
    emit('validity', options.value !== null)
  }
  while (rowKeys.value.length < (options.value?.length || 0)) rowKeys.value.push(++nextKey)
}, { immediate: true })
const textFields = [{ key: 'label', label: 'payment.admin.planName' }, { key: 'description', label: 'payment.admin.planDescription' }]
const numericFields = [
  { key: 'amount', label: 'payment.admin.price', step: '0.01' },
  { key: 'balance_bonus', label: 'payment.admin.balanceBonus', step: '0.01' },
  { key: 'original_price', label: 'payment.admin.originalPrice', step: '0.01' },
  { key: 'concurrency', label: 'payment.admin.concurrencyTarget', step: '1' },
  { key: 'sort_order', label: 'payment.admin.sortOrder', step: '1' },
]
function rulesFor(option: Record<string, unknown>): PurchaseRules | undefined {
  return option.purchase_rules as PurchaseRules | undefined
}
function write(value: Array<Record<string, unknown>>) {
  lastEmitted = JSON.stringify(value, null, 2)
  emit('update:modelValue', lastEmitted)
}
function update(index: number, key: string, value: unknown) {
  if (!options.value) return
  write(options.value.map((option, i) => i === index ? { ...option, [key]: value } : option))
}
function setValidity(index: number, valid: boolean) {
  if (valid) invalidRows.value.delete(index); else invalidRows.value.add(index)
  emit('validity', invalidRows.value.size === 0)
}
function add() {
  if (!options.value) return
  write([...options.value, { amount: 0, label: '', description: '', balance_bonus: 0, enabled: false, sort_order: options.value.length }])
}
function remove(index: number) {
  if (!options.value) return
  rowKeys.value.splice(index, 1)
  write(options.value.filter((_, i) => i !== index))
  invalidRows.value = new Set([...invalidRows.value].filter(i => i !== index).map(i => i > index ? i - 1 : i))
  emit('validity', invalidRows.value.size === 0)
}
</script>
