<template>
  <div>
    <label class="input-label">
      {{ t('admin.paidFallback.schedulingTier') }}
      <select class="input mt-1" :value="modelValue ? 'paid' : 'ordinary'" :disabled="disabled" data-testid="paid-fallback-account-field" @change="emit('update:modelValue', ($event.target as HTMLSelectElement).value === 'paid')">
        <option value="ordinary">{{ t('admin.paidFallback.ordinary') }}</option>
        <option value="paid" :disabled="!canEnable">{{ t('admin.paidFallback.paid') }}</option>
      </select>
    </label>
    <p class="input-hint">{{ t('admin.paidFallback.accountHint') }}</p>
  </div>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
withDefaults(defineProps<{ modelValue: boolean; disabled?: boolean; canEnable?: boolean }>(), { canEnable: true })
const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()
const { t } = useI18n()
</script>
