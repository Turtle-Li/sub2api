<template>
  <div class="min-w-0">
    <div class="flex min-h-10 items-center justify-between gap-4">
      <span class="text-sm font-medium text-gray-700 dark:text-gray-200">{{ t('admin.paidFallback.account') }}</span>
      <Toggle
        :model-value="modelValue"
        :disabled="disabled || (!canEnable && !modelValue)"
        :aria-label="t('admin.paidFallback.account')"
        data-testid="paid-fallback-account-field"
        @update:model-value="emit('update:modelValue', $event)"
      />
    </div>
    <p class="input-hint">{{ t('admin.paidFallback.accountHint') }}</p>
  </div>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
withDefaults(defineProps<{ modelValue: boolean; disabled?: boolean; canEnable?: boolean }>(), { canEnable: true })
const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()
const { t } = useI18n()
</script>
